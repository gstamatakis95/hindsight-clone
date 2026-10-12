package postgres_test

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres/pgtest"
	"reflect"
	"slices"
	"testing"
	"time"
)

// Shared fixtures. Every case gets its own empty database, so fixed names never collide.
var (
	// bankA is the main bank of most subtests.
	bankA = domain.NewBankRef("tenant-1", "org", "bank-a")
	// bankB is a second bank of the same tenant, for isolation checks.
	bankB = domain.NewBankRef("tenant-1", "org", "bank-b")
	// bankOtherTenant has bankA's bank id under another tenant.
	bankOtherTenant = domain.NewBankRef("tenant-2", "org", "bank-a")
	// bankOtherOrg has bankA's bank id under another org of the same tenant.
	bankOtherOrg = domain.NewBankRef("tenant-1", "org-2", "bank-a")
	// epoch anchors fixed timestamps: whole seconds in UTC, so microsecond storage round-trips exactly.
	epoch = time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	// errRollback is returned from InTx callbacks that must roll back.
	errRollback = errors.New("forced rollback")
)

const (
	// waitTimeout bounds every wait on a goroutine, so a deadlocked adapter fails instead of hanging.
	waitTimeout = 15 * time.Second
	// scoreTolerance is the allowed error of a cosine-similarity score.
	scoreTolerance = 1e-3
)

// newUUID returns a random RFC 4122 version-4 UUID in canonical lowercase form.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go 1.24+); it crashes the program instead
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// newID returns a fresh UUID as any string-based id type (UnitID, ChunkID, EntityID, OperationID).
func newID[T ~string]() T { return T(newUUID()) }

// angleVec returns the unit vector (cos d, sin d, 0, ..., 0) of domain.EmbeddingDim floats for an angle of
// d degrees. Its cosine similarity to angleVec(0) is cos(d), so similarity ordering is predictable.
func angleVec(deg float64) []float32 {
	v := make([]float32, domain.EmbeddingDim)
	r := deg * math.Pi / 180
	v[0] = float32(math.Cos(r))
	v[1] = float32(math.Sin(r))
	return v
}

// cosDeg is cos of an angle in degrees: the expected score of angleVec(d) against angleVec(0).
func cosDeg(deg float64) float64 { return math.Cos(deg * math.Pi / 180) }

// day returns epoch plus n days.
func day(n int) time.Time { return epoch.AddDate(0, 0, n) }

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// nowMicro is the wall clock rounded down to microseconds (Postgres timestamp precision), in UTC.
func nowMicro() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// ---------- running subtests ----------

// storeCase is one named store subtest.
type storeCase struct {
	name string
	fn   func(t *testing.T, s store.DB)
}

// runStoreCases runs each case as a parallel subtest over a Store on its own empty database.
func runStoreCases(t *testing.T, cases []storeCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.fn(t, pgtest.NewStore(t))
		})
	}
}

// runQueueCases runs each case as a parallel subtest over a Store and its Claimer on their own empty
// database (Claim and ReclaimStale are global across banks, so cases never share one).
func runQueueCases(t *testing.T, cases []queueCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := pgtest.NewStore(t)
			c.fn(t, queueEnv{s: s, c: s})
		})
	}
}

// ---------- transaction helpers ----------

// inTx runs fn in one transaction and fails the test if it returns an error. fn must not call t.Fatal
// (a Goexit inside InTx could leak an open transaction); it returns an error instead.
func inTx(t *testing.T, s store.DB, fn func(tx store.Tx) error) {
	t.Helper()
	if err := s.InTx(t.Context(), fn); err != nil {
		t.Fatalf("InTx: %v", err)
	}
}

// txErr runs fn in its own transaction and returns InTx's error, for calls expected to fail. Each expected
// failure gets its own transaction: Postgres aborts a transaction after a constraint violation.
func txErr(t *testing.T, s store.DB, fn func(tx store.Tx) error) error {
	t.Helper()
	return s.InTx(t.Context(), fn)
}

