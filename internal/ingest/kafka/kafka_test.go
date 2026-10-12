package kafka

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"memd/internal/domain"
	"memd/internal/ingest"
	"memd/internal/ingest/ingesttest"
	"net"
	"os"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// kafkaImage is the broker the tests run against: Apache Kafka, single node in KRaft mode.
const kafkaImage = "apache/kafka-native:4.1.1"

// requireDockerEnv makes a missing broker (no Docker) fail the tests instead of skipping them.
const requireDockerEnv = "MEMD_REQUIRE_DOCKER"

var (
	// brokerAddr is the host:port of the shared broker, set by TestMain.
	brokerAddr string
	// brokerErr records why the broker could not start (e.g. no Docker); tests that need it skip.
	brokerErr error
	// topicSeq numbers the per-test topics.
	topicSeq atomic.Int64
)

// TestMain starts one broker for the whole test binary and terminates it afterwards.
func TestMain(m *testing.M) {
	os.Exit(runWithKafka(m))
}

// runWithKafka runs the tests against a fresh broker. The broker must advertise the address clients dial, so the
// host port is chosen before the container starts. A start failure is recorded in brokerErr rather than failing
// the binary, so the tests skip when Docker is unavailable.
func runWithKafka(m *testing.M) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	port, err := freePort()
	if err != nil {
		brokerErr = err
		return m.Run()
	}
	ctr, err := testcontainers.Run(ctx, kafkaImage,
		testcontainers.WithEnv(kafkaEnv(port)),
		testcontainers.WithExposedPorts("9092/tcp"),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.PortBindings = network.PortMap{
				network.MustParsePort("9092/tcp"): {{HostPort: strconv.Itoa(port)}},
			}
		}),
		testcontainers.WithWaitStrategy(wait.ForLog("Kafka Server started").WithStartupTimeout(2*time.Minute)),
	)
	defer func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			log.Printf("terminate kafka: %v", err)
		}
	}()
	if err != nil {
		brokerErr = fmt.Errorf("start kafka: %w", err)
		return m.Run()
	}
	brokerAddr = net.JoinHostPort("localhost", strconv.Itoa(port))
	return m.Run()
}

// kafkaEnv configures a single-node KRaft broker advertising localhost:port, with internal topics that fit one
// node and no initial rebalance delay, so a fresh group's first join is immediate.
func kafkaEnv(port int) map[string]string {
	return map[string]string{
		"KAFKA_NODE_ID":                                  "1",
		"KAFKA_PROCESS_ROLES":                            "broker,controller",
		"KAFKA_LISTENERS":                                "PLAINTEXT://:9092,CONTROLLER://:9093",
		"KAFKA_ADVERTISED_LISTENERS":                     "PLAINTEXT://localhost:" + strconv.Itoa(port),
		"KAFKA_CONTROLLER_LISTENER_NAMES":                "CONTROLLER",
		"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP":           "CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT",
		"KAFKA_CONTROLLER_QUORUM_VOTERS":                 "1@localhost:9093",
		"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR":         "1",
		"KAFKA_OFFSETS_TOPIC_NUM_PARTITIONS":             "1",
		"KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR": "1",
		"KAFKA_TRANSACTION_STATE_LOG_MIN_ISR":            "1",
		"KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS":         "0",
	}
}

// freePort returns a TCP port free on the host at the time of the call.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("free port: %w", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// requireBroker skips t without a broker, or fails it when MEMD_REQUIRE_DOCKER is set.
func requireBroker(t *testing.T) {
	t.Helper()
	if brokerErr != nil {
		if os.Getenv(requireDockerEnv) != "" {
			t.Fatalf("Kafka unavailable and %s is set: %v", requireDockerEnv, brokerErr)
		}
		t.Skipf("Kafka unavailable: %v", brokerErr)
	}
}

// fixture is one test's subscription: a fresh topic with its dead-letter topic and group.
type fixture struct {
	cfg   Config
	admin *kadm.Client
	prod  *kgo.Client
}

