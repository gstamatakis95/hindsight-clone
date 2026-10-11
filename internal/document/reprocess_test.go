package document_test

import (
	"context"
	"encoding/json"
	"errors"
	"memd/internal/blob"
	"memd/internal/document"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/jobs"
	"memd/internal/store"
	"testing"
	"time"
)

// reprocessAt is the mention time of the retained item behind document r1.
var reprocessAt = time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)

// seedRetained adds document r1 as retain leaves it (stored text, item scopes, stored overrides) with two
// units of the item: u1 as extracted and the newer u2, which a curator edited.
func (f fixture) seedRetained(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	tx := func(fn func(tx store.Tx) error) {
		t.Helper()
		if err := f.store.InTx(ctx, fn); err != nil {
			t.Fatal(err)
		}
	}
	tx(func(tx store.Tx) error {
		return tx.Documents().Upsert(ctx, domain.Document{
			ID: "r1", Bank: testBank, Tags: []string{"a"}, BlobKey: blob.Key(testBank, "r1", "hash1"),
			ObservationScopeModes: &domain.ObservationScopeModes{Mode: domain.ScopeModePerTag},
			Overrides:             &domain.BankConfig{Retain: &domain.RetainConfig{Mission: "stored"}},
		})
	})
	at := reprocessAt
	for _, id := range []string{"u1", "u2"} { // one transaction each: u2 is the newer unit
		tx(func(tx store.Tx) error {
			seq, err := tx.Seq(ctx, testBank)
			if err != nil {
				return err
			}
			return tx.Units().Insert(ctx, []domain.Unit{{
				ID: uid(id), Bank: testBank, DocumentID: "r1", Type: domain.FactTypeWorld, Text: "fact " + id,
				Tags: []string{"a"}, Context: "chat", Metadata: map[string]string{"k": "v"}, MentionedAt: &at,
				Seq: seq,
			}})
		})
	}
	tx(func(tx store.Tx) error {
		seq, err := tx.Seq(ctx, testBank)
		if err != nil {
			return err
		}
		us, err := tx.GetUnits(ctx, testBank, []domain.UnitID{uid("u2")})
		if err != nil || len(us) != 1 {
			return errors.Join(err, errors.New("u2 not found"))
		}
		edited := f.clock.Now()
		u := us[0]
		u.Context, u.Timeless, u.EditedAt, u.Seq = "curated", true, &edited, seq
		return tx.Units().Update(ctx, u)
	})
}

// reprocessJob claims r1's retain_document job, completes it and decodes its payload.
func (f fixture) reprocessJob(t *testing.T) (store.Job, jobs.RetainPayload) {
	t.Helper()
	ctx := context.Background()
	js, err := f.store.Claim(ctx, "w1", []domain.OperationKind{domain.OperationKindRetainDocument}, 1)
	if err != nil || len(js) != 1 {
		t.Fatalf("claim retain_document: %v, %d jobs", err, len(js))
	}
	if err := f.store.Complete(ctx, js[0].ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	var p jobs.RetainPayload
	if err := json.Unmarshal(js[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	return js[0], p
}

// TestReprocessDocument: the forced retain_document job carries the stored text's hash and key, the
// document's tags and item scopes (also kept on the row), the item's context, metadata and mention time from
// the newest unit no curator edited (a curated context or timeless flag never spreads), and replays the
// stored overrides unless new ones are given, which then replace them on the document.
func TestReprocessDocument(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.seedRetained(t)
	s := document.New(document.Deps{DB: f.store, Blob: fake.NewBlob(), Clock: f.clock})
	op, err := s.ReprocessDocument(ctx, testBank, "r1", nil)
	if err != nil || op.Kind != domain.OperationKindRetainDocument || op.Status != domain.OperationStatusPending {
		t.Fatalf("ReprocessDocument = %+v, %v", op, err)
	}
	j, p := f.reprocessJob(t)
	if j.ID != op.ID {
		t.Errorf("claimed %s, want the reprocess operation %s", j.ID, op.ID)
	}
	checkReprocessPayload(t, p)
	if j.Overrides == nil || j.Overrides.GetRetain().Mission != "stored" {
		t.Errorf("job overrides = %+v, want the stored ones replayed", j.Overrides)
	}
	checkNewOverrides(t, f, s)
}

// checkReprocessPayload fails the test unless p is forced over r1's stored text with its tags and per_tag
// scopes, and carries the uncurated unit's context, metadata and mention time.
func checkReprocessPayload(t *testing.T, p jobs.RetainPayload) {
	t.Helper()
	if !p.ForceReextract || p.ContentHash != "hash1" || p.BlobKey != blob.Key(testBank, "r1", "hash1") ||
		p.ObservationScopeModes == nil || p.ObservationScopeModes.Mode != domain.ScopeModePerTag || len(p.Tags) != 1 {
		t.Errorf("payload %+v, want forced, the stored text and per_tag scopes", p)
	}
	if p.Context != "chat" || p.Timeless || p.Metadata["k"] != "v" || p.Timestamp == nil ||
		!p.Timestamp.Equal(reprocessAt) {
		t.Errorf("payload context %q, timeless %v, metadata %v, timestamp %v; want the uncurated item's",
			p.Context, p.Timeless, p.Metadata, p.Timestamp)
	}
}

// checkNewOverrides reprocesses r1 with new overrides: they replace the stored ones on the document and the
// job, and the item scopes stay.
func checkNewOverrides(t *testing.T, f fixture, s *document.Service) {
	t.Helper()
	ctx := context.Background()
	newOverrides := &domain.BankConfig{Retain: &domain.RetainConfig{Mission: "new"}}
	if _, err := s.ReprocessDocument(ctx, testBank, "r1", newOverrides); err != nil {
		t.Fatal(err)
	}
	j, _ := f.reprocessJob(t)
	var d domain.Document
	if err := f.store.InTx(ctx, func(tx store.Tx) error {
		var err error
		d, err = tx.Documents().Get(ctx, testBank, "r1")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if d.Overrides.GetRetain().Mission != "new" || j.Overrides.GetRetain().Mission != "new" ||
		d.ObservationScopeModes == nil || d.ObservationScopeModes.Mode != domain.ScopeModePerTag {
		t.Errorf("new overrides: document %+v, job overrides %+v", d, j.Overrides)
	}
}

// TestReprocessDocumentRefusals: an unknown document is NotFound, one without stored text FailedPrecondition
// and a tombstoned bank BankDeleting; none enqueues anything.
func TestReprocessDocumentRefusals(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.seedRetained(t)
	s := document.New(document.Deps{DB: f.store, Blob: fake.NewBlob(), Clock: f.clock})
	if _, err := s.ReprocessDocument(ctx, testBank, "nope", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown document: %v, want NotFound", err)
	}
	if _, err := s.ReprocessDocument(ctx, testBank, "d1", nil); !errors.Is(err, domain.ErrFailedPrecondition) {
		t.Errorf("document without stored text: %v, want FailedPrecondition", err)
	}
	if err := f.store.InTx(ctx, func(tx store.Tx) error {
		return tx.Banks().MarkDeleting(ctx, testBank, f.clock.Now())
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReprocessDocument(ctx, testBank, "r1", nil); !errors.Is(err, domain.ErrBankDeleting) {
		t.Errorf("deleting bank: %v, want BankDeleting", err)
	}
	if ops := f.jobs(t, domain.OperationKindRetainDocument); len(ops) != 0 {
		t.Errorf("refused reprocesses enqueued %d jobs", len(ops))
	}
}
