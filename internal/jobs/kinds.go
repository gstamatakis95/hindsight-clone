package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/store"
	"time"
)

// The job catalogue: for every job kind, its payload, its serial key and the Enqueue helpers that build its
// row. Every enqueue in the codebase goes through these helpers, so a kind's payload bytes, serial key and
// dedupe policy are decided in one place. Each helper enqueues into the caller's transaction and returns
// what Queue.Enqueue returns. No helper calls tx.Seq: a caller that changes memory content calls tx.Seq(b)
// first, before any read it depends on and before the enqueue; the maintenance tick and continuations take
// no seq at all (Seq refuses a tombstoned bank, which a delete must still reach).

// Serial keys shared by every job of a bank: at most one job per (bank, serial key) runs at a time.
const (
	// SerialKeyConsolidate serializes a bank's consolidations.
	SerialKeyConsolidate = "consolidate"
	// SerialKeyGraph serializes a bank's graph maintenance.
	SerialKeyGraph = "graph"
	// SerialKeyDeleteBank serializes a bank's delete.
	SerialKeyDeleteBank = "bank"
)

// emptyPayload is the payload of the automatic consolidate, graph_maintenance and delete_bank jobs.
var emptyPayload = json.RawMessage(`{}`)

// DocumentSerialKey is the serial key of a document's jobs (retain_document, rederive_memory, retag_document):
// "doc:{id}", so they never overlap.
func DocumentSerialKey(doc domain.DocumentID) string { return "doc:" + string(doc) }

// RefreshSerialKey is the refresh_mental_model serial key of model id: refreshes of one model never overlap.
func RefreshSerialKey(id domain.MentalModelID) string { return "mm:" + string(id) }

// RederiveSerialKey is the serial key of u's rederive: its document's key, so it never overlaps a retain of
// that document. A unit without a document (not produced by retain) falls back to "unit:{id}".
func RederiveSerialKey(u domain.Unit) string {
	if u.DocumentID == "" {
		return "unit:" + string(u.ID)
	}
	return DocumentSerialKey(u.DocumentID)
}

// RetainPayload is the payload of a retain_document job: everything the retain run needs besides the stored
// document row and Job.Overrides (the merged request < item overrides). It pins the content by hash and blob
// key, so a job that resumes after a newer retain of the same document still processes its own content.
// ReprocessDocument enqueues the same payload.
type RetainPayload struct {
	// DocumentID is the document to process.
	DocumentID domain.DocumentID `json:"document_id"`
	// ContentHash is the hex SHA-256 of the original text.
	ContentHash string `json:"content_hash"`
	// BlobKey is where the original text is stored (blob.Key).
	BlobKey string `json:"blob_key"`
	// Tags are the document's normalized tags, written on every unit.
	Tags []string `json:"tags,omitempty"`
	// Context is the item's free-form context, stored on every unit.
	Context string `json:"context,omitempty"`
	// Timestamp is when the content was said; nil with Timeless (or "now" when neither is set).
	Timestamp *time.Time `json:"timestamp,omitempty"`
	// Timeless marks content with no time anchor: units get no mention or occurrence dates.
	Timeless bool `json:"timeless,omitempty"`
	// Metadata is the item's string metadata, stored on every unit.
	Metadata map[string]string `json:"metadata,omitempty"`
	// Entities are the caller's entity hints.
	Entities []RetainHint `json:"entities,omitempty"`
	// ResolveEntities false takes the hint names literally; nil = true.
	ResolveEntities *bool `json:"resolve_entities,omitempty"`
	// ObservationScopes is the item's scope fan-out; nil = the bank default.
	ObservationScopes *domain.ObservationScopes `json:"observation_scopes,omitempty"`
	// ForceReextract re-runs extraction even when content and overrides are unchanged.
	ForceReextract bool `json:"force_reextract,omitempty"`
	// SkipConsolidation suppresses the consolidate job after the retain.
	SkipConsolidation bool `json:"skip_consolidation,omitempty"`
	// ChunkMaxTokens, ChunkOverlapTokens and StructuredChunkMaxTokens pin the chunking resolved at ingest, so a
	// config change between attempts cannot move chunk boundaries under a resuming job; 0 = resolve now.
	ChunkMaxTokens int `json:"chunk_max_tokens,omitempty"`
	// ChunkOverlapTokens is the pinned overlap (see ChunkMaxTokens).
	ChunkOverlapTokens int `json:"chunk_overlap_tokens,omitempty"`
	// StructuredChunkMaxTokens is the pinned structured limit (see ChunkMaxTokens).
	StructuredChunkMaxTokens int `json:"structured_chunk_max_tokens,omitempty"`
}

