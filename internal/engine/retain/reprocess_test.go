package retain

import (
	"encoding/json"
	"errors"
	"memd/internal/blob"
	"memd/internal/domain"
	"memd/internal/engine/docs"
	"memd/internal/jobs"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"testing"
	"time"
)

// testBank is the bank every reprocess test writes to.
var testBank = domain.BankRef{Tenant: "acme", Bank: "notes"}

// reprocessAt is the mention time of the seeded units.
var reprocessAt = time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)

// contentHash is the content hash the seeded document's blob key ends with.
const contentHash = "c0ffee"

// seeded is a store holding document d1 (tags [a], per_tag item scopes, stored overrides with mission
// "stored") as a completed retain leaves it, with two units.
type seeded struct {
	t     *testing.T
	store *postgres.DB
}

// seed writes the bank, d1 and its two units, both with the item's values (context "chat", metadata k=v,
// mentioned at reprocessAt); with curated set, the unit Reprocess scans first is then curated.
func seed(t *testing.T, curated bool) seeded {
	t.Helper()
	s := seeded{t: t, store: pgtest.NewStore(t)}
	s.inTx(func(tx store.Tx) error {
		if _, err := tx.Banks().Ensure(t.Context(), testBank); err != nil {
			return err
		}
		seq, err := tx.Seq(t.Context(), testBank)
		if err != nil {
			return err
		}
		if err := tx.Documents().Upsert(t.Context(), domain.Document{
			ID: "d1", Bank: testBank, Tags: []string{"a"}, BlobKey: blob.Key(testBank, "d1", contentHash),
			Overrides:         &domain.BankConfig{Retain: &domain.RetainConfig{Mission: "stored"}},
			ObservationScopes: &domain.ObservationScopes{Mode: domain.ScopeModePerTag},
		}); err != nil {
			return err
		}
		at := reprocessAt
		item := domain.Unit{
			ID: pgtest.ID[domain.UnitID]("u1"), Bank: testBank, DocumentID: "d1", Type: domain.FactTypeWorld,
			Text: "one", Context: "chat", Metadata: map[string]string{"k": "v"}, Tags: []string{"a"},
			MentionedAt: &at, Seq: seq, State: domain.MemoryStateValid,
		}
		second := item
		second.ID, second.Text = pgtest.ID[domain.UnitID]("u2"), "two"
		return tx.Units().Insert(t.Context(), []domain.Unit{item, second})
	})
	if curated {
		s.curateNewest()
	}
	return s
}

// curateNewest edits the unit d1 lists first (newest first, as Reprocess scans) to context "curated" and
// timeless, as UpdateMemory would.
func (s seeded) curateNewest() {
	s.t.Helper()
	s.inTx(func(tx store.Tx) error {
		seq, err := tx.Seq(s.t.Context(), testBank)
		if err != nil {
			return err
		}
		us, err := tx.ListUnits(s.t.Context(), store.UnitQuery{Bank: testBank, DocumentID: "d1", Limit: 10})
		if err != nil || len(us) != 2 {
			s.t.Fatalf("units of d1: %d, %v", len(us), err)
		}
		edited := reprocessAt.Add(time.Hour)
		u := us[0]
		u.Context, u.Timeless, u.EditedAt, u.Seq = "curated", true, &edited, seq
		return tx.Units().Update(s.t.Context(), u)
	})
}

// inTx runs fn in a transaction, failing the test on error.
func (s seeded) inTx(fn func(tx store.Tx) error) {
	s.t.Helper()
	if err := s.store.InTx(s.t.Context(), fn); err != nil {
		s.t.Fatal(err)
	}
}

// document reads d1.
func (s seeded) document() domain.Document {
	s.t.Helper()
	var d domain.Document
	s.inTx(func(tx store.Tx) error {
		var err error
		d, err = tx.Documents().Get(s.t.Context(), testBank, "d1")
		return err
	})
	return d
}

// reprocessJob claims the next retain_document job and decodes its payload.
func (s seeded) reprocessJob() (store.Job, jobs.RetainPayload) {
	s.t.Helper()
	js, err := s.store.Claim(s.t.Context(), "w1", []domain.OperationKind{domain.OperationKindRetainDocument}, 1)
	if err != nil || len(js) != 1 {
		s.t.Fatalf("Claim retain_document: %v, %d jobs", err, len(js))
	}
	var p jobs.RetainPayload
	if err := json.Unmarshal(js[0].Payload, &p); err != nil {
		s.t.Fatal(err)
	}
	return js[0], p
}

