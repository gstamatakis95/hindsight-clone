package observation

import (
	"context"
	"fmt"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/prompts"
	"memd/internal/store"
	"slices"
	"strings"
	"time"
)

// actionKind is what a consolidation decision does to a scope's observations.
type actionKind string

// Action kinds.
const (
	// actionCreate adds a new observation.
	actionCreate actionKind = "create"
	// actionUpdate rewrites an existing observation and bumps its version.
	actionUpdate actionKind = "update"
)

// consolidationAction is one decision of the CONSOLIDATE call for one scope.
type consolidationAction struct {
	// Kind is Create | Update | None.
	Kind actionKind
	// ObservationID is the observation to rewrite, for Update.
	ObservationID domain.UnitID
	// Text is the new observation text (Create and Update).
	Text string
	// Evidence is each source unit + exact quote the observation rests on.
	Evidence []domain.Evidence
	// Embedding is Text's embedding, computed before the apply transaction.
	Embedding []float32
}

// embed fills the embedding of every action that has none, in one EmbedDocuments call.
func (c *consolidator) embed(ctx context.Context, actions []consolidationAction) error {
	var idx []int
	var texts []string
	for i, a := range actions {
		if len(a.Embedding) == 0 {
			idx = append(idx, i)
			texts = append(texts, a.Text)
		}
	}
	if len(texts) == 0 {
		return nil
	}
	vecs, err := c.d.Embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		return fmt.Errorf("consolidate: embed observations: %w", err)
	}
	if len(vecs) != len(texts) {
		return fmt.Errorf("%w: consolidate: embedder returned %d vectors for %d texts",
			domain.ErrUnavailable, len(vecs), len(texts))
	}
	for k, i := range idx {
		actions[i].Embedding = vecs[k]
	}
	return nil
}

// dedup checks each create against the scope's existing observations. A merge verdict turns the create
// into an update of the twin with the merged text (re-embedded later), so the twin gains the evidence
// and no near-duplicate is written; keep leaves the create. A failed check keeps the create (nothing is
// lost) unless the run is stopping.
func (c *consolidator) dedup(
	ctx context.Context, s domain.ScopeKey, cfg domain.BankConfig, actions []consolidationAction,
) ([]domain.Usage, error) {
	var usage []domain.Usage
	for i, a := range actions {
		if a.Kind != actionCreate {
			continue
		}
		v, err := c.deduper.Check(ctx, s, cfg, a.Text, a.Embedding)
		if v.Usage != nil {
			usage = append(usage, *v.Usage)
		}
		if err != nil {
			if errStops(ctx, err) {
				return usage, fmt.Errorf("consolidate: dedup: %w", err)
			}
			continue
		}
		if v.Merge && v.TwinID != "" {
			actions[i] = consolidationAction{Kind: actionUpdate, ObservationID: v.TwinID, Text: v.Text, Evidence: a.Evidence}
		}
	}
	return usage, nil
}

// firstUpdates keeps the first update (or dedup merge) of each observation and drops the later ones, so
// one batch writes each observation once and no evidence is attached to a text that doesn't state it.
// dropped reports a drop: the batch's facts are then left unstamped and re-decided by the next run.
func firstUpdates(actions []consolidationAction) (out []consolidationAction, dropped bool) {
	seen := map[domain.UnitID]bool{}
	for _, a := range actions {
		if a.Kind == actionUpdate {
			if seen[a.ObservationID] {
				dropped = true
				continue
			}
			seen[a.ObservationID] = true
		}
		out = append(out, a)
	}
	return out, dropped
}

// applyInput is one batch's decision to write.
type applyInput struct {
	scope   domain.ScopeKey
	actions []consolidationAction
	// incomplete reports that decide dropped part of the decision (mergeUpdates): nothing is stamped.
	incomplete bool
	batch      []domain.UnitID               // the batch's facts
	stamp      []domain.UnitID               // the facts whose last scope this is: stamped in this transaction
	expect     map[domain.UnitID]domain.Unit // the facts as planned (seq and tags)
	cited      map[domain.UnitID][]domain.ScopeKey
	refresh    []domain.MentalModel // models this scope reaches: refreshed when the batch writes
	mm         *domain.MentalModelConfig
}

// applyOutput is what one apply transaction committed.
type applyOutput struct {
	wrote     bool            // an observation was written
	cited     []domain.UnitID // facts the written observations cite in this scope
	stamped   []domain.UnitID // facts stamped consolidated
	unsettled []domain.UnitID // facts whose decision did not fully land: not stamped this run
}

