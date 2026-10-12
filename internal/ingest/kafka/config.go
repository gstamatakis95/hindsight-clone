package kafka

import (
	"context"
	"crypto/tls"
	"memd/internal/domain"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// Defaults of the optional Config fields.
const (
	// DefaultPollWait is how long Receive waits for a message before it returns an empty slice.
	DefaultPollWait = time.Second
	// DefaultMaxBuffered is how many records of one partition the source holds before it pauses fetching it.
	DefaultMaxBuffered = 10_000
	// DefaultClientID is the client id the brokers see in their logs and quotas.
	DefaultClientID = "memd"
)

// SASL mechanisms Config.SASL.Mechanism accepts.
const (
	// MechanismPlain is SASL/PLAIN: the password travels as is, so use it only over TLS.
	MechanismPlain = "PLAIN"
	// MechanismSCRAMSHA256 is SASL/SCRAM with SHA-256.
	MechanismSCRAMSHA256 = "SCRAM-SHA-256"
	// MechanismSCRAMSHA512 is SASL/SCRAM with SHA-512.
	MechanismSCRAMSHA512 = "SCRAM-SHA-512"
)

// Config is everything a [Source] needs. The package comment says which fields are deployment settings
// (environment) and which are behaviour (the server config file).
type Config struct {
	// Brokers are the seed brokers, host:port. Required.
	Brokers []string
	// Topic is the topic to consume. Required.
	Topic string
	// Group is the consumer group id. Every memd ingest process of one deployment uses the same group, so the
	// topic's partitions are spread over them. Required.
	Group string
	// DeadLetterTopic receives the dead-lettered messages. Required, and different from Topic.
	DeadLetterTopic string
	// ClientID names the client to the brokers (DefaultClientID when empty).
	ClientID string
	// TLS, when set, dials the brokers over TLS with this configuration; nil means plaintext.
	TLS *tls.Config
	// SASL authenticates the connection; the zero value means no SASL.
	SASL SASL
	// PollWait is how long Receive waits for a message before it returns an empty slice (DefaultPollWait when 0).
	PollWait time.Duration
	// MaxBuffered is how many records of one partition the source holds, settled ones behind an unsettled one
	// included, before it pauses fetching that partition (DefaultMaxBuffered when 0).
	MaxBuffered int
}

// SASL is the SASL authentication of a [Config].
type SASL struct {
	// Mechanism is MechanismPlain, MechanismSCRAMSHA256 or MechanismSCRAMSHA512; empty disables SASL.
	Mechanism string
	// Username is the SASL user. Required with a Mechanism.
	Username string
	// Password is the SASL password.
	Password string
}

// withDefaults returns c with the optional fields filled in.
func (c Config) withDefaults() Config {
	if c.ClientID == "" {
		c.ClientID = DefaultClientID
	}
	if c.PollWait == 0 {
		c.PollWait = DefaultPollWait
	}
	if c.MaxBuffered == 0 {
		c.MaxBuffered = DefaultMaxBuffered
	}
	return c
}

// validate reports the first invalid field as domain.ErrInvalidArgument naming it.
func (c Config) validate() error {
	switch {
	case len(c.Brokers) == 0:
		return domain.InvalidField("brokers", "at least one broker is required")
	case c.Topic == "":
		return domain.InvalidField("topic", "the topic is required")
	case c.Group == "":
		return domain.InvalidField("group", "the consumer group is required")
	case c.DeadLetterTopic == "":
		return domain.InvalidField("dead_letter_topic", "a dead-letter topic is required, so nothing is dropped")
	case c.DeadLetterTopic == c.Topic:
		return domain.InvalidField("dead_letter_topic", "must differ from the topic, or dead letters are re-consumed")
	case c.PollWait < 0:
		return domain.InvalidField("poll_wait", "must not be negative")
	case c.MaxBuffered < 0:
		return domain.InvalidField("max_buffered", "must not be negative")
	}
	for _, b := range c.Brokers {
		if b == "" {
			return domain.InvalidField("brokers", "a broker address is empty")
		}
	}
	return c.SASL.validate()
}

// validate checks the mechanism name and that it has a user.
func (s SASL) validate() error {
	switch s.Mechanism {
	case "":
		return nil
	case MechanismPlain, MechanismSCRAMSHA256, MechanismSCRAMSHA512:
	default:
		return domain.InvalidField("sasl.mechanism", "unknown SASL mechanism %q", s.Mechanism)
	}
	if s.Username == "" {
		return domain.InvalidField("sasl.username", "required with SASL mechanism %s", s.Mechanism)
	}
	return nil
}

// mechanism returns the franz-go mechanism, or nil when SASL is off.
func (s SASL) mechanism() sasl.Mechanism {
	switch s.Mechanism {
	case MechanismPlain:
		return plain.Auth{User: s.Username, Pass: s.Password}.AsMechanism()
	case MechanismSCRAMSHA256:
		return scram.Auth{User: s.Username, Pass: s.Password}.AsSha256Mechanism()
	case MechanismSCRAMSHA512:
		return scram.Auth{User: s.Username, Pass: s.Password}.AsSha512Mechanism()
	}
	return nil
}

// partitionHook is the signature of franz-go's revoke and lost callbacks.
type partitionHook = func(ctx context.Context, cl *kgo.Client, partitions map[string][]int32)

// clientOptions returns the franz-go options of a group consumer that commits by hand, never mid-poll across a
// rebalance, and starts a new group at the earliest offset. The callbacks are the source's revoke and lost hooks.
func (c Config) clientOptions(onRevoked, onLost partitionHook) []kgo.Opt {
	opts := []kgo.Opt{
		kgo.SeedBrokers(c.Brokers...),
		kgo.ClientID(c.ClientID),
		kgo.ConsumerGroup(c.Group),
		kgo.ConsumeTopics(c.Topic),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
		kgo.OnPartitionsRevoked(onRevoked),
		kgo.OnPartitionsLost(onLost),
	}
	if c.TLS != nil {
		opts = append(opts, kgo.DialTLSConfig(c.TLS))
	}
	if m := c.SASL.mechanism(); m != nil {
		opts = append(opts, kgo.SASL(m))
	}
	return opts
}
