package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"memd/internal/grpcapi"
	"memd/internal/ingest"
	"memd/internal/ingest/kafka"
	"memd/internal/ingestapi"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// The queue consumer (memd ingest, and memd all when MEMD_INGEST_PROVIDER is set): openSource builds the
// ingest.Source the provider names, newConsumer the ingestapi.Consumer over it and the memory and org services
// wireServices built, and runIngest runs it with its optional health server.

// runIngest opens the source and runs the consumer until ctx is done; the consumer then retains and settles what
// it holds and closes the source. With MEMD_INGEST_HEALTH_ADDR set it also serves grpc.health.v1 there, ready while
// Postgres answers.
func runIngest(ctx context.Context, d *deps) error {
	src, err := openSource(d.cfg)
	if err != nil {
		return err
	}
	var lis net.Listener
	if d.cfg.Ingest.HealthAddr != "" {
		if lis, err = (&net.ListenConfig{}).Listen(ctx, "tcp", d.cfg.Ingest.HealthAddr); err != nil {
			_ = src.Close()
			return fmt.Errorf("memd: listen %s: %w", d.cfg.Ingest.HealthAddr, err)
		}
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return newConsumer(d, src).Run(gctx) })
	if lis != nil {
		probes := []grpcapi.ReadinessProbe{grpcapi.PostgresProbe{Ping: d.ping}}
		g.Go(func() error { return grpcapi.ServeHealth(gctx, lis, probes, apiShutdownGrace) })
		d.log.Info("memd ingest health serving", "addr", lis.Addr().String())
	}
	d.log.Info("memd ingest running", "provider", d.cfg.Ingest.Provider, "tenant", d.cfg.Ingest.Tenant,
		"org", d.cfg.Ingest.Org)
	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("memd: ingest: %w", err)
	}
	return nil
}

// newConsumer builds the queue consumer over src from the server config file's ingest section, the pinning
// settings and d's services, clock, logger and observability hooks.
func newConsumer(d *deps, src ingest.Source) *ingestapi.Consumer {
	f := d.cfg.File.Ingest
	c := f.Consumer
	deps := d.ingest
	deps.Source, deps.Clock, deps.Logger = src, d.clock, d.log
	deps.Hooks = ingestapi.Hooks{Batch: d.tel.IngestBatchHook(), Count: d.tel.IngestCountHook()}
	return ingestapi.New(ingestapi.Config{
		BatchSize: c.BatchSize, FlushInterval: time.Duration(c.FlushInterval), Concurrency: c.Concurrency,
		MaxAttempts: c.MaxAttempts, BackoffInitial: time.Duration(c.BackoffInitial),
		BackoffMax: time.Duration(c.BackoffMax), ShutdownGrace: time.Duration(c.ShutdownGrace),
		MaxRequestBytes: f.MaxRequestBytes, Tenant: d.cfg.Ingest.Tenant, Org: d.cfg.Ingest.Org,
	}, deps)
}

// openSource builds the ingest.Source MEMD_INGEST_PROVIDER selects (Config.Validate has already rejected an
// unknown or unset one).
func openSource(cfg Config) (ingest.Source, error) {
	switch cfg.Ingest.Provider {
	case ingestProviderKafka:
		kc, err := kafkaConfig(cfg)
		if err != nil {
			return nil, err
		}
		src, err := kafka.New(kc)
		if err != nil {
			return nil, fmt.Errorf("memd: kafka: %w", err)
		}
		return src, nil
	default:
		return nil, fmt.Errorf("memd: %s: no queue source for provider %q", envIngestProvider, cfg.Ingest.Provider)
	}
}

// kafkaConfig is the Kafka source's config: the connection from the environment, the poll settings from the
// server config file's ingest.consumer section.
func kafkaConfig(cfg Config) (kafka.Config, error) {
	k := cfg.Ingest.Kafka
	var brokers []string
	for _, b := range strings.Split(k.Brokers, ",") {
		if b = strings.TrimSpace(b); b != "" {
			brokers = append(brokers, b)
		}
	}
	tc, err := kafkaTLS(k)
	if err != nil {
		return kafka.Config{}, err
	}
	return kafka.Config{
		Brokers: brokers, Topic: k.Topic, Group: k.Group, DeadLetterTopic: k.DLQTopic, ClientID: k.ClientID, TLS: tc,
		SASL:        kafka.SASL{Mechanism: k.SASLMechanism, Username: k.SASLUsername, Password: k.SASLPassword},
		PollWait:    time.Duration(cfg.File.Ingest.Consumer.PollWait),
		MaxBuffered: cfg.File.Ingest.Consumer.MaxBuffered,
	}, nil
}

// kafkaTLS is the TLS configuration of the Kafka connection: nil (plaintext) unless MEMD_INGEST_KAFKA_TLS or a TLS
// file is set; the CA file replaces the system pool, and the certificate and key enable mutual TLS.
func kafkaTLS(k KafkaEnv) (*tls.Config, error) {
	if !k.TLS && k.TLSCAFile == "" && k.TLSCertFile == "" {
		return nil, nil
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if k.TLSCAFile != "" {
		pem, err := os.ReadFile(k.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("memd: %s: %w", envKafkaTLSCA, err)
		}
		tc.RootCAs = x509.NewCertPool()
		if !tc.RootCAs.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("memd: %s: no PEM certificate in %s", envKafkaTLSCA, k.TLSCAFile)
		}
	}
	if k.TLSCertFile != "" {
		cert, err := tls.LoadX509KeyPair(k.TLSCertFile, k.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("memd: %s / %s: %w", envKafkaTLSCert, envKafkaTLSKey, err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	return tc, nil
}
