package postgres_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
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

// TestAcceptSeqOrder is the "Seq order" acceptance test: of concurrent transactions on one bank, the
// one that commits later always has the higher seq, and a reader that sees BankSeq S sees every row stamped
// at or below S.
//
// Each transaction takes Seq, inserts one unit stamped with it, holds the transaction open a little and, as
// the last step before committing, records its seq. Seq holds the bank row lock until commit, so the next
// transaction records only after this one has committed: the recorded order is the commit order by
// construction, however loaded the machine is. A concurrent reader repeatedly reads BankSeq S and then counts
// the bank's units with seq <= S, which must be exactly S (seqs 1..S, one unit each).
func TestAcceptSeqOrder(t *testing.T) {
	t.Parallel()
	pool := pgtest.Pool(t)
	s := postgres.New(pool)
	bank := domain.NewBankRef("tenant-seq", "org", "bank-seq")
	pgtest.CreateOrgs(t, s, bank)
	if err := s.InTx(t.Context(), func(tx store.Tx) error {
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
				if err := writeOneSeq(t.Context(), s, bank, w, func(seq int64) {
					mu.Lock()
					committed = append(committed, seq)
					mu.Unlock()
				}); err != nil {
					errs <- err
					return
				}
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
	if reads.Load() == 0 {
		t.Errorf("the reader checked no snapshot while the writers ran")
	}
	t.Logf("%d commits in seq order; reader checked %d snapshots", len(committed), reads.Load())
}

// writeOneSeq runs one transaction: Seq, insert one unit stamped with it, hold 2-4 ms, record the seq (still
// holding the bank row lock), commit.
func writeOneSeq(ctx context.Context, s *postgres.DB, bank domain.BankRef, writer int, record func(int64)) error {
	err := s.InTx(ctx, func(tx store.Tx) error {
		seq, err := tx.Seq(ctx, bank)
		if err != nil {
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
		record(seq)
		return nil
	})
	if err != nil {
		return fmt.Errorf("writer %d: %w", writer, err)
	}
	return nil
}

// checkSeqPrefix loops until stop: it reads BankSeq S through the Reader, then counts the bank's units with
// seq <= S in a separate statement; the count must be exactly S.
func checkSeqPrefix(
	ctx context.Context, s *postgres.DB, pool *pgxpool.Pool, bank domain.BankRef, stop *atomic.Bool,
	reads *atomic.Int64,
) error {
	for !stop.Load() {
		seq, err := s.Read().BankSeq(ctx, bank)
		if err != nil {
			return err
		}
		var n int64
		err = pool.QueryRow(ctx,
			`SELECT count(*) FROM memory_units WHERE tenant_id = $1 AND org_id = $2 AND bank_id = $3 AND seq <= $4`,
			bank.Tenant, bank.Org, bank.Bank, seq).Scan(&n)
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
