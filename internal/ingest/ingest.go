package ingest

import (
	"context"
	"errors"
	"time"
)

// ErrClosed is returned by Receive after Close. It is neither a context error nor a domain error class: a consumer
// that sees it stops its loop.
var ErrClosed = errors.New("ingest: source closed")

// Attribute names and values with a meaning to the consumer.
const (
	// AttrContentType names the attribute that tells how Message.Body is encoded.
	AttrContentType = "content-type"
	// ContentTypeJSON is the default encoding: protojson.
	ContentTypeJSON = "application/json"
	// ContentTypeProto is binary protobuf.
	ContentTypeProto = "application/x-protobuf"
)

// Message is one delivery of one queue message.
type Message struct {
	// ID is the broker's id for the message, for logs only.
	ID string
	// Key is the ordering key (a Kafka key, an SQS MessageGroupId, a Pub/Sub ordering key). Producers set it to the
	// document id. Empty means no ordering.
	Key string
	// Body is the encoded payload.
	Body []byte
	// Attributes are the broker's headers or attributes; see [AttrContentType].
	Attributes map[string]string
	// Attempt is the 1-based delivery count when the broker reports it, and 0 when it does not.
	Attempt int
	// Handle is the adapter's own settlement token. The consumer passes the message back unchanged and never
	// inspects it.
	Handle any
}

// Source is a queue subscription with at-least-once delivery. The package comment states the full contract.
type Source interface {
	// Receive returns up to max messages, waiting at most the provider's long-poll time. It returns an empty slice
	// and no error on a timeout, the context's error when ctx ends, and ErrClosed after Close.
	Receive(ctx context.Context, max int) ([]Message, error)
	// Ack settles messages for good. It is idempotent, and acking an expired lease or a closed source is not an
	// error.
	Ack(ctx context.Context, msgs ...Message) error
	// Nack asks for redelivery no sooner than delay, keeping per-Key order where the provider has it.
	Nack(ctx context.Context, delay time.Duration, msgs ...Message) error
	// DeadLetter moves messages to the configured dead-letter destination, with the reason, and settles them.
	DeadLetter(ctx context.Context, reason string, msgs ...Message) error
	// Close stops receiving. Messages not yet settled are redelivered later, to this or another consumer.
	Close() error
}