// newFixture creates a topic with the given partition count, a one-partition dead-letter topic and a group name
// no other test uses.
func newFixture(t *testing.T, partitions int32) *fixture {
	t.Helper()
	requireBroker(t)
	topic := fmt.Sprintf("memd-test-%d", topicSeq.Add(1))
	cfg := Config{
		Brokers: []string{brokerAddr}, Topic: topic, Group: topic + "-group", DeadLetterTopic: topic + "-dlq",
		PollWait: 100 * time.Millisecond,
	}
	prod, err := kgo.NewClient(kgo.SeedBrokers(brokerAddr))
	if err != nil {
		t.Fatalf("producer: %v", err)
	}
	t.Cleanup(prod.Close)
	admin := kadm.NewClient(prod)
	for name, n := range map[string]int32{cfg.Topic: partitions, cfg.DeadLetterTopic: 1} {
		if _, err := admin.CreateTopic(t.Context(), n, 1, nil, name); err != nil {
			t.Fatalf("create topic %s: %v", name, err)
		}
	}
	return &fixture{cfg: cfg, admin: admin, prod: prod}
}

// open returns a new consumer in the fixture's group.
func (f *fixture) open(t *testing.T) ingest.Source {
	t.Helper()
	src, err := New(f.cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return src
}

// publish produces msgs to the topic in order, keyed by Message.Key, with the attributes as headers.
func (f *fixture) publish(t *testing.T, msgs ...ingest.Message) {
	t.Helper()
	for _, m := range msgs {
		rec := &kgo.Record{Topic: f.cfg.Topic, Value: m.Body}
		if m.Key != "" {
			rec.Key = []byte(m.Key)
		}
		for _, k := range slices.Sorted(maps.Keys(m.Attributes)) {
			rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: k, Value: []byte(m.Attributes[k])})
		}
		if err := f.prod.ProduceSync(t.Context(), rec).FirstErr(); err != nil {
			t.Fatalf("produce: %v", err)
		}
	}
}

// deadLetters reads the whole dead-letter topic, up to its end offset at the time of the call.
func (f *fixture) deadLetters(t *testing.T) []*kgo.Record {
	t.Helper()
	ends, err := f.admin.ListEndOffsets(t.Context(), f.cfg.DeadLetterTopic)
	if err != nil {
		t.Fatalf("list end offsets: %v", err)
	}
	end, _ := ends.Lookup(f.cfg.DeadLetterTopic, 0)
	if end.Offset <= 0 {
		return nil
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokerAddr), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
		f.cfg.DeadLetterTopic: {0: kgo.NewOffset().AtStart()},
	}))
	if err != nil {
		t.Fatalf("dead-letter reader: %v", err)
	}
	defer cl.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var out []*kgo.Record
	for int64(len(out)) < end.Offset {
		fetches := cl.PollFetches(ctx)
		if err := ctx.Err(); err != nil {
			t.Fatalf("read dead letters: %d of %d before the timeout", len(out), end.Offset)
		}
		out = append(out, fetches.Records()...)
	}
	return out
}

// committed returns the group's committed offset for partition 0 of the topic, -1 when there is none.
func (f *fixture) committed(t *testing.T) int64 {
	t.Helper()
	offs, err := f.admin.FetchOffsets(t.Context(), f.cfg.Group)
	if err != nil {
		t.Fatalf("fetch offsets: %v", err)
	}
	if o, ok := offs.Lookup(f.cfg.Topic, 0); ok && o.Err == nil {
		return o.At
	}
	return -1
}

// TestContract runs the shared contract suite on a real broker, one topic and group per case. The topic has two
// partitions, so the suite also covers records spread over partitions.
func TestContract(t *testing.T) {
	ingesttest.Run(t, func(t *testing.T) ingesttest.Harness {
		f := newFixture(t, 2)
		return ingesttest.Harness{
			Open:    f.open,
			Publish: f.publish,
			DeadLetters: func(t *testing.T) []ingest.Message {
				var out []ingest.Message
				for _, r := range f.deadLetters(t) {
					out = append(out, ingest.Message{Key: string(r.Key), Body: r.Value})
				}
				return out
			},
		}
	})
}