// apply writes one batch's decision in one short transaction with no model call: tx.Seq, then each
// action (observation row stamped with the seq, evidence with exact quotes, an observation_versions row),
// the refreshes of the models the scope reaches, then consolidated_at on the facts this scope finishes.
//
// A fact edited, retagged or removed since it was planned (different seq or tags, or gone) is not live.
// An action citing a fact that is not live, or updating an observation that is gone or left the scope, is
// skipped whole (never rewritten or trimmed), and then no fact of the batch is stamped: the next run
// re-decides them (docs/design/consolidation.md#stamping).
func (c *consolidator) apply(ctx context.Context, in applyInput) (applyOutput, error) {
	var out applyOutput
	err := c.d.DB.InTx(ctx, func(tx store.Tx) error {
		out = applyOutput{}
		now := c.d.Clock.Now()
		// Seq first, every time: it locks the bank row, so no edit or retag can commit between the
		// re-read below and the stamp (writers of the bank serialize on that row).
		seq, err := tx.Seq(ctx, in.scope.BankRef)
		if err != nil {
			return fmt.Errorf("seq: %w", err)
		}
		live, err := liveFacts(ctx, tx, in.scope.BankRef, in.batch, in.expect)
		if err != nil {
			return err
		}
		var complete bool
		if out.cited, complete, err = c.writeActions(ctx, tx, in, live, seq, now); err != nil {
			return err
		}
		out.wrote = len(out.cited) > 0
		complete = complete && !in.incomplete
		if out.wrote {
			if err := enqueueRefreshes(ctx, tx, in.scope.BankRef, in.mm, in.refresh, now); err != nil {
				return err
			}
		}
		for _, id := range in.batch {
			if _, ok := live[id]; !complete || !ok {
				out.unsettled = append(out.unsettled, id)
			}
		}
		if !complete {
			return nil
		}
		if out.stamped, err = stampable(ctx, tx, in.stamp, live, in.cited); err != nil {
			return err
		}
		if len(out.stamped) == 0 {
			return nil
		}
		return tx.Units().MarkConsolidated(ctx, out.stamped, now)
	})
	if err != nil {
		return applyOutput{}, fmt.Errorf("consolidate: apply in scope %q: %w", in.scope.String(), err)
	}
	return out, nil
}

// writeActions writes every action that can land, stamped with seq, and returns the facts the written
// actions cite (empty when nothing was written) and whether every action landed.
func (c *consolidator) writeActions(
	ctx context.Context, tx store.Tx, in applyInput, live map[domain.UnitID]domain.Unit, seq int64, now time.Time,
) (cited []domain.UnitID, complete bool, err error) {
	complete = true
	for _, a := range in.actions {
		obs, ok, err := c.target(ctx, tx, in.scope, a, live)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			complete = false
			continue
		}
		if err := writeObservation(ctx, tx, obs, a, seq, now); err != nil {
			return nil, false, err
		}
		for _, e := range a.Evidence {
			if !slices.Contains(cited, e.Unit) {
				cited = append(cited, e.Unit)
			}
		}
	}
	return cited, complete, nil
}

// liveFacts re-reads ids and keeps those unchanged since they were planned: same seq and same tags. An
// edit or a retag bumps the seq, so a changed fact is treated as gone.
func liveFacts(
	ctx context.Context, tx store.Tx, b domain.BankRef, ids []domain.UnitID, expect map[domain.UnitID]domain.Unit,
) (map[domain.UnitID]domain.Unit, error) {
	facts, err := tx.GetUnits(ctx, b, ids)
	if err != nil {
		return nil, fmt.Errorf("re-read facts: %w", err)
	}
	live := map[domain.UnitID]domain.Unit{}
	for _, f := range facts {
		if p, ok := expect[f.ID]; ok && p.Seq == f.Seq && domain.SameTagSet(p.Tags, f.Tags) {
			live[f.ID] = f
		}
	}
	return live, nil
}

// stampable returns those of ids that are live and still cited in every scope of cited: the scopes the
// plan skipped because they already cited the unit, and the scopes this run wrote an observation citing it
// in. A concurrent clear, retag or invalidate may have deleted that observation since, resetting the unit
// without changing its seq. The caller holds tx.Seq, so no clear can commit in between.
func stampable(
	ctx context.Context, tx store.Tx, ids []domain.UnitID, live map[domain.UnitID]domain.Unit,
	cited map[domain.UnitID][]domain.ScopeKey,
) ([]domain.UnitID, error) {
	var out []domain.UnitID
	for _, id := range ids {
		if _, ok := live[id]; !ok {
			continue
		}
		still := true
		for _, k := range cited[id] {
			ok, err := tx.Evidence().Cites(ctx, k, id)
			if err != nil {
				return nil, fmt.Errorf("cites %s in scope %q: %w", id, k.String(), err)
			}
			if !ok {
				still = false
				break
			}
		}
		if still {
			out = append(out, id)
		}
	}
	return out, nil
}

