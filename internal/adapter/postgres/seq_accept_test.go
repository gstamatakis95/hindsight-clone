package postgres_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/domain"
	"memd/internal/port"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seqWriters and seqTxPerWriter size the seq-order stress test.
const (
	seqWriters     = 8
	seqTxPerWriter = 25
)

// TestAcceptSeqOrder is the WP-01 "Seq order" acceptance test: of concurrent transactions on one bank, the
// one that commits later always has the higher seq, and a reader that sees BankSeq S sees every row stamped
// at or below S.
//
// Each transaction takes Seq, inserts one unit stamped with it and holds the transaction open a little
// before committing. Commit order is recorded by the test when InTx returns; the hold (>= 2 ms between Seq
// and commit) is far longer than the gap between a commit and its recording, so the recorded order is the
// commit order. A concurrent reader repeatedly reads BankSeq S and then counts the bank's units with
// seq <= S, which must be exactly S (seqs 1..S, one unit each).
func TestAcceptSeqOrder(t *testing.T) {
	pool := pgtest.Fresh(t)
	s := postgres.New(pool)
	bank := port.BankRef{Tenant: "tenant-seq", Bank: "bank-seq"}
	if err := s.InTx(t.Context(), func(tx port.Tx) error {
		_, err := tx.Banks().Ensure(t.Context(), bank)
		return err
	}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	var (
		mu        sync.Mutex
		committed []int64
		stop      atomic.Bool
		readerErr = make(chan error, 1)
		reads     atomic.Int64
	)
	go func() { readerErr <- checkSeqPrefix(t.Context(), s, pool, bank, &stop, &reads) }()

	var wg sync.WaitGroup
	errs := make(chan error, seqWriters)
	for w := range seqWriters {
		wg.Go(func() {
			for range seqTxPerWriter {
				seq, err := writeOneSeq(t.Context(), s, bank, w)
				if err != nil {
					errs <- err
					return
				}
				mu.Lock()
				committed = append(committed, seq)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	stop.Store(true)
	close(errs)
	for err := range errs {
		t.Fatalf("writer: %v", err)
	}
	if err := <-readerErr; err != nil {
		t.Fatalf("reader: %v", err)
	}
	if len(committed) != seqWriters*seqTxPerWriter {
		t.Fatalf("committed %d transactions, want %d", len(committed), seqWriters*seqTxPerWriter)
	}
	for i := 1; i < len(committed); i++ {
		if committed[i] <= committed[i-1] {
			t.Fatalf("commit #%d got seq %d after commit #%d with seq %d: later commit, lower seq",
				i, committed[i], i-1, committed[i-1])
		}
	}
	t.Logf("%d commits in seq order; reader checked %d snapshots", len(committed), reads.Load())
}

// writeOneSeq runs one transaction: Seq, insert one unit stamped with it, hold 2-4 ms, commit.
func writeOneSeq(ctx context.Context, s *postgres.Store, bank port.BankRef, writer int) (int64, error) {
	var seq int64
	err := s.InTx(ctx, func(tx port.Tx) error {
		var err error
		if seq, err = tx.Seq(ctx, bank); err != nil {
			return err
		}
		u := domain.Unit{
			ID: domain.UnitID(uuid.NewString()), Bank: bank, Type: domain.FactTypeWorld,
			Text: "writer", Seq: seq, Metadata: map[string]string{"writer": strconv.Itoa(writer)},
		}
		if err := tx.Units().Insert(ctx, []domain.Unit{u}); err != nil {
			return err
		}
		time.Sleep(time.Duration(2000+rand.IntN(2000)) * time.Microsecond) //nolint:gosec // test jitter
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("writer %d: %w", writer, err)
	}
	return seq, nil
}

// checkSeqPrefix loops until stop: it reads BankSeq S through the Reader, then counts the bank's units with
// seq <= S in a separate statement; the count must be exactly S.
func checkSeqPrefix(
	ctx context.Context, s *postgres.Store, pool *pgxpool.Pool, bank port.BankRef, stop *atomic.Bool,
	reads *atomic.Int64,
) error {
	for !stop.Load() {
		seq, err := s.Read().BankSeq(ctx, bank)
		if err != nil {
			return err
		}
		var n int64
		err = pool.QueryRow(ctx,
			`SELECT count(*) FROM memory_units WHERE tenant_id = $1 AND bank_id = $2 AND seq <= $3`,
			bank.Tenant, bank.Bank, seq).Scan(&n)
		if err != nil {
			return fmt.Errorf("count units: %w", err)
		}
		if n != seq {
			return fmt.Errorf("reader saw BankSeq %d but %d units at or below it", seq, n)
		}
		reads.Add(1)
	}
	return nil
}