// ensureBanks creates the given banks (and their orgs) with Ensure.
func ensureBanks(t *testing.T, s store.DB, banks ...domain.BankRef) {
	t.Helper()
	pgtest.EnsureBanks(t, s, banks...)
}

// fact builds a world fact of bank b with a fresh id. Callers set embeddings, tags and times as needed.
func fact(b domain.BankRef, text string) domain.Unit {
	return domain.Unit{ID: newID[domain.UnitID](), Bank: b, Type: domain.FactTypeWorld, Text: text}
}

// observation builds an observation of bank b in the scope given by tags, at version 1.
func observation(b domain.BankRef, text string, tags ...string) domain.Unit {
	return domain.Unit{
		ID: newID[domain.UnitID](), Bank: b, Type: domain.FactTypeObservation, Text: text,
		Tags: domain.NormalizeTags(tags), Version: 1, ProofCount: 1, Embedding: angleVec(45),
	}
}

// insertUnits inserts us (all of one bank) in one transaction stamped with that transaction's Seq, which it
// returns. The units' Seq fields are overwritten in the caller's slice.
func insertUnits(t *testing.T, s store.DB, us ...domain.Unit) int64 {
	t.Helper()
	var seq int64
	inTx(t, s, func(tx store.Tx) error {
		var err error
		seq, err = stampAndInsert(t, tx, us)
		return err
	})
	return seq
}

// stampAndInsert takes the tx's Seq for the bank of us, stamps every unit with it and inserts them.
func stampAndInsert(t *testing.T, tx store.Tx, us []domain.Unit) (int64, error) {
	if len(us) == 0 {
		return 0, nil
	}
	seq, err := tx.Seq(t.Context(), us[0].Bank)
	if err != nil {
		return 0, err
	}
	for i := range us {
		us[i].Seq = seq
	}
	return seq, tx.Units().Insert(t.Context(), us)
}

// upsertDocs upserts documents with the given ids (no tags, no overrides) into bank b.
func upsertDocs(t *testing.T, s store.DB, b domain.BankRef, ids ...domain.DocumentID) {
	t.Helper()
	inTx(t, s, func(tx store.Tx) error {
		for _, id := range ids {
			d := domain.Document{ID: id, Bank: b, Tags: []string{}, BlobKey: blob.Key(b, id, "h0")}
			if err := tx.Documents().Upsert(t.Context(), d); err != nil {
				return err
			}
		}
		return nil
	})
}

// getUnits reads units through a fresh Read() view.
func getUnits(t *testing.T, s store.DB, b domain.BankRef, ids ...domain.UnitID) []domain.Unit {
	t.Helper()
	us, err := s.Read().GetUnits(t.Context(), b, ids)
	if err != nil {
		t.Fatalf("GetUnits: %v", err)
	}
	return us
}

// bankSeq reads BankSeq through a fresh Read() view.
func bankSeq(t *testing.T, s store.DB, b domain.BankRef) int64 {
	t.Helper()
	seq, err := s.Read().BankSeq(t.Context(), b)
	if err != nil {
		t.Fatalf("BankSeq(%v): %v", b, err)
	}
	return seq
}

// getOp reads one operation through a new transaction's Queue and returns Get's error unchanged.
func getOp(t *testing.T, s store.DB, b domain.BankRef, id domain.OperationID) (domain.Operation, error) {
	t.Helper()
	var op domain.Operation
	err := s.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		op, err = tx.Queue().Get(t.Context(), b, id)
		return err
	})
	return op, err
}

// listOps reads the first page (up to 100) of bank b's operations matching f, failing the test on error.
func listOps(t *testing.T, s store.DB, b domain.BankRef, f store.OperationFilter) []domain.Operation {
	t.Helper()
	if f.PageSize == 0 {
		f.PageSize = 100
	}
	var ops []domain.Operation
	inTx(t, s, func(tx store.Tx) error {
		var err error
		ops, _, err = tx.Queue().List(t.Context(), b, f)
		return err
	})
	return ops
}

