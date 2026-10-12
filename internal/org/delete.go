package org

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/store"
	"time"
)

// DeleteConfig bounds and paces one delete_org run.
type DeleteConfig struct {
	// FanoutBatch is how many banks one transaction enqueues a delete_bank for; 0 = DefaultFanoutBatch.
	FanoutBatch int
	// SweepBatch is the row limit of one OrgRepo.DeleteBatch transaction; 0 = DefaultSweepBatch.
	SweepBatch int
	// MaxBatches is how many sweep batches one run commits before it yields and re-enqueues itself; 0 =
	// DefaultMaxBatches.
	MaxBatches int
	// BlobPageSize is the blob keys per listed page (deleted with a fence check between pages); 0 =
	// DefaultBlobPageSize.
	BlobPageSize int
	// BankWait is how long the delete waits before it looks again while banks of the org are still being deleted;
	// 0 = DefaultBankWait.
	BankWait time.Duration
	// BlobGrace is the least time between the request and the blob sweep, so that a request which passed its org
	// check before the tombstone has finished writing its blob by then; 0 = none. The server sets it from its config
	// file (deletion.blob_grace, at least the RPC deadline by default).
	BlobGrace time.Duration
	// VerifyAttempts is how many times one run sweeps again after a failed verification before it fails (and is
	// retried); 0 = DefaultVerifyAttempts.
	VerifyAttempts int
}

// Org delete defaults.
const (
	// DefaultFanoutBatch is the default banks per fan-out transaction.
	DefaultFanoutBatch = 500
	// DefaultSweepBatch is the default rows per sweep transaction.
	DefaultSweepBatch = 1000
	// DefaultMaxBatches is the default sweep batches per run.
	DefaultMaxBatches = 100
	// DefaultBlobPageSize is the default keys per listed blob page.
	DefaultBlobPageSize = 1000
	// DefaultBankWait is the default wait for the org's bank deletes.
	DefaultBankWait = 5 * time.Second
	// DefaultVerifyAttempts is the default sweep-and-verify rounds per run.
	DefaultVerifyAttempts = 3
	// BlockedDelay is how long the delete waits when another operation of the org is still processing.
	BlockedDelay = 5 * time.Second
)

// withDefaults fills zero fields.
func (c DeleteConfig) withDefaults() DeleteConfig {
	def := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&c.FanoutBatch, DefaultFanoutBatch)
	def(&c.SweepBatch, DefaultSweepBatch)
	def(&c.MaxBatches, DefaultMaxBatches)
	def(&c.BlobPageSize, DefaultBlobPageSize)
	def(&c.VerifyAttempts, DefaultVerifyAttempts)
	if c.BankWait <= 0 {
		c.BankWait = DefaultBankWait
	}
	return c
}

// DeleteOrg is the api side of an org delete. In one transaction it locks the org row (waiting for bank creations
// in flight), tombstones the org and then every live bank of it, cancels their pending jobs (Queue.CancelOrg spares
// delete_bank and delete_org), starts an IN_PROGRESS receipt and enqueues (or joins) delete_org, and returns that
// operation and the receipt. An org already being deleted returns its current receipt and joins its delete_org (or
// enqueues one if its delete stopped). An unknown org is domain.ErrOrgNotFound. It takes no change seq: nothing
// here is memory content, and Seq refuses a tombstoned bank.
func (s *Service) DeleteOrg(ctx context.Context, o domain.OrgRef) (domain.Operation, domain.DeletionReceipt, error) {
	now := s.d.Clock.Now()
	var (
		op domain.Operation
		rc domain.DeletionReceipt
	)
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		op, rc = domain.Operation{}, domain.DeletionReceipt{}
		org, err := tx.Orgs().LockForDelete(ctx, o)
		if err != nil {
			return err
		}
		if !org.Deleting() {
			if rc, err = s.tombstone(ctx, tx, o, now); err != nil {
				return err
			}
		} else if rc, err = s.receiptOf(ctx, tx, org); err != nil {
			return err
		}
		id, _, err := jobs.EnqueueDeleteOrg(ctx, tx, o, rc.ID)
		if err != nil {
			return err
		}
		op, err = tx.Queue().Get(ctx, o.Scope(), id)
		return err
	})
	if err != nil {
		return domain.Operation{}, domain.DeletionReceipt{}, fmt.Errorf("delete org: %w", err)
	}
	return op, rc, nil
}

