package ingestapi

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/ingest"
	"memd/internal/memory"
	"time"
)

// Events the Count hook receives; internal/observability maps each to its metric (memd.ingest.<event>).
const (
	// EventReceived counts the messages Receive handed out.
	EventReceived = "received"
	// EventAcked counts messages acked after their item was committed, or found unchanged, stale or superseded.
	EventAcked = "acked"
	// EventNacked counts messages handed back for redelivery after a delay.
	EventNacked = "nacked"
	// EventDeadLettered counts messages moved to the dead-letter destination.
	EventDeadLettered = "dead_lettered"
	// EventDropped counts messages acked without retaining them, because their org is deleted or being deleted.
	EventDropped = "dropped"
)

// Reasons of the consumer's own decisions. The others are the domain reasons of the errors (ErrorInfo reasons,
// e.g. INVALID_ARGUMENT, BANK_DELETING, INGEST_BACKLOG).
const (
	// reasonUndecodable: the body is not an IngestEnvelope in the encoding its content-type names.
	reasonUndecodable = "UNDECODABLE"
	// reasonTenantMismatch: the envelope names a tenant other than the pinned one.
	reasonTenantMismatch = "TENANT_MISMATCH"
	// reasonOrgMismatch: the envelope names an org other than the pinned one.
	reasonOrgMismatch = "ORG_MISMATCH"
	// reasonMaxAttempts: the message kept failing until Config.MaxAttempts deliveries.
	reasonMaxAttempts = "MAX_ATTEMPTS"
	// reasonOrgDeleted: the org is not found and has a deletion receipt.
	reasonOrgDeleted = "ORG_DELETED"
)

// action is how a message is settled.
type action int

// Settlement actions.
const (
	// actAck acks: the message is done.
	actAck action = iota + 1
	// actDrop acks without retaining: the message belongs to a deleted org and must not come back.
	actDrop
	// actNack asks for redelivery after decision.delay.
	actNack
	// actDeadLetter moves the message to the dead-letter destination.
	actDeadLetter
)

// decision is one message's settlement.
type decision struct {
	act action
	// reason is a bounded code: the metric tag, and the start of a dead letter's reason.
	reason string
	// detail is the human-readable part of a dead letter's reason.
	detail string
	// delay is the Nack delay.
	delay time.Duration
	// pause is how long to stop receiving (a backlog rejection's retry delay).
	pause time.Duration
}

// deadLetter is the decision to dead-letter with reason and detail.
func deadLetter(reason, detail string) decision {
	return decision{act: actDeadLetter, reason: reason, detail: detail}
}

// decide maps a message's RetainBatch outcome to its settlement. st and err are the item's status and error, or,
// when the call failed as a whole, BatchUnspecified and the call's error; attempt is the message's delivery count
// (0 = unknown) and orgGone whether its org has a deletion receipt. The error class decides before the status:
// an org or bank deletion error arrives as a REJECTED item too.
//
//   - ACCEPTED, UNCHANGED, STALE, SUPERSEDED: Ack (the write, if any, has committed).
//   - ORG_DELETING, or ORG_NOT_FOUND for an org with a deletion receipt: drop (Ack, never retained), so content
//     of a deleted org doesn't come back.
//   - RESOURCE_EXHAUSTED (the backlog gate): Nack after its RetryInfo delay and pause receiving for it; it
//     never counts towards the attempts, since the message is not at fault.
//   - ORG_NOT_FOUND without a receipt (not created yet), BANK_DELETING (the bank may be re-created), FAILED and
//     any transient error: Nack with exponential backoff, and dead-letter (MAX_ATTEMPTS) at Config.MaxAttempts.
//   - REJECTED, and a permanent call error (invalid argument, failed precondition, not found, already exists):
//     dead-letter with the error's reason.
func decide(st memory.BatchStatus, err error, attempt int, orgGone bool, cfg Config) decision {
	if err == nil {
		switch st {
		case memory.BatchAccepted, memory.BatchUnchanged, memory.BatchStale, memory.BatchSuperseded:
			return decision{act: actAck}
		case memory.BatchUnspecified, memory.BatchRejected, memory.BatchFailed:
		}
		err = fmt.Errorf("%w: retain batch item without an outcome (status %d)", domain.ErrUnavailable, st)
	}
	switch {
	case errors.Is(err, domain.ErrOrgDeleting):
		return decision{act: actDrop, reason: reasonOf(err)}
	case errors.Is(err, domain.ErrOrgNotFound) && orgGone:
		return decision{act: actDrop, reason: reasonOrgDeleted}
	case errors.Is(err, domain.ErrResourceExhausted):
		delay, ok := domain.RetryDelay(err)
		if !ok || delay <= 0 {
			delay = backoff(attempt, cfg)
		}
		return decision{act: actNack, reason: reasonOf(err), delay: delay, pause: delay}
	case errors.Is(err, domain.ErrOrgNotFound), errors.Is(err, domain.ErrBankDeleting):
		return retry(err, attempt, cfg)
	case st == memory.BatchRejected || permanent(err):
		return deadLetter(reasonOf(err), err.Error())
	default:
		return retry(err, attempt, cfg)
	}
}