// RetainHint is one entity hint in a RetainPayload (domain.EntityHint with JSON names).
type RetainHint struct {
	// Text is the entity name.
	Text string `json:"text"`
	// Type is the optional entity type.
	Type string `json:"type,omitempty"`
}

// ConsolidatePayload is the consolidate job's payload. The automatic job (retain, rederive, retag, clears,
// the maintenance tick) carries the empty object {}: every scope, bank defaults.
type ConsolidatePayload struct {
	// Scopes limits a manual run to units whose tags contain one of these tag sets; empty = every unit.
	Scopes []domain.TagSet `json:"scopes,omitempty"`
}

// RefreshPayload is the refresh_mental_model job payload. Optional keys are omitted when unset, so
// Queue.Upgrade keeps a queued row's value unless a joiner sets it (force_full is OR-ed).
type RefreshPayload struct {
	// MentalModelID is the model refreshed.
	MentalModelID domain.MentalModelID `json:"mental_model_id"`
	// ForceFull rewrites in full even when delta mode would apply.
	ForceFull bool `json:"force_full,omitempty"`
	// Strategy overrides the spec's strategy for this refresh.
	Strategy domain.RefreshStrategy `json:"strategy,omitempty"`
	// Manual marks a user-requested refresh; its success clears auto_refresh_paused.
	Manual bool `json:"manual,omitempty"`
}

// RederivePayload is the rederive_memory job payload.
type RederivePayload struct {
	// UnitID is the edited or restored unit. The payload carries no entity names: the RPC committed them.
	UnitID domain.UnitID `json:"unit_id"`
}

// RetagPayload is the retag_document job payload; the tags live on the document row.
type RetagPayload struct {
	// DocumentID is the retagged document.
	DocumentID domain.DocumentID `json:"document_id"`
}

// encode marshals a payload of kind.
func encode(kind domain.OperationKind, v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode %s payload: %w", kind, err)
	}
	return raw, nil
}

// enqueue enqueues j in tx.
func enqueue(ctx context.Context, tx store.Tx, j store.Job) (domain.OperationID, bool, error) {
	return tx.Queue().Enqueue(ctx, j)
}

// retainDocumentJob builds a retain_document row (DedupeNone).
func retainDocumentJob(
	b domain.BankRef, p RetainPayload, overrides *domain.BankConfig, parent domain.OperationID,
) (store.Job, error) {
	raw, err := encode(domain.OperationKindRetainDocument, p)
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Bank: b, Kind: domain.OperationKindRetainDocument, SerialKey: DocumentSerialKey(p.DocumentID),
		Payload: raw, Overrides: overrides, ParentID: parent, Dedupe: store.DedupeNone,
	}, nil
}

// EnqueueRetainDocument enqueues the retain_document job of p's document (serial key "doc:{id}",
// DedupeNone) with overrides, under the retain_batch parent when parent is set.
func EnqueueRetainDocument(
	ctx context.Context, tx store.Tx, b domain.BankRef, p RetainPayload, overrides *domain.BankConfig,
	parent domain.OperationID,
) (domain.OperationID, bool, error) {
	j, err := retainDocumentJob(b, p, overrides, parent)
	if err != nil {
		return "", false, err
	}
	return enqueue(ctx, tx, j)
}

// retainBatchJob builds a retain_batch parent row: no serial key, DedupeNone, {"documents":n}.
func retainBatchJob(b domain.BankRef, documents int) store.Job {
	return store.Job{
		Bank: b, Kind: domain.OperationKindRetainBatch, Payload: []byte(fmt.Sprintf(`{"documents":%d}`, documents)),
	}
}

// EnqueueRetainBatch enqueues the retain_batch parent of an async retain of several documents. It is never
// run: the maintenance tick settles it once its children are terminal.
func EnqueueRetainBatch(
	ctx context.Context, tx store.Tx, b domain.BankRef, documents int,
) (domain.OperationID, bool, error) {
	return enqueue(ctx, tx, retainBatchJob(b, documents))
}