// opIDs returns the ids of ops in order.
func opIDs(ops []domain.Operation) []domain.OperationID {
	out := make([]domain.OperationID, len(ops))
	for i, op := range ops {
		out[i] = op.ID
	}
	return out
}

// ---------- comparison helpers ----------

// unitIDs returns the ids of us in order.
func unitIDs(us []domain.Unit) []domain.UnitID {
	out := make([]domain.UnitID, len(us))
	for i, u := range us {
		out[i] = u.ID
	}
	return out
}

// hitIDs returns the ids of hits in order.
func hitIDs(hs []store.Hit) []domain.UnitID {
	out := make([]domain.UnitID, len(hs))
	for i, h := range hs {
		out[i] = h.ID
	}
	return out
}

// sorted returns a sorted copy of xs.
func sorted[T cmp.Ordered](xs []T) []T {
	out := slices.Clone(xs)
	slices.Sort(out)
	return out
}

// sameSet reports whether a and b hold the same elements with the same multiplicity, in any order.
func sameSet[T cmp.Ordered](a, b []T) bool {
	return slices.Equal(sorted(a), sorted(b))
}

// wantSet fails the test unless got and want hold the same elements, in any order.
func wantSet[T cmp.Ordered](t *testing.T, what string, got, want []T) {
	t.Helper()
	if !sameSet(got, want) {
		t.Errorf("%s: got %v, want (any order) %v", what, sorted(got), sorted(want))
	}
}

// wantSeq fails the test unless got equals want in order.
func wantSeq[T comparable](t *testing.T, what string, got, want []T) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

// wantErrIs fails the test unless errors.Is(err, target).
func wantErrIs(t *testing.T, what string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Errorf("%s: got error %v, want errors.Is(_, %v)", what, err, target)
	}
}

// wantNoHit fails the test if id is among hits.
func wantNoHit(t *testing.T, what string, hs []store.Hit, id domain.UnitID) {
	t.Helper()
	if slices.Contains(hitIDs(hs), id) {
		t.Errorf("%s: unexpected hit %s in %v", what, id, hitIDs(hs))
	}
}

// wantScoresDescending fails the test if any hit scores higher than the one before it.
func wantScoresDescending(t *testing.T, what string, hs []store.Hit) {
	t.Helper()
	for i := 1; i < len(hs); i++ {
		if hs[i].Score > hs[i-1].Score {
			t.Errorf("%s: hit %d score %v > hit %d score %v", what, i, hs[i].Score, i-1, hs[i-1].Score)
		}
	}
}

// sameTimePtr reports whether two optional times are both nil or the same instant.
func sameTimePtr(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// jsonEqual reports whether a and b marshal to semantically equal JSON (key order and spacing ignored).
// It compares config structs, whose omitempty tags make nil and absent fields equivalent.
func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	return reflect.DeepEqual(jsonValue(t, a), jsonValue(t, b))
}

// jsonValue marshals v and decodes it into a generic value; a json.RawMessage is decoded as is.
func jsonValue(t *testing.T, v any) any {
	t.Helper()
	raw, ok := v.(json.RawMessage)
	if !ok {
		var err error
		if raw, err = json.Marshal(v); err != nil {
			t.Fatalf("marshal %T: %v", v, err)
		}
	}
	if len(raw) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return out
}

// wantJSON fails the test unless got and want are semantically equal JSON values.
func wantJSON(t *testing.T, what string, got, want any) {
	t.Helper()
	if !jsonEqual(t, got, want) {
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(want)
		t.Errorf("%s: got %s, want %s", what, gb, wb)
	}
}

// waitFor receives one value from ch or fails the test after waitTimeout.
func waitFor[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(waitTimeout):
		t.Fatalf("timed out after %v waiting for %s", waitTimeout, what)
		var zero T
		return zero
	}
}

// embeddedFact is fact with an embedding. UnitRepo.Unconsolidated and Store.UnconsolidatedBanks skip units
// without one, so a test expecting a fact among the consolidation candidates builds it here.
func embeddedFact(b domain.BankRef, text string) domain.Unit {
	u := fact(b, text)
	u.Embedding = angleVec(0)
	return u
}