// retry is a Nack with backoff, or a dead letter once attempt reaches Config.MaxAttempts.
func retry(err error, attempt int, cfg Config) decision {
	if attempt >= cfg.MaxAttempts {
		return deadLetter(reasonMaxAttempts, fmt.Sprintf("failed %d deliveries, last with %s: %v", attempt,
			reasonOf(err), err))
	}
	return decision{act: actNack, reason: reasonOf(err), delay: backoff(attempt, cfg)}
}

// backoff is Config.BackoffInitial doubled per delivery after the first, capped at Config.BackoffMax.
func backoff(attempt int, cfg Config) time.Duration {
	d := cfg.BackoffInitial
	for n := 1; n < attempt && d < cfg.BackoffMax; n++ {
		d *= 2
	}
	return min(d, cfg.BackoffMax)
}

// permanent reports whether err will fail the same way on every redelivery.
func permanent(err error) bool {
	return errors.Is(err, domain.ErrInvalidArgument) || errors.Is(err, domain.ErrFailedPrecondition) ||
		errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrAlreadyExists)
}

// reasonOf is err's domain reason, CANCELLED or DEADLINE_EXCEEDED for a context error, else UNKNOWN.
func reasonOf(err error) string {
	switch r := domain.ReasonOf(err); {
	case r != "":
		return r
	case errors.Is(err, context.Canceled):
		return "CANCELLED"
	case errors.Is(err, context.DeadlineExceeded):
		return "DEADLINE_EXCEEDED"
	default:
		return "UNKNOWN"
	}
}

// settled is one message with its decision.
type settled struct {
	m ingest.Message
	d decision
}

// settlements collects the decisions of a set of messages, to settle them in as few calls as possible.
type settlements []settled

// add records m's decision.
func (s *settlements) add(m ingest.Message, d decision) { *s = append(*s, settled{m: m, d: d}) }

// settle applies the decisions: one Ack for the acked and dropped messages, one Nack per delay and one
// DeadLetter per reason. A dead letter that fails is nacked instead, so it is neither lost nor held in flight;
// an Ack or Nack failure is only logged (an unsettled message is redelivered).
func (c *Consumer) settle(ctx context.Context, s settlements) {
	var acks []ingest.Message
	nacks := map[time.Duration][]settled{}
	dead := map[string][]settled{}
	for _, x := range s {
		switch x.d.act {
		case actAck, actDrop:
			acks = append(acks, x.m)
		case actNack:
			nacks[x.d.delay] = append(nacks[x.d.delay], x)
		case actDeadLetter:
			full := x.d.reason + ": " + x.d.detail
			dead[full] = append(dead[full], x)
		}
	}
	if len(acks) > 0 {
		if err := c.d.Source.Ack(ctx, acks...); err != nil {
			c.d.Logger.Warn("ingest: ack failed; the messages may be redelivered", "err", err, "messages", len(acks))
		} else {
			for _, x := range s {
				c.countSettled(x)
			}
		}
	}
	for delay, xs := range nacks {
		c.nack(ctx, delay, xs)
	}
	for reason, xs := range dead {
		if err := c.d.Source.DeadLetter(ctx, reason, messages(xs)...); err != nil {
			c.d.Logger.Warn("ingest: dead letter failed; nacking instead", "err", err, "messages", len(xs))
			for _, x := range xs {
				c.nack(ctx, backoff(x.m.Attempt, c.cfg), []settled{x})
			}
			continue
		}
		for _, x := range xs {
			c.count(EventDeadLettered, x.d.reason, 1)
		}
	}
}

// nack hands xs back for redelivery after delay and counts them.
func (c *Consumer) nack(ctx context.Context, delay time.Duration, xs []settled) {
	if err := c.d.Source.Nack(ctx, delay, messages(xs)...); err != nil {
		c.d.Logger.Warn("ingest: nack failed; the messages will be redelivered", "err", err, "messages", len(xs))
		return
	}
	for _, x := range xs {
		c.count(EventNacked, x.d.reason, 1)
	}
}

// countSettled counts an acked or dropped message.
func (c *Consumer) countSettled(x settled) {
	switch x.d.act {
	case actAck:
		c.count(EventAcked, "", 1)
	case actDrop:
		c.count(EventDropped, x.d.reason, 1)
	case actNack, actDeadLetter:
	}
}

// messages returns the messages of xs.
func messages(xs []settled) []ingest.Message {
	out := make([]ingest.Message, len(xs))
	for i, x := range xs {
		out[i] = x.m
	}
	return out
}