// consolidateJob builds the automatic consolidate row with dedupe d.
func consolidateJob(b domain.BankRef, d store.DedupePolicy) store.Job {
	return store.Job{
		Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: SerialKeyConsolidate, Payload: emptyPayload,
		Dedupe: d,
	}
}

// EnqueueConsolidate enqueues the bank's automatic consolidate (payload {}, no overrides) or joins a
// pending one (DedupePending). Retain, retag, curation and the clears use it.
func EnqueueConsolidate(ctx context.Context, tx store.Tx, b domain.BankRef) (domain.OperationID, bool, error) {
	return enqueue(ctx, tx, consolidateJob(b, store.DedupePending))
}

// EnqueueAutoConsolidate enqueues the bank's automatic consolidate or joins a pending or running one
// (DedupePendingOrRunning): a running consolidate re-enqueues itself while units remain, so the maintenance
// tick never queues a job that would make a failing running consolidate skip its retry.
func EnqueueAutoConsolidate(ctx context.Context, tx store.Tx, b domain.BankRef) (domain.OperationID, bool, error) {
	return enqueue(ctx, tx, consolidateJob(b, store.DedupePendingOrRunning))
}

// scopedConsolidateJob builds a consolidate row limited to scopes and/or carrying overrides (DedupeNone);
// with neither it is the automatic row (DedupePending).
func scopedConsolidateJob(
	b domain.BankRef, scopes []domain.TagSet, overrides *domain.BankConfig,
) (store.Job, error) {
	if len(scopes) == 0 && overrides == nil {
		return consolidateJob(b, store.DedupePending), nil
	}
	j := consolidateJob(b, store.DedupeNone)
	j.Overrides = overrides
	if len(scopes) > 0 {
		raw, err := encode(domain.OperationKindConsolidate, ConsolidatePayload{Scopes: scopes})
		if err != nil {
			return store.Job{}, err
		}
		j.Payload = raw
	}
	return j, nil
}

// EnqueueScopedConsolidate enqueues a consolidate limited to scopes (payload {"scopes":[...]}) and/or
// carrying overrides; such a job is never deduplicated in either direction (DedupeNone), because an
// automatic run would apply bank defaults instead. With no scopes and no overrides it is EnqueueConsolidate.
// The Consolidate RPC and a scoped run's own continuation use it.
func EnqueueScopedConsolidate(
	ctx context.Context, tx store.Tx, b domain.BankRef, scopes []domain.TagSet, overrides *domain.BankConfig,
) (domain.OperationID, bool, error) {
	j, err := scopedConsolidateJob(b, scopes, overrides)
	if err != nil {
		return "", false, err
	}
	return enqueue(ctx, tx, j)
}

// graphJob builds the bank's graph_maintenance row.
func graphJob(b domain.BankRef) store.Job {
	return store.Job{
		Bank: b, Kind: domain.OperationKindGraphMaintenance, SerialKey: SerialKeyGraph, Payload: emptyPayload,
		Dedupe: store.DedupePendingOrRunning,
	}
}

// EnqueueGraph enqueues the bank's graph_maintenance (payload {}) or joins a pending or running one
// (DedupePendingOrRunning). Every removal of units (replace, archive, deleted observations) and the
// maintenance tick use it; a removal that joins a running job is picked up by that job's final re-check.
func EnqueueGraph(ctx context.Context, tx store.Tx, b domain.BankRef) (domain.OperationID, bool, error) {
	return enqueue(ctx, tx, graphJob(b))
}

// rederiveJob builds u's rederive_memory row (DedupeNone).
func rederiveJob(u domain.Unit, overrides *domain.BankConfig) (store.Job, error) {
	raw, err := encode(domain.OperationKindRederiveMemory, RederivePayload{UnitID: u.ID})
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Bank: u.Bank, Kind: domain.OperationKindRederiveMemory, SerialKey: RederiveSerialKey(u), Payload: raw,
		Overrides: overrides, Dedupe: store.DedupeNone,
	}, nil
}

// EnqueueRederive enqueues rederive_memory for u (serial key RederiveSerialKey, DedupeNone) with the
// request's overrides (nil from the maintenance tick).
func EnqueueRederive(
	ctx context.Context, tx store.Tx, u domain.Unit, overrides *domain.BankConfig,
) (domain.OperationID, bool, error) {
	j, err := rederiveJob(u, overrides)
	if err != nil {
		return "", false, err
	}
	return enqueue(ctx, tx, j)
}

