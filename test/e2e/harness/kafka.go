package harness

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// KafkaImage is the broker the queue-ingestion tests run: Apache Kafka, one node in KRaft mode (the image the Kafka
// adapter's own tests use).
const KafkaImage = "apache/kafka-native:4.1.1"

// Kafka is a single-broker Kafka for one test, with a producer client.
type Kafka struct {
	// Brokers is the broker's host:port, as memd's MEMD_INGEST_KAFKA_BROKERS takes it.
	Brokers string
	// Client produces records and administers topics.
	Client *kgo.Client
}

// StartKafka starts a broker that advertises the host port clients dial and stops it with the test.
func StartKafka(t testing.TB) *Kafka {
	t.Helper()
	RequireDocker(t)
	ports, err := freePorts(1)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	port := strconv.Itoa(ports[0])
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ctr, err := testcontainers.Run(ctx, KafkaImage,
		testcontainers.WithEnv(kafkaEnv(port)),
		testcontainers.WithExposedPorts("9092/tcp"),
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.PortBindings = network.PortMap{network.MustParsePort("9092/tcp"): {{HostPort: port}}}
		}),
		testcontainers.WithWaitStrategy(wait.ForLog("Kafka Server started").WithStartupTimeout(2*time.Minute)),
	)
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })
	if err != nil {
		t.Fatalf("harness: start kafka: %v", err)
	}
	k := &Kafka{Brokers: net.JoinHostPort("localhost", port)}
	if k.Client, err = kgo.NewClient(kgo.SeedBrokers(k.Brokers)); err != nil {
		t.Fatalf("harness: kafka client: %v", err)
	}
	t.Cleanup(k.Client.Close)
	return k
}

// kafkaEnv configures a single-node KRaft broker advertising localhost:port, with internal topics that fit one node
// and no initial rebalance delay.
func kafkaEnv(port string) map[string]string {
	return map[string]string{
		"KAFKA_NODE_ID":                                  "1",
		"KAFKA_PROCESS_ROLES":                            "broker,controller",
		"KAFKA_LISTENERS":                                "PLAINTEXT://:9092,CONTROLLER://:9093",
		"KAFKA_ADVERTISED_LISTENERS":                     "PLAINTEXT://localhost:" + port,
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

// CreateTopic creates a topic with the given partitions (memd never creates its topics, the dead-letter one
// included).
func (k *Kafka) CreateTopic(t testing.TB, name string, partitions int32) {
	t.Helper()
	if _, err := kadm.NewClient(k.Client).CreateTopic(t.Context(), partitions, 1, nil, name); err != nil {
		t.Fatalf("harness: create topic %s: %v", name, err)
	}
}

// Produce writes one record to topic, keyed by key, with headers.
func (k *Kafka) Produce(t testing.TB, topic, key string, value []byte, headers map[string]string) {
	t.Helper()
	rec := &kgo.Record{Topic: topic, Key: []byte(key), Value: value}
	for h, v := range headers {
		rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: h, Value: []byte(v)})
	}
	if err := k.Client.ProduceSync(t.Context(), rec).FirstErr(); err != nil {
		t.Fatalf("harness: produce to %s: %v", topic, err)
	}
}

// ReadAll reads partition 0 of topic from the start until it has n records or the deadline passes, and returns what
// it read.
func (k *Kafka) ReadAll(t testing.TB, topic string, n int, within time.Duration) []*kgo.Record {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(k.Brokers), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
		topic: {0: kgo.NewOffset().AtStart()},
	}))
	if err != nil {
		t.Fatalf("harness: reader: %v", err)
	}
	defer cl.Close()
	ctx, cancel := context.WithTimeout(t.Context(), within)
	defer cancel()
	var out []*kgo.Record
	for len(out) < n && ctx.Err() == nil {
		out = append(out, cl.PollFetches(ctx).Records()...)
	}
	return out
}
