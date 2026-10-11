package postgres_test

import (
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"sync"
	"testing"
	"time"
)

// blockedFor is how long a second writer must stay blocked on Tx.Seq while the first holds the bank row.
const blockedFor = 150 * time.Millisecond

// TestSeq runs the contract tests of Tx.Seq and Reader.BankSeq: the per-bank change sequence whose
// row lock makes seq order commit order (SPEC "Behavioural rules", change sequence). newStore is called
// once per subtest and must return a store over an empty database. The store must allow two concurrent
// transactions (a pool of at least two connections).
func TestSeq(t *testing.T) {
	t.Parallel()
	runStoreCases(t, []storeCase{
		{"FirstSeqIsOne", testSeqFirst},
		{"SameValueWithinTx", testSeqSameTx},
		{"StrictlyIncreasingAcrossTx", testSeqIncreasing},
		{"UnknownBank", testSeqUnknown},
		{"TombstonedBank", testSeqDeleting},
		{"BanksIndependent", testSeqIndependent},
		{"RollbackNotVisible", testSeqRollback},
		{"ConcurrentWritersSerialize", testSeqConcurrent},
	})
}

// takeSeq runs one transaction that calls Seq on b and returns the value.
func takeSeq(t *testing.T, s store.DB, b domain.BankRef) int64 {
	t.Helper()
	var seq int64
	inTx(t, s, func(tx store.Tx) error {
		var err error
		seq, err = tx.Seq(t.Context(), b)
		return err
	})
	return seq
}

// seqErr runs one transaction that calls Seq on b and returns its error.
func seqErr(t *testing.T, s store.DB, b domain.BankRef) error {
	t.Helper()
	return txErr(t, s, func(tx store.Tx) error {
		_, err := tx.Seq(t.Context(), b)
		return err
	})
}

// testSeqFirst: a new bank has BankSeq 0; its first Seq is 1 and BankSeq and ChangeSeq follow.
func testSeqFirst(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	if got := bankSeq(t, s, bankA); got != 0 {
		t.Errorf("BankSeq(new bank) = %d, want 0", got)
	}
	if got := takeSeq(t, s, bankA); got != 1 {
		t.Errorf("first Seq = %d, want 1", got)
	}
	if got := bankSeq(t, s, bankA); got != 1 {
		t.Errorf("BankSeq after first Seq = %d, want 1", got)
	}
	if got := mustBank(t, s, bankA).ChangeSeq; got != 1 {
		t.Errorf("Bank.ChangeSeq after first Seq = %d, want 1", got)
	}
}

// testSeqSameTx: Seq twice in one tx returns the same value and bumps the bank once.
func testSeqSameTx(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	takeSeq(t, s, bankA)
	before := bankSeq(t, s, bankA)
	var first, second int64
	inTx(t, s, func(tx store.Tx) error {
		var err error
		if first, err = tx.Seq(t.Context(), bankA); err != nil {
			return err
		}
		second, err = tx.Seq(t.Context(), bankA)
		return err
	})
	if first != second {
		t.Errorf("Seq twice in one tx = %d then %d, want the same value", first, second)
	}
	if first != before+1 {
		t.Errorf("Seq = %d, want BankSeq+1 = %d", first, before+1)
	}
	if got := bankSeq(t, s, bankA); got != before+1 {
		t.Errorf("BankSeq after a tx calling Seq twice = %d, want %d (one bump)", got, before+1)
	}
}

// testSeqIncreasing: sequential transactions get strictly increasing seqs; BankSeq is the last one.
func testSeqIncreasing(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	var prev int64
	for i := range 5 {
		seq := takeSeq(t, s, bankA)
		if seq <= prev {
			t.Errorf("tx %d: Seq = %d, not above previous %d", i, seq, prev)
		}
		prev = seq
	}
	if got := bankSeq(t, s, bankA); got != prev {
		t.Errorf("BankSeq = %d, want the last Seq %d", got, prev)
	}
}

// testSeqUnknown: Seq on an unknown bank is ErrNotFound (Seq does not create banks).
func testSeqUnknown(t *testing.T, s store.DB) {
	wantErrIs(t, "Seq(unknown bank)", seqErr(t, s, bankA), domain.ErrNotFound)
	_, err := s.Read().BankSeq(t.Context(), bankA)
	wantErrIs(t, "BankSeq(unknown bank) after failed Seq", err, domain.ErrNotFound)
}

// testSeqDeleting: Seq on a tombstoned bank is ErrBankDeleting and does not bump it.
func testSeqDeleting(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	takeSeq(t, s, bankA)
	inTx(t, s, func(tx store.Tx) error { return tx.Banks().MarkDeleting(t.Context(), bankA, day(1)) })
	err := seqErr(t, s, bankA)
	wantErrIs(t, "Seq(tombstoned bank)", err, domain.ErrBankDeleting)
	wantErrIs(t, "Seq(tombstoned bank) class", err, domain.ErrFailedPrecondition)
	if got := mustBank(t, s, bankA).ChangeSeq; got != 1 {
		t.Errorf("ChangeSeq after rejected Seq = %d, want 1", got)
	}
}