// target returns the observation an action writes: the existing one for an update (re-read in tx), a new
// one for a create. ok is false when the action must be skipped: a cited fact is not live, or the update's
// observation is gone or left the scope.
func (c *consolidator) target(
	ctx context.Context, tx store.Tx, s domain.ScopeKey, a consolidationAction, live map[domain.UnitID]domain.Unit,
) (domain.Unit, bool, error) {
	if len(a.Evidence) == 0 {
		return domain.Unit{}, false, nil
	}
	for _, e := range a.Evidence {
		if _, ok := live[e.Unit]; !ok {
			return domain.Unit{}, false, nil
		}
	}
	if a.Kind == actionUpdate {
		cur, err := tx.GetUnits(ctx, s.BankRef, []domain.UnitID{a.ObservationID})
		if err != nil {
			return domain.Unit{}, false, fmt.Errorf("re-read observation %s: %w", a.ObservationID, err)
		}
		ok := len(cur) == 1 && cur[0].Type == domain.FactTypeObservation && domain.SameTagSet(cur[0].Tags, s.Tags)
		if !ok {
			return domain.Unit{}, false, nil
		}
		return cur[0], true, nil
	}
	obs := domain.Unit{ID: c.newID(), Bank: s.BankRef, Type: domain.FactTypeObservation, Tags: s.Tags}
	for _, e := range a.Evidence {
		obs.OccurredStart = earliest(obs.OccurredStart, live[e.Unit].OccurredStart)
		obs.OccurredEnd = latest(obs.OccurredEnd, live[e.Unit].OccurredEnd)
		obs.MentionedAt = latest(obs.MentionedAt, live[e.Unit].MentionedAt)
	}
	return obs, true, nil
}

// writeObservation upserts obs with the action's text and embedding at the next version (1 for a new
// one), adds the evidence and records the version with every source the observation now cites.
func writeObservation(
	ctx context.Context, tx store.Tx, obs domain.Unit, a consolidationAction, seq int64, now time.Time,
) error {
	sources := slices.Clone(obs.SourceIDs)
	for _, e := range a.Evidence {
		if !slices.Contains(sources, e.Unit) {
			sources = append(sources, e.Unit)
		}
	}
	slices.Sort(sources)
	obs.Text, obs.Embedding, obs.Seq = a.Text, a.Embedding, seq
	obs.ProofCount, obs.Version = len(sources), obs.Version+1
	if err := tx.Units().UpsertObservation(ctx, obs); err != nil {
		return fmt.Errorf("upsert observation %s: %w", obs.ID, err)
	}
	if err := tx.Evidence().Add(ctx, obs.ID, a.Evidence); err != nil {
		return fmt.Errorf("evidence of %s: %w", obs.ID, err)
	}
	v := domain.ObservationVersion{Version: obs.Version, Text: obs.Text, SourceFactIDs: sources, CreatedAt: now}
	if err := tx.Units().RecordObservationVersion(ctx, obs.Bank, obs.ID, v); err != nil {
		return fmt.Errorf("version of %s: %w", obs.ID, err)
	}
	return nil
}

// earliest returns the earlier of two optional times.
func earliest(a, b *time.Time) *time.Time {
	if a == nil || (b != nil && b.Before(*a)) {
		return b
	}
	return a
}

// latest returns the later of two optional times.
func latest(a, b *time.Time) *time.Time {
	if a == nil || (b != nil && b.After(*a)) {
		return b
	}
	return a
}

// Dedup defaults.
const (
	// defaultDedupThreshold is dedup_threshold when unset (the spec's 0.97).
	defaultDedupThreshold = 0.97
	// dedupTopK is how many nearest observations the probe reads; only the best one is checked.
	dedupTopK = 5
)

// dedupVerdict is the outcome of one dedup check.
type dedupVerdict struct {
	// TwinID is the near-duplicate observation; empty when none is at or above the threshold.
	TwinID domain.UnitID
	// Merge reports that DEDUP_CHECK judged both to state the same fact.
	Merge bool
	// Text is the merged observation text (Merge only).
	Text string
	// Usage is the DEDUP_CHECK call's usage; nil when no call was made.
	Usage *domain.Usage
}

// deduper reconciles a new observation with a near-duplicate already in its scope, over the vector search of
// store.Reader.
type deduper struct {
	llm    ai.LLM
	router *ai.Router
	read   store.Reader
}