// retagJob builds doc's retag_document row (DedupePending).
func retagJob(b domain.BankRef, doc domain.DocumentID) (store.Job, error) {
	raw, err := encode(domain.OperationKindRetagDocument, RetagPayload{DocumentID: doc})
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Bank: b, Kind: domain.OperationKindRetagDocument, SerialKey: DocumentSerialKey(doc), Payload: raw,
		Dedupe: store.DedupePending,
	}, nil
}

// EnqueueRetag enqueues doc's retag_document job or joins a pending one (DedupePending).
func EnqueueRetag(
	ctx context.Context, tx store.Tx, b domain.BankRef, doc domain.DocumentID,
) (domain.OperationID, bool, error) {
	j, err := retagJob(b, doc)
	if err != nil {
		return "", false, err
	}
	return enqueue(ctx, tx, j)
}

// deleteBankJob builds the bank's delete_bank row with dedupe d, due at at (zero = now).
func deleteBankJob(b domain.BankRef, d store.DedupePolicy, at time.Time) store.Job {
	return store.Job{
		Bank: b, Kind: domain.OperationKindDeleteBank, SerialKey: SerialKeyDeleteBank, Payload: emptyPayload,
		RunAt: at, Dedupe: d,
	}
}

// EnqueueDeleteBank enqueues the bank's delete_bank (payload {}) or joins a pending or running one
// (DedupePendingOrRunning). DeleteBank and the tick's resume of stalled deletes use it.
func EnqueueDeleteBank(ctx context.Context, tx store.Tx, b domain.BankRef) (domain.OperationID, bool, error) {
	return enqueue(ctx, tx, deleteBankJob(b, store.DedupePendingOrRunning, time.Time{}))
}

// EnqueueDeleteBankContinuation parks the bank's delete_bank continuation until at, or joins a pending one
// (DedupePending): a delete that finds another job of the bank still processing waits for it.
func EnqueueDeleteBankContinuation(
	ctx context.Context, tx store.Tx, b domain.BankRef, at time.Time,
) (domain.OperationID, bool, error) {
	return enqueue(ctx, tx, deleteBankJob(b, store.DedupePending, at))
}

// RefreshRequest asks for one refresh of a model.
type RefreshRequest struct {
	// Bank owns the model.
	Bank domain.BankRef
	// ID is the model refreshed.
	ID domain.MentalModelID
	// Manual marks a user request: it ignores the interval floor, releases a parked refresh and, on
	// success, clears auto_refresh_paused.
	Manual bool
	// ForceFull rewrites in full even when delta mode would apply.
	ForceFull bool
	// Overrides are per-call overrides; nil = none (a joined refresh keeps its own).
	Overrides *domain.BankConfig
	// Strategy overrides the refresh strategy for this call; unspecified = the model's.
	Strategy domain.RefreshStrategy
	// RunAt is when the refresh becomes due; zero = now. Ignored when joining a queued refresh.
	RunAt time.Time
}

// refreshJob builds req's refresh_mental_model row (DedupePending) and its payload.
func refreshJob(req RefreshRequest, now time.Time) (store.Job, error) {
	raw, err := encode(domain.OperationKindRefreshMentalModel, RefreshPayload{
		MentalModelID: req.ID, ForceFull: req.ForceFull, Strategy: req.Strategy, Manual: req.Manual,
	})
	if err != nil {
		return store.Job{}, err
	}
	runAt := req.RunAt
	if req.Manual || runAt.IsZero() {
		runAt = now
	}
	return store.Job{
		Bank: req.Bank, Kind: domain.OperationKindRefreshMentalModel, SerialKey: RefreshSerialKey(req.ID),
		Payload: raw, Overrides: req.Overrides, RunAt: runAt, Dedupe: store.DedupePending,
	}, nil
}

// EnqueueRefresh queues req's refresh in tx, or joins the model's queued (PENDING) refresh and upgrades
// it: force_full OR-ed, strategy and overrides replaced when given, manual set. A manual request also
// releases a parked refresh (run_at = now). joined reports a join.
func EnqueueRefresh(
	ctx context.Context, tx store.Tx, req RefreshRequest, now time.Time,
) (id domain.OperationID, joined bool, err error) {
	j, err := refreshJob(req, now)
	if err != nil {
		return "", false, err
	}
	id, joined, err = enqueue(ctx, tx, j)
	if err != nil || !joined {
		return id, joined, err
	}
	if req.ForceFull || req.Manual || req.Strategy != domain.RefreshStrategyUnspecified || req.Overrides != nil {
		if err := tx.Queue().Upgrade(ctx, id, j.Payload, req.Overrides); err != nil {
			return "", false, err
		}
	}
	if req.Manual {
		if err := tx.Queue().Reschedule(ctx, id, now); err != nil {
			return "", false, err
		}
	}
	return id, true, nil
}

