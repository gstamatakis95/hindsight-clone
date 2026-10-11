package memory

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/linking"
	"memd/internal/store"
	"slices"
	"time"
)

// preparedFact is one extracted, normalized and embedded fact, ready to store.
type preparedFact struct {
	fact    domain.ExtractedFact
	unitID  domain.UnitID
	chunkID domain.ChunkID
	vec     []float32
	causes  []int // indexes into the batch's facts
}

// preparedBatch is one batch of chunks with their facts, prepared outside any transaction.
type preparedBatch struct {
	chunks []domain.Chunk
	facts  []preparedFact
}

// processBatch prepares pending chunks (model calls) and then commits them in one short transaction.
func (r *retainer) processBatch(
	ctx context.Context, rn *run, pending []domain.Chunk,
) ([]domain.UnitID, []domain.Usage, error) {
	pb, usage, err := r.prepare(ctx, rn, pending)
	if err != nil {
		return nil, usage, err
	}
	if err := r.commitBatch(ctx, rn, pb); err != nil {
		return nil, usage, err
	}
	rn.replaced = true
	for _, c := range pb.chunks {
		rn.owned = append(rn.owned, c.Index)
	}
	ids := make([]domain.UnitID, len(pb.facts))
	for i, f := range pb.facts {
		ids[i] = f.unitID
	}
	return ids, usage, nil
}

// prepare extracts every pending chunk, resolves dates and embeds the facts. No transaction is open.
func (r *retainer) prepare(
	ctx context.Context, rn *run, pending []domain.Chunk,
) (*preparedBatch, []domain.Usage, error) {
	pb := &preparedBatch{}
	var usage []domain.Usage
	for _, c := range pending {
		c.ID = domain.ChunkID(r.d.NewID())
		pb.chunks = append(pb.chunks, c)
		facts, u, err := r.d.Extractor.Extract(ctx, extractInput{
			Bank: rn.j.Bank, DocumentID: rn.p.DocumentID, ChunkIndex: c.Index, Text: c.Text, Context: rn.p.Context,
			MentionedAt: rn.mentioned, Metadata: rn.p.Metadata, Hints: hints(rn.p), Config: rn.cfg,
		})
		usage = append(usage, u...)
		if err != nil {
			return nil, usage, fmt.Errorf("retain: extract chunk %d: %w", c.Index, err)
		}
		base := len(pb.facts)
		for _, f := range facts {
			if err := r.normalize(ctx, rn, &f); err != nil {
				return nil, usage, err
			}
			causes := make([]int, 0, len(f.Causes))
			for _, c := range f.Causes {
				if c >= 0 && base+c < len(pb.facts) { // only earlier facts of this chunk
					causes = append(causes, base+c)
				}
			}
			pb.facts = append(pb.facts, preparedFact{
				fact: f, unitID: domain.UnitID(r.d.NewID()), chunkID: c.ID, causes: causes,
			})
		}
	}
	return pb, usage, r.embed(ctx, pb)
}

// normalize applies the item's time handling: a timeless item's facts carry no dates; otherwise a relative
// expression is resolved against the mention time.
func (r *retainer) normalize(ctx context.Context, rn *run, f *domain.ExtractedFact) error {
	if rn.p.Timeless {
		f.OccurredStart, f.OccurredEnd, f.TimeExpression = nil, nil, ""
		return nil
	}
	if f.OccurredStart != nil || f.TimeExpression == "" || r.d.Dates == nil || rn.mentioned == nil {
		return nil
	}
	if err := r.d.Dates.Normalize(ctx, rn.cfg, f, *rn.mentioned); err != nil {
		return fmt.Errorf("retain: normalize dates: %w", err)
	}
	return nil
}

// embed embeds every fact of the batch in one call.
func (r *retainer) embed(ctx context.Context, pb *preparedBatch) error {
	if len(pb.facts) == 0 {
		return nil
	}
	texts := make([]string, len(pb.facts))
	for i, f := range pb.facts {
		texts[i] = f.fact.Text
	}
	vecs, err := r.d.Embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		return err
	}
	if len(vecs) != len(texts) {
		return fmt.Errorf("%w: embedder returned %d vectors for %d texts", domain.ErrUnavailable, len(vecs), len(texts))
	}
	for i := range pb.facts {
		pb.facts[i].vec = vecs[i]
	}
	return nil
}