// tombstone marks live org o and its banks as being deleted, cancels their pending jobs and creates the receipt,
// in tx (after LockForDelete).
func (s *Service) tombstone(
	ctx context.Context, tx store.Tx, o domain.OrgRef, now time.Time,
) (domain.DeletionReceipt, error) {
	banks, err := tx.Orgs().MarkDeleting(ctx, o, now)
	if err != nil {
		return domain.DeletionReceipt{}, err
	}
	if _, err := tx.Queue().CancelOrg(ctx, o); err != nil {
		return domain.DeletionReceipt{}, err
	}
	return s.createReceipt(ctx, tx, o, now, banks)
}

// createReceipt starts an IN_PROGRESS receipt of org o in tx.
func (s *Service) createReceipt(
	ctx context.Context, tx store.Tx, o domain.OrgRef, requested time.Time, banks int,
) (domain.DeletionReceipt, error) {
	rc := domain.DeletionReceipt{
		Tenant: o.Tenant, OrgIDHash: domain.OrgIDHash(o), Status: domain.ReceiptStatusInProgress,
		RequestedAt: requested, Banks: banks, ServerVersion: s.d.ServerVersion,
	}
	id, err := tx.Receipts().Create(ctx, rc)
	if err != nil {
		return domain.DeletionReceipt{}, err
	}
	rc.ID = id
	return rc, nil
}

// receiptOf returns the receipt of tombstoned org's running delete: its latest receipt, unless that is missing or
// already completed (an earlier org of the same id), in which case it starts one dated from the tombstone.
func (s *Service) receiptOf(ctx context.Context, tx store.Tx, org domain.Org) (domain.DeletionReceipt, error) {
	rc, err := tx.Receipts().Latest(ctx, org.Ref)
	switch {
	case err == nil && rc.Status != domain.ReceiptStatusCompleted:
		return rc, nil
	case err != nil && !errors.Is(err, domain.ErrNotFound):
		return domain.DeletionReceipt{}, err
	}
	return s.createReceipt(ctx, tx, org.Ref, *org.DeletingAt, 0)
}

// GetDeletionReceipt returns the receipt of org o's most recent delete (looked up by domain.OrgIDHash); an org never
// deleted is domain.ErrNotFound.
func (s *Service) GetDeletionReceipt(ctx context.Context, o domain.OrgRef) (domain.DeletionReceipt, error) {
	var rc domain.DeletionReceipt
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		rc, err = tx.Receipts().Latest(ctx, o)
		return err
	})
	if err != nil {
		return domain.DeletionReceipt{}, fmt.Errorf("deletion receipt: %w", err)
	}
	return rc, nil
}

// ResumeStalledDeletes re-enqueues delete_org for every tombstoned org whose delete stopped (failed for good, or
// reclaimed at its attempt cap) and returns how many it inserted, so an org is never left tombstoned forever. Each
// org gets one short transaction; an org whose delete just finished is skipped, and an error on one org doesn't stop
// the rest.
func (s *Service) ResumeStalledDeletes(ctx context.Context) (int, error) {
	orgs, err := s.d.Maintenance.StalledOrgDeletes(ctx)
	if err != nil {
		return 0, fmt.Errorf("list stalled org deletes: %w", err)
	}
	inserted := 0
	var errs []error
	for _, o := range orgs {
		var joined bool
		err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
			joined = true
			org, err := tx.Orgs().Get(ctx, o)
			if err != nil || !org.Deleting() {
				return err
			}
			rc, err := s.receiptOf(ctx, tx, org)
			if err != nil {
				return err
			}
			_, joined, err = jobs.EnqueueDeleteOrg(ctx, tx, o, rc.ID)
			return err
		})
		switch {
		case errors.Is(err, domain.ErrNotFound):
		case err != nil:
			errs = append(errs, fmt.Errorf("org %s: %w", o, err))
		case !joined:
			inserted++
		}
	}
	return inserted, errors.Join(errs...)
}

// PruneReceipts deletes the receipts of deletes completed before before and returns how many.
func (s *Service) PruneReceipts(ctx context.Context, before time.Time) (int, error) {
	var n int
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		var err error
		n, err = tx.Receipts().Prune(ctx, before)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("prune deletion receipts: %w", err)
	}
	return n, nil
}
