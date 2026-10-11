package consolidate

import (
	"context"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/store"
	"sync"
)

// hookAt is a Checkpointer that runs fn at its n-th report (1-based), outside any transaction: a
// concurrent writer landing between two batches.
type hookAt struct {
	n, seen int
	fn      func()
}

// Report counts reports and runs the hook at the n-th.
func (h *hookAt) Report(context.Context, domain.Progress) error {
	h.seen++
	if h.seen == h.n {
		h.fn()
	}
	return nil
}

// hookEmbedder decorates an Embedder: fn runs once, on the first EmbedDocuments call, which happens
// inside decide (after the CONSOLIDATE call, before the apply transaction).
type hookEmbedder struct {
	ai.Embedder
	once sync.Once
	fn   func()
}

// EmbedDocuments runs the hook once, then embeds.
func (h *hookEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	h.once.Do(h.fn)
	return h.Embedder.EmbedDocuments(ctx, texts)
}

// recorder collects what the transactions of a recStore wrote.
type recorder struct {
	versions []domain.ObservationVersion
	evidence map[domain.UnitID][]domain.Evidence
}

// recStore decorates a Store so its transactions record observation versions and evidence.
type recStore struct {
	store.DB
	rec *recorder
}

// InTx runs fn with a recording transaction.
func (s recStore) InTx(ctx context.Context, fn func(store.Tx) error) error {
	return s.DB.InTx(ctx, func(tx store.Tx) error { return fn(recTx{Tx: tx, rec: s.rec}) })
}

// recTx records through its unit and evidence repositories.
type recTx struct {
	store.Tx
	rec *recorder
}

// Units returns the recording unit repository.
func (t recTx) Units() store.UnitRepo { return recUnits{UnitRepo: t.Tx.Units(), rec: t.rec} }

// Evidence returns the recording evidence repository.
func (t recTx) Evidence() store.EvidenceRepo {
	return recEvidence{EvidenceRepo: t.Tx.Evidence(), rec: t.rec}
}

// recUnits records RecordObservationVersion.
type recUnits struct {
	store.UnitRepo
	rec *recorder
}

// RecordObservationVersion records v, then writes it.
func (u recUnits) RecordObservationVersion(
	ctx context.Context, b domain.BankRef, obs domain.UnitID, v domain.ObservationVersion,
) error {
	u.rec.versions = append(u.rec.versions, v)
	return u.UnitRepo.RecordObservationVersion(ctx, b, obs, v)
}

// recEvidence records Add.
type recEvidence struct {
	store.EvidenceRepo
	rec *recorder
}

// Add records ev, then writes it.
func (r recEvidence) Add(ctx context.Context, obs domain.UnitID, ev []domain.Evidence) error {
	if r.rec.evidence == nil {
		r.rec.evidence = map[domain.UnitID][]domain.Evidence{}
	}
	r.rec.evidence[obs] = append(r.rec.evidence[obs], ev...)
	return r.EvidenceRepo.Add(ctx, obs, ev)
}