// testSeqIndependent: each bank has its own sequence.
func testSeqIndependent(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA, bankB, bankOtherTenant)
	for range 3 {
		takeSeq(t, s, bankA)
	}
	if got := takeSeq(t, s, bankB); got != 1 {
		t.Errorf("first Seq of bank B = %d, want 1", got)
	}
	if got := takeSeq(t, s, bankOtherTenant); got != 1 {
		t.Errorf("first Seq of the same bank id under another tenant = %d, want 1", got)
	}
	if a, b := bankSeq(t, s, bankA), bankSeq(t, s, bankB); a != 3 || b != 1 {
		t.Errorf("BankSeq A, B = %d, %d; want 3, 1", a, b)
	}
}

// testSeqRollback: a rolled-back Seq bump is not visible; the next tx gets the next value without a gap.
func testSeqRollback(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	takeSeq(t, s, bankA)
	before := bankSeq(t, s, bankA)
	u := fact(bankA, "rolled back")
	err := s.InTx(t.Context(), func(tx store.Tx) error {
		if _, err := stampAndInsert(t, tx, []domain.Unit{u}); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("InTx = %v, want the callback's error", err)
	}
	if got := bankSeq(t, s, bankA); got != before {
		t.Errorf("BankSeq after rollback = %d, want %d", got, before)
	}
	if got := getUnits(t, s, bankA, u.ID); len(got) != 0 {
		t.Errorf("rolled-back unit visible: %v", unitIDs(got))
	}
	if got := takeSeq(t, s, bankA); got != before+1 {
		t.Errorf("Seq after rollback = %d, want %d", got, before+1)
	}
}

// seqResult is what a concurrent writer reports.
type seqResult struct {
	seq int64
	err error
}

// holdSeq starts a transaction that takes Seq on bankA, inserts u stamped with it, reports the seq on
// held, then waits for release before committing. Its InTx error goes to done.
func holdSeq(t *testing.T, s store.DB, u domain.Unit, held chan<- seqResult, release <-chan struct{},
	done chan<- error,
) {
	go func() {
		done <- s.InTx(t.Context(), func(tx store.Tx) error {
			seq, err := stampAndInsert(t, tx, []domain.Unit{u})
			held <- seqResult{seq, err}
			if err != nil {
				return err
			}
			<-release
			return nil
		})
	}()
}

// testSeqConcurrent: while tx A holds bank A's seq, tx B's Seq on the same bank blocks; B gets a higher seq
// once A commits, and after both commit every unit stamped <= BankSeq is visible with its seq.
func testSeqConcurrent(t *testing.T, s store.DB) {
	ensureBanks(t, s, bankA)
	uA, uB := fact(bankA, "written by A"), fact(bankA, "written by B")
	heldA, heldB := make(chan seqResult, 1), make(chan seqResult, 1)
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	releaseA, releaseB := make(chan struct{}), make(chan struct{})
	var once sync.Once
	freeA := func() { once.Do(func() { close(releaseA) }) }
	defer freeA()
	close(releaseB) // B commits as soon as it has its seq

	holdSeq(t, s, uA, heldA, releaseA, doneA)
	a := waitFor(t, "tx A's Seq", heldA)
	if a.err != nil {
		t.Fatalf("tx A Seq/Insert: %v", a.err)
	}
	holdSeq(t, s, uB, heldB, releaseB, doneB)
	select {
	case b := <-heldB:
		t.Errorf("tx B's Seq returned %d (err %v) while tx A held the bank row; want it blocked", b.seq, b.err)
		heldB <- b
	case <-time.After(blockedFor):
	}
	freeA()
	if err := waitFor(t, "tx A's commit", doneA); err != nil {
		t.Fatalf("tx A commit: %v", err)
	}
	b := waitFor(t, "tx B's Seq", heldB)
	if err := waitFor(t, "tx B's commit", doneB); err != nil || b.err != nil {
		t.Fatalf("tx B: seq err %v, commit err %v", b.err, err)
	}
	if b.seq <= a.seq {
		t.Errorf("tx B's seq %d not above tx A's %d", b.seq, a.seq)
	}
	assertSeqVisible(t, s, map[domain.UnitID]int64{uA.ID: a.seq, uB.ID: b.seq})
}

// assertSeqVisible checks that BankSeq covers every given unit and each is visible with its seq.
func assertSeqVisible(t *testing.T, s store.DB, want map[domain.UnitID]int64) {
	t.Helper()
	top := bankSeq(t, s, bankA)
	ids := make([]domain.UnitID, 0, len(want))
	for id, seq := range want {
		ids = append(ids, id)
		if seq > top {
			t.Errorf("unit %s seq %d above BankSeq %d after commit", id, seq, top)
		}
	}
	got := getUnits(t, s, bankA, ids...)
	if len(got) != len(want) {
		t.Errorf("GetUnits after both commits = %v, want all of %v", unitIDs(got), ids)
	}
	for _, u := range got {
		if u.Seq != want[u.ID] {
			t.Errorf("unit %s seq = %d, want %d", u.ID, u.Seq, want[u.ID])
		}
	}
}