// commitBatch writes one batch: the replacement of the old units (first batch only), the chunks, the units,
// their entities and labels, and their links, all under one change seq.
func (r *retainer) commitBatch(ctx context.Context, rn *run, pb *preparedBatch) error {
	replace := !rn.replaced
	return r.d.Store.InTx(ctx, func(tx store.Tx) error {
		seq, err := tx.Seq(ctx, rn.j.Bank)
		if err != nil {
			return err
		}
		if err := r.checkOwner(ctx, tx, rn); err != nil {
			return err
		}
		if replace {
			if err := r.replace(ctx, tx, rn); err != nil {
				return err
			}
		}
		if err := tx.Chunks().Insert(ctx, pb.chunks); err != nil {
			return err
		}
		return r.writeUnits(ctx, tx, rn, pb, seq)
	})
}

// writeUnits resolves labels and entities, inserts the units, attaches their entities and links them.
func (r *retainer) writeUnits(ctx context.Context, tx store.Tx, rn *run, pb *preparedBatch, seq int64) error {
	if len(pb.facts) == 0 {
		return nil
	}
	b := rn.j.Bank
	units := make([]domain.Unit, len(pb.facts))
	ents := make([][]domain.EntityID, len(pb.facts))
	for i, pf := range pb.facts {
		labelIDs, labelTags, err := r.d.Labels.Resolve(ctx, tx, b, rn.cfg.GetRetain().EntityLabels, pf.fact.Labels)
		if err != nil {
			return fmt.Errorf("retain: labels: %w", err)
		}
		entityIDs, err := r.d.Entities.Resolve(ctx, tx, b, linking.ResolveInput{
			Mentions: pf.fact.Entities, Hints: hints(rn.p), Literal: !boolOr(rn.p.ResolveEntities, true),
			At: factTime(pf.fact, rn.mentioned), Threshold: rn.t.matchThreshold,
		})
		if err != nil {
			return fmt.Errorf("retain: entities: %w", err)
		}
		units[i] = r.unit(rn, pf, seq, labelTags)
		ents[i] = slices.Compact(slices.Sorted(slices.Values(append(entityIDs, labelIDs...))))
	}
	if err := tx.Units().Insert(ctx, units); err != nil {
		return err
	}
	for i, u := range units {
		if len(ents[i]) > 0 {
			if err := tx.Entities().Attach(ctx, u.ID, ents[i]); err != nil {
				return err
			}
		}
	}
	causes := linking.CauseMap{}
	for i, pf := range pb.facts {
		for _, c := range pf.causes {
			causes[units[i].ID] = append(causes[units[i].ID], units[c].ID)
		}
	}
	if err := r.d.Linker.LinkWith(ctx, tx, b, units, causes, rn.cfg); err != nil {
		return fmt.Errorf("retain: link: %w", err)
	}
	return nil
}

// unit builds the stored unit of one fact.
func (r *retainer) unit(rn *run, pf preparedFact, seq int64, labelTags []string) domain.Unit {
	typ := pf.fact.Type
	if typ != domain.FactTypeExperience {
		typ = domain.FactTypeWorld
	}
	return domain.Unit{
		ID: pf.unitID, Bank: rn.j.Bank, DocumentID: rn.p.DocumentID, ChunkID: pf.chunkID, Type: typ,
		Text: pf.fact.Text, Context: rn.p.Context, Metadata: rn.p.Metadata,
		Tags:      domain.NormalizeTags(append(slices.Clone(rn.tags), labelTags...)),
		Embedding: pf.vec, OccurredStart: pf.fact.OccurredStart, OccurredEnd: pf.fact.OccurredEnd,
		MentionedAt: rn.mentioned, Timeless: rn.p.Timeless, Seq: seq, State: domain.MemoryStateValid,
	}
}