// checkReprocessPayload fails the test unless the reprocess job carries the item's scopes, context,
// metadata, mention time and content hash, is forced, and replays the stored overrides.
func checkReprocessPayload(t *testing.T, j store.Job, p jobs.RetainPayload) {
	t.Helper()
	if p.ObservationScopes == nil || p.ObservationScopes.Mode != domain.ScopeModePerTag || !p.ForceReextract {
		t.Errorf("payload scopes %+v, force %v; want per_tag and forced", p.ObservationScopes, p.ForceReextract)
	}
	if p.Context != "chat" || p.Metadata["k"] != "v" || p.Timestamp == nil || !p.Timestamp.Equal(reprocessAt) {
		t.Errorf("payload context %q, metadata %v, timestamp %v; want the item's", p.Context, p.Metadata, p.Timestamp)
	}
	if p.ContentHash != contentHash || p.DocumentID != "d1" || len(p.Tags) != 1 || p.Tags[0] != "a" {
		t.Errorf("payload %+v, want d1's content hash and tags", p)
	}
	if j.Overrides == nil || j.Overrides.GetRetain().Mission != "stored" {
		t.Errorf("job overrides = %+v, want the stored ones replayed", j.Overrides)
	}
}

// TestReprocessCarriesScopesAndOverrides: Reprocess keeps the document's item scopes (on the row and in the
// payload), replays the stored overrides unless new ones are given, and takes the item's context, metadata
// and mention time from a unit.
func TestReprocessCarriesScopesAndOverrides(t *testing.T) {
	s := seed(t, false)
	rp := NewReprocessor(s.store)
	opID, err := rp.Reprocess(t.Context(), testBank, "d1", nil)
	if err != nil || opID == "" {
		t.Fatalf("Reprocess: %q, %v", opID, err)
	}
	if sc := s.document().ObservationScopes; sc == nil || sc.Mode != domain.ScopeModePerTag {
		t.Fatalf("document scopes after reprocess = %+v, want per_tag (Upsert must carry them)", sc)
	}
	j, p := s.reprocessJob()
	if j.ID != opID {
		t.Fatalf("claimed %s, want the reprocess operation %s", j.ID, opID)
	}
	checkReprocessPayload(t, j, p)
	if err := s.store.Complete(t.Context(), j.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := rp.Reprocess(t.Context(), testBank, "d1", &domain.BankConfig{
		Retain: &domain.RetainConfig{Mission: "new"},
	}); err != nil {
		t.Fatal(err)
	}
	j, _ = s.reprocessJob()
	d := s.document()
	if d.Overrides == nil || d.Overrides.GetRetain().Mission != "new" || j.Overrides.GetRetain().Mission != "new" ||
		d.ObservationScopes == nil {
		t.Errorf("new overrides: document %+v, job overrides %+v", d, j.Overrides)
	}
}

// TestReprocessUnknownDocumentAndDeletingBank: an unknown document is NotFound and a tombstoned bank is
// BankDeleting; neither enqueues anything.
func TestReprocessUnknownDocumentAndDeletingBank(t *testing.T) {
	s := seed(t, false)
	rp := NewReprocessor(s.store)
	if _, err := rp.Reprocess(t.Context(), testBank, "nope", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown document: %v, want NotFound", err)
	}
	s.inTx(func(tx store.Tx) error {
		return tx.Banks().MarkDeleting(t.Context(), testBank, reprocessAt)
	})
	if _, err := rp.Reprocess(t.Context(), testBank, "d1", nil); !errors.Is(err, domain.ErrBankDeleting) {
		t.Errorf("deleting bank: %v, want BankDeleting", err)
	}
	js, err := s.store.Claim(t.Context(), "w1", []domain.OperationKind{domain.OperationKindRetainDocument}, 10)
	if err != nil || len(js) != 0 {
		t.Errorf("reprocess enqueued %d jobs (%v)", len(js), err)
	}
}

// The retain Reprocessor is what docs.DocumentAdmin delegates ReprocessDocument to (decision 1303).
var _ docs.Reprocessor = NewReprocessor(nil)

// TestReprocessIgnoresCuratedUnit: the item's context, timeless flag and mention time come from a unit no
// curator edited, so a curated context or timeless flag on one unit never spreads to the re-extracted ones
// (reprocess resets curation).
func TestReprocessIgnoresCuratedUnit(t *testing.T) {
	s := seed(t, true)
	if _, err := NewReprocessor(s.store).Reprocess(t.Context(), testBank, "d1", nil); err != nil {
		t.Fatal(err)
	}
	_, p := s.reprocessJob()
	if p.Context != "chat" || p.Timeless || p.Timestamp == nil || !p.Timestamp.Equal(reprocessAt) {
		t.Errorf("payload context %q, timeless %v, timestamp %v; want the uncurated item values",
			p.Context, p.Timeless, p.Timestamp)
	}
}