// TestCommitOnlyPastContiguousAcks pins the commit model: an ack past an unsettled record commits nothing, and
// acking that record commits past every settled record after it.
func TestCommitOnlyPastContiguousAcks(t *testing.T) {
	f := newFixture(t, 1)
	f.publish(t, ingest.Message{Body: []byte("m0")}, ingest.Message{Body: []byte("m1")},
		ingest.Message{Body: []byte("m2")})
	src := f.open(t)
	got := receive(t, src, 3)
	if err := src.Ack(t.Context(), got[1], got[2]); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if c := f.committed(t); c > 0 {
		t.Fatalf("committed offset %d with offset 0 unsettled", c)
	}
	if err := src.Ack(t.Context(), got[0]); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if c := f.committed(t); c != 3 {
		t.Fatalf("committed offset = %d after acking 0..2, want 3", c)
	}
}

// TestDeadLetterCarriesReason checks the dead-letter record: same key and value, the original headers, and the
// failure headers naming the reason and the source position.
func TestDeadLetterCarriesReason(t *testing.T) {
	f := newFixture(t, 1)
	f.publish(t, ingest.Message{
		Key: "doc-1", Body: []byte("payload"),
		Attributes: map[string]string{ingest.AttrContentType: ingest.ContentTypeJSON, HeaderReason: "stale"},
	})
	src := f.open(t)
	m := receive(t, src, 1)[0]
	if err := src.DeadLetter(t.Context(), "undecodable", m); err != nil {
		t.Fatalf("DeadLetter: %v", err)
	}
	dead := f.deadLetters(t)
	if len(dead) != 1 {
		t.Fatalf("dead letters = %d, want 1", len(dead))
	}
	r := dead[0]
	if string(r.Key) != "doc-1" || string(r.Value) != "payload" {
		t.Fatalf("dead letter key/value = %q/%q, want doc-1/payload", r.Key, r.Value)
	}
	headers := map[string][]string{}
	for _, h := range r.Headers {
		headers[h.Key] = append(headers[h.Key], string(h.Value))
	}
	want := map[string]string{
		ingest.AttrContentType: ingest.ContentTypeJSON, HeaderReason: "undecodable", HeaderTopic: f.cfg.Topic,
		HeaderPartition: "0", HeaderOffset: "0", HeaderAttempt: "1",
	}
	for k, v := range want {
		if !slices.Equal(headers[k], []string{v}) {
			t.Errorf("header %s = %v, want [%s]", k, headers[k], v)
		}
	}
	if c := f.committed(t); c != 1 {
		t.Errorf("committed offset = %d after the dead letter, want 1", c)
	}
}

// TestNewRequiresDeadLetterTopic checks that a source without a dead-letter destination is never built.
func TestNewRequiresDeadLetterTopic(t *testing.T) {
	for name, dlq := range map[string]string{"missing": "", "same as topic": "in"} {
		_, err := New(Config{Brokers: []string{"localhost:9092"}, Topic: "in", Group: "g", DeadLetterTopic: dlq})
		if !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("%s dead-letter topic: New error = %v, want ErrInvalidArgument", name, err)
		}
	}
}

// receive polls src until it has delivered n messages.
func receive(t *testing.T, src ingest.Source, n int) []ingest.Message {
	t.Helper()
	var got []ingest.Message
	for deadline := time.Now().Add(20 * time.Second); len(got) < n; {
		if time.Now().After(deadline) {
			t.Fatalf("received %d of %d messages before the timeout", len(got), n)
		}
		batch, err := src.Receive(t.Context(), n-len(got))
		if err != nil {
			t.Fatalf("Receive: %v", err)
		}
		got = append(got, batch...)
	}
	return got
}
