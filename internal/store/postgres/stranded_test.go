package postgres_test

import (
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"reflect"
	"testing"
	"time"
)

// strandedRederive enqueues a rederive_memory of u and returns its id.
func strandedRederive(t *testing.T, s *postgres.DB, u domain.Unit) domain.OperationID {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"unit_id": string(u.ID)})
	var id domain.OperationID
	if err := s.InTx(t.Context(), func(tx store.Tx) error {
		var err error
		id, _, err = tx.Queue().Enqueue(t.Context(), store.Job{
			Bank: u.Bank, Kind: domain.OperationKindRederiveMemory, SerialKey: "doc:" + string(u.DocumentID),
			Payload: payload, Dedupe: store.DedupeNone,
		})
		return err
	}); err != nil {
		t.Fatalf("enqueue rederive: %v", err)
	}
	return id
}

// TestStrandedFacts checks the maintenance read behind the NULL-embedding rederive reconcile: a fact whose
// rederive failed for good is listed once the retry delay has passed, and not while a rederive is pending,
// once it has an embedding, or when its bank is tombstoned.
func TestStrandedFacts(t *testing.T) {
	t.Parallel()
	s := pgtest.NewStore(t)
	ctx := t.Context()
	stranded := fuUnit(fuBank, "stranded fact")
	stranded.DocumentID = "doc-1"
	healthy := fuUnit(fuBank, "healthy fact")
	healthy.Embedding = make([]float32, domain.EmbeddingDim)
	healthy.Embedding[0] = 1
	if err := fuTx(t, s, func(tx store.Tx) error {
		if _, err := tx.Banks().Ensure(ctx, fuBank); err != nil {
			return err
		}
		return tx.Documents().Upsert(ctx, domain.Document{Bank: fuBank, ID: "doc-1"})
	}); err != nil {
		t.Fatal(err)
	}
	fuInsert(t, s, stranded, healthy)
	strandedRederive(t, s, stranded)
	strandedRederive(t, s, healthy)
	jobs, err := s.Claim(ctx, "w", []domain.OperationKind{domain.OperationKindRederiveMemory}, 10)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("claim: %v %v", jobs, err)
	}
	for _, j := range jobs {
		if err := s.Fail(ctx, j.ID, errors.New("embedder down"), nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.StrandedFacts(ctx, time.Hour, 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("within the retry delay: %v %v", got, err)
	}
	got, err = s.StrandedFacts(ctx, 0, 10)
	want := []domain.Unit{{ID: stranded.ID, Bank: fuBank, DocumentID: "doc-1"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("stranded: %+v %v, want %+v", got, err, want)
	}
	pending := strandedRederive(t, s, stranded)
	if got, _ := s.StrandedFacts(ctx, 0, 10); len(got) != 0 {
		t.Fatalf("listed while rederive %s is pending: %+v", pending, got)
	}
	fuMarkDeleting(t, s, fuBank)
	if got, _ := s.StrandedFacts(ctx, 0, 10); len(got) != 0 {
		t.Fatalf("listed on a tombstoned bank: %+v", got)
	}
}