// factTime is a fact's time for entity recency: when it occurred, else when it was said.
func factTime(f domain.ExtractedFact, mentioned *time.Time) time.Time {
	switch {
	case f.OccurredStart != nil:
		return *f.OccurredStart
	case mentioned != nil:
		return *mentioned
	default:
		return time.Time{}
	}
}

// replace removes the document's old units in the first batch's transaction: observations citing them are
// deleted and their other sources reset (EvidenceRepo.DeleteCiting, before the units go, since evidence rows
// cascade), then the units are deleted and graph maintenance is queued for their neighbours. The units are
// listed with UnitRepo.IDsByDocument, which writes nothing (decision 802, superseded by the CCR batch).
// It first reopens the document (decision 2200), so a run that fails after replacing never leaves it stamped
// complete.
func (r *retainer) replace(ctx context.Context, tx store.Tx, rn *run) error {
	b, doc := rn.j.Bank, rn.p.DocumentID
	if err := reopen(ctx, tx, b, doc); err != nil {
		return err
	}
	ids, err := tx.Units().IDsByDocument(ctx, b, doc)
	if err != nil || len(ids) == 0 {
		return err
	}
	if _, _, err := tx.Evidence().DeleteCiting(ctx, b, ids); err != nil {
		return err
	}
	if _, err := tx.Units().DeleteByDocument(ctx, b, doc); err != nil {
		return err
	}
	_, _, err = jobs.EnqueueGraph(ctx, tx, b)
	return err
}

// reopen clears the document's extracted_at when another retain of the same content completed it after this
// run started (overlapping runs, decision 807): the units this run is about to replace are that retain's, so
// until this run completes the document is in flight again. It re-upserts the document as read in this
// transaction (the bank is Seq-locked), so only extracted_at changes. Without it, a run failing after its
// first batch would leave a document stamped complete with only that batch's units, and every later retain
// of the same content would report Unchanged (decision 2200).
func reopen(ctx context.Context, tx store.Tx, b domain.BankRef, id domain.DocumentID) error {
	d, err := tx.Documents().Get(ctx, b, id)
	if err != nil || d.ExtractedAt == nil {
		return err
	}
	return tx.Documents().Upsert(ctx, domain.Document{
		ID: d.ID, Bank: d.Bank, Overrides: d.Overrides, Tags: d.Tags, BlobKey: d.BlobKey,
		ObservationScopeModes: d.ObservationScopeModes,
	})
}

// complete stamps the document's hashes and extracted_at, appends RETAIN_COMPLETED and queues the bank's
// consolidation, in one transaction (which also replaces the old units when no batch did).
func (r *retainer) complete(ctx context.Context, rn *run) error {
	replace := !rn.replaced
	b, doc := rn.j.Bank, rn.p.DocumentID
	err := r.d.Store.InTx(ctx, func(tx store.Tx) error {
		seq, err := tx.Seq(ctx, b)
		if err != nil {
			return err
		}
		if err := r.checkOwner(ctx, tx, rn); err != nil {
			return err
		}
		if replace {
			if err := r.replace(ctx, tx, rn); err != nil {
				return err
			}
		}
		if err := tx.Documents().Complete(ctx, b, doc, rn.p.ContentHash, rn.oh); err != nil {
			return err
		}
		if err := tx.Events().Append(ctx, domain.LifecycleEvent{
			Seq: seq, Type: domain.EventTypeRetainCompleted, Bank: b, CreatedAt: r.now(), OperationID: rn.j.ID,
			SubjectID: string(doc),
		}); err != nil {
			return err
		}
		if !consolidates(rn) {
			return nil
		}
		_, _, err = jobs.EnqueueConsolidate(ctx, tx, b)
		return err
	})
	if err == nil {
		rn.replaced = true
	}
	return err
}

// consolidates reports whether the retain queues the bank's automatic consolidation: not with
// skip_consolidation, nor when observations or automatic consolidation are disabled.
func consolidates(rn *run) bool {
	return !rn.p.SkipConsolidation && boolOr(rn.cfg.GetObservations().Enabled, true) &&
		boolOr(rn.cfg.GetObservations().AutoConsolidation, true)
}