// QueueModelRefresh enqueues (or joins) an automatic refresh of model id in tx. A paused model, or one
// that is gone (deleted meanwhile), is skipped and returns ("", nil). A model never refreshed yet (no
// last_refreshed_at) is due now and runs in full; otherwise the refresh is parked until
// domain.RefreshDueAt (debounce or interval floor). It reads only through tx (no config resolver, which
// opens its own transaction): the debounce and floor come from the model's overrides, then the bank's own
// config, then the defaults. Curation (models citing a removed unit) and CreateBank from a template (first
// builds) use it.
func QueueModelRefresh(
	ctx context.Context, tx store.Tx, b domain.BankRef, id domain.MentalModelID, now time.Time,
) (domain.OperationID, error) {
	m, err := tx.MentalModels().Get(ctx, b, id)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("queue refresh %s: %w", id, err)
	}
	if m.AutoRefreshPaused {
		return "", nil
	}
	req := RefreshRequest{Bank: b, ID: id}
	if m.LastRefreshedAt == nil {
		req.ForceFull = true
	} else {
		mc, err := modelRefreshConfig(ctx, tx, m)
		if err != nil {
			return "", err
		}
		req.RunAt, _ = domain.RefreshDueAt(m, mc, now)
	}
	opID, _, err := EnqueueRefresh(ctx, tx, req, now)
	if err != nil {
		return "", fmt.Errorf("queue refresh %s: %w", id, err)
	}
	return opID, nil
}

// modelRefreshConfig is the bank's own mental-model config with the model's overrides on top, read in tx
// (tenant and server levels are left out; RefreshDueAt falls back to the defaults).
func modelRefreshConfig(ctx context.Context, tx store.Tx, m domain.MentalModel) (*domain.MentalModelConfig, error) {
	bank, err := tx.Banks().Get(ctx, m.Bank)
	if err != nil {
		return nil, fmt.Errorf("refresh config of %s: %w", m.Spec.ID, err)
	}
	mc := bank.Config.MentalModels
	if m.Spec.Overrides == nil || m.Spec.Overrides.MentalModels == nil {
		return mc, nil
	}
	top := m.Spec.Overrides.MentalModels
	var out domain.MentalModelConfig
	if mc != nil {
		out = *mc
	}
	if top.RefreshDebounceSeconds != nil {
		out.RefreshDebounceSeconds = top.RefreshDebounceSeconds
	}
	if top.MinRefreshIntervalSeconds != nil {
		out.MinRefreshIntervalSeconds = top.MinRefreshIntervalSeconds
	}
	return &out, nil
}

// requeueJob builds the continuation row of claimed job j: same kind and payload ({} when empty), serial key
// key, DedupePending, due at at.
func requeueJob(j store.Job, key string, at time.Time) store.Job {
	payload := j.Payload
	if len(payload) == 0 {
		payload = emptyPayload
	}
	return store.Job{Bank: j.Bank, Kind: j.Kind, SerialKey: key, Payload: payload, RunAt: at, Dedupe: store.DedupePending}
}

// Requeue enqueues a continuation of claimed job j (same kind and payload, serial key key, DedupePending)
// due at at (zero = now), in its own short transaction. DedupePending joins a continuation already queued
// (a delete parks one while another job of the bank is still running). It takes no seq. A continuation for
// a bank that is gone is not needed: ErrNotFound, and ErrBankDeleting for any kind but delete_bank, return
// nil.
func Requeue(ctx context.Context, db store.DB, j store.Job, key string, at time.Time) error {
	err := db.InTx(ctx, func(tx store.Tx) error {
		_, _, err := enqueue(ctx, tx, requeueJob(j, key, at))
		return err
	})
	gone := errors.Is(err, domain.ErrNotFound) ||
		(errors.Is(err, domain.ErrBankDeleting) && j.Kind != domain.OperationKindDeleteBank)
	if gone {
		return nil
	}
	if err != nil {
		return fmt.Errorf("re-enqueue %s: %w", j.Kind, err)
	}
	return nil
}
