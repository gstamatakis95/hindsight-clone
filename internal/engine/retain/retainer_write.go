package retain

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"time"
)

// preparedFact is one extracted, normalized and embedded fact, ready to store.
type preparedFact struct {
	fact    ExtractedFact
	unitID  port.UnitID
	chunkID port.ChunkID
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
) ([]port.UnitID, []domain.Usage, error) {
	pb, usage, err := r.prepare(ctx, rn, pending)
	if err != nil {
		return nil, usage, err
	}
	if err := r.commitBatch(ctx, rn, pb); err != nil {
		return nil, usage, err
	}
	rn.replaced = true
	ids := make([]port.UnitID, len(pb.facts))
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
		c.ID = port.ChunkID(r.d.NewID())
		pb.chunks = append(pb.chunks, c)
		facts, u, err := r.d.Extractor.Extract(ctx, ExtractInput{
			Bank: rn.j.Bank, DocumentID: rn.p.DocumentID, ChunkIndex: c.Index, Text: c.Text, Context: rn.p.Context,
			MentionedAt: rn.mentioned, Metadata: rn.p.Metadata, Hints: rn.p.hints(), Config: rn.cfg,
		})
		usage = append(usage, u...)
		if err != nil {
			return nil, usage, err
		}
		base := len(pb.facts)
		for _, f := range facts {
			if err := r.normalize(ctx, rn, &f); err != nil {
				return nil, usage, err
			}
			causes := make([]int, 0, len(f.Causes))
			for _, c := range f.Causes {
				causes = append(causes, base+c)
			}
			pb.facts = append(pb.facts, preparedFact{
				fact: f, unitID: port.UnitID(r.d.NewID()), chunkID: c.ID, causes: causes,
			})
		}
	}
	return pb, usage, r.embed(ctx, pb)
}

// normalize applies the item's time handling: a timeless item's facts carry no dates; otherwise a relative
// expression is resolved against the mention time.
func (r *retainer) normalize(ctx context.Context, rn *run, f *ExtractedFact) error {
	if rn.p.Timeless {
		f.OccurredStart, f.OccurredEnd, f.TimeExpression = nil, nil, ""
		return nil
	}
	if f.OccurredStart != nil || f.TimeExpression == "" || r.d.Dates == nil || rn.mentioned == nil {
		return nil
	}
	return r.d.Dates.Normalize(ctx, rn.cfg, f, *rn.mentioned)
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
	return r.d.Store.InTx(ctx, func(tx port.Tx) error {
		seq, err := tx.Seq(ctx, rn.j.Bank)
		if err != nil {
			return err
		}
		if replace {
			if err := r.replace(ctx, tx, rn, seq); err != nil {
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
func (r *retainer) writeUnits(ctx context.Context, tx port.Tx, rn *run, pb *preparedBatch, seq int64) error {
	if len(pb.facts) == 0 {
		return nil
	}
	b := rn.j.Bank
	units := make([]domain.Unit, len(pb.facts))
	ents := make([][]port.EntityID, len(pb.facts))
	for i, pf := range pb.facts {
		labelIDs, labelTags, err := r.d.Labels.Resolve(ctx, tx, b, rn.cfg.EntityLabels, pf.fact.Labels)
		if err != nil {
			return err
		}
		entityIDs, err := r.d.Entities.Resolve(ctx, tx, b, ResolveInput{
			Mentions: pf.fact.Entities, Hints: rn.p.hints(), Literal: !boolOr(rn.p.ResolveEntities, true),
			At: factTime(pf.fact, rn.mentioned), Threshold: rn.t.matchThreshold,
		})
		if err != nil {
			return err
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
	causes := CauseMap{}
	for i, pf := range pb.facts {
		for _, c := range pf.causes {
			causes[units[i].ID] = append(causes[units[i].ID], units[c].ID)
		}
	}
	return r.d.Linker.LinkWith(ctx, tx, b, units, causes, rn.cfg)
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
func factTime(f ExtractedFact, mentioned *time.Time) time.Time {
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
// listed with SetDocumentTags (decision 802).
func (r *retainer) replace(ctx context.Context, tx port.Tx, rn *run, seq int64) error {
	b, doc := rn.j.Bank, rn.p.DocumentID
	ids, err := tx.Units().SetDocumentTags(ctx, b, doc, rn.tags, seq)
	if err != nil || len(ids) == 0 {
		return err
	}
	if _, _, err := tx.Evidence().DeleteCiting(ctx, b, ids); err != nil {
		return err
	}
	if _, err := tx.Units().DeleteByDocument(ctx, b, doc); err != nil {
		return err
	}
	_, _, err = tx.Queue().Enqueue(ctx, port.Job{
		Bank: b, Kind: domain.OperationKindGraphMaintenance, SerialKey: graphSerialKey,
		Payload: json.RawMessage(`{}`), Dedupe: port.DedupePendingOrRunning,
	})
	return err
}

// complete stamps the document's hashes and extracted_at, appends RETAIN_COMPLETED and queues the bank's
// consolidation, in one transaction (which also replaces the old units when no batch did).
func (r *retainer) complete(ctx context.Context, rn *run) error {
	replace := !rn.replaced
	b, doc := rn.j.Bank, rn.p.DocumentID
	err := r.d.Store.InTx(ctx, func(tx port.Tx) error {
		seq, err := tx.Seq(ctx, b)
		if err != nil {
			return err
		}
		if replace {
			if err := r.replace(ctx, tx, rn, seq); err != nil {
				return err
			}
		}
		if err := tx.Documents().Complete(ctx, b, doc, rn.p.ContentHash, rn.oh); err != nil {
			return err
		}
		if err := tx.Events().Append(ctx, domain.LifecycleEvent{
			Seq: seq, Type: domain.EventTypeRetainCompleted, Bank: b, At: r.now(), OperationID: rn.j.ID,
			SubjectID: string(doc),
		}); err != nil {
			return err
		}
		if !consolidates(rn) {
			return nil
		}
		_, _, err = tx.Queue().Enqueue(ctx, port.Job{
			Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: consolidateSerialKey,
			Payload: json.RawMessage(`{}`), Dedupe: port.DedupePending,
		})
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
	return !rn.p.SkipConsolidation && boolOr(rn.cfg.EnableObservations, true) &&
		boolOr(rn.cfg.AutoConsolidation, true)
}