// newDeduper returns the deduper. It probes with store.Reader.SearchVector (exact cosine over the scope's
// observations) instead of recall: the threshold is a cosine similarity, which fused recall scores
// are not.
func newDeduper(llm ai.LLM, router *ai.Router, r store.Reader) *deduper {
	return &deduper{llm: llm, router: router, read: r}
}

// dedupResponse is the DEDUP_CHECK response (prompts dedup_system.schema.json).
type dedupResponse struct {
	Action string `json:"action"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

// Check finds the observation of scope s nearest to emb with cosine >= cfg's dedup_threshold (>= 1 disables
// dedup) and asks DEDUP_CHECK whether text repeats it. It runs outside any transaction; the caller folds a
// merge into its apply transaction (the twin is updated with the merged text and gains the new observation's
// evidence, and the new observation is not created).
func (d *deduper) Check(
	ctx context.Context, s domain.ScopeKey, cfg domain.BankConfig, text string, emb []float32,
) (dedupVerdict, error) {
	threshold := defaultDedupThreshold
	if cfg.GetObservations().DedupThreshold != nil {
		threshold = *cfg.GetObservations().DedupThreshold
	}
	if threshold >= 1 || len(emb) == 0 {
		return dedupVerdict{}, nil
	}
	twin, ok, err := d.nearest(ctx, s, emb, threshold)
	if err != nil || !ok {
		return dedupVerdict{}, err
	}
	binding, err := d.router.Resolve(cfg, domain.ModelSlotDedupCheck)
	if err != nil {
		return dedupVerdict{}, fmt.Errorf("consolidate: dedup binding: %w", err)
	}
	req, err := dedupRequest(binding, text, twin.Text)
	if err != nil {
		return dedupVerdict{}, err
	}
	var out dedupResponse
	usage, err := d.llm.Structured(ctx, req, &out)
	v := dedupVerdict{TwinID: twin.ID, Usage: &usage}
	if err != nil {
		return v, fmt.Errorf("consolidate: dedup check: %w", err)
	}
	if out.Action == "merge" {
		v.Merge = true
		v.Text = strings.TrimSpace(out.Text)
		if v.Text == "" {
			v.Text = twin.Text
		}
	}
	return v, nil
}

// nearest returns the scope's observation most similar to emb at or above threshold.
func (d *deduper) nearest(
	ctx context.Context, s domain.ScopeKey, emb []float32, threshold float64,
) (domain.Unit, bool, error) {
	hits, err := d.read.SearchVector(ctx, store.VectorQuery{
		Bank:          s.BankRef,
		Embedding:     emb,
		Types:         []domain.FactType{domain.FactTypeObservation},
		Tags:          scopePredicate(s),
		MinSimilarity: threshold,
		Limit:         dedupTopK,
	})
	if err != nil {
		return domain.Unit{}, false, fmt.Errorf("consolidate: dedup probe: %w", err)
	}
	if len(hits) == 0 || hits[0].Score < threshold {
		return domain.Unit{}, false, nil
	}
	us, err := d.read.GetUnits(ctx, s.BankRef, []domain.UnitID{hits[0].ID})
	if err != nil {
		return domain.Unit{}, false, fmt.Errorf("consolidate: dedup twin: %w", err)
	}
	if len(us) == 0 {
		return domain.Unit{}, false, nil
	}
	return us[0], true, nil
}

// dedupRequest renders the DEDUP_CHECK call.
func dedupRequest(binding domain.ModelBinding, text, existing string) (ai.StructuredRequest, error) {
	data := prompts.DedupData{New: text, Existing: existing}
	system, err := prompts.Render(prompts.DedupSystem, data)
	if err != nil {
		return ai.StructuredRequest{}, fmt.Errorf("consolidate: render dedup system: %w", err)
	}
	user, err := prompts.Render(prompts.DedupUser, data)
	if err != nil {
		return ai.StructuredRequest{}, fmt.Errorf("consolidate: render dedup user: %w", err)
	}
	schema, err := prompts.Schema(prompts.DedupSystem)
	if err != nil {
		return ai.StructuredRequest{}, fmt.Errorf("consolidate: dedup schema: %w", err)
	}
	return ai.StructuredRequest{
		Slot: domain.ModelSlotDedupCheck, Binding: binding, Name: prompts.DedupSystem,
		System: system, User: user, Schema: schema,
	}, nil
}

// scopePredicate selects exactly the observations of scope s (its exact tag set; the shared scope is
// the untagged one).
func scopePredicate(s domain.ScopeKey) *domain.TagPredicate {
	return &domain.TagPredicate{Op: domain.PredicateTags, Tags: s.Tags, Match: domain.TagsMatchExact}
}
