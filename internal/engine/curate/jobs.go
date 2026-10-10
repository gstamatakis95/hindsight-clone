package curate

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// Serial keys of the jobs this package enqueues (SPEC "Jobs", decision 1301).
const (
	// consolidateKey serializes consolidation per bank.
	consolidateKey = "consolidate"
	// graphKey serializes graph maintenance per bank.
	graphKey = "graph"
)

// emptyPayload is the automatic consolidate and graph_maintenance payload.
var emptyPayload = json.RawMessage(`{}`)

// enqueueConsolidate enqueues an automatic consolidation (payload {}, DedupePending) in tx.
func enqueueConsolidate(ctx context.Context, tx port.Tx, b port.BankRef) (port.OperationID, error) {
	id, _, err := tx.Queue().Enqueue(ctx, port.Job{
		Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: consolidateKey,
		Payload: emptyPayload, Dedupe: port.DedupePending,
	})
	return id, err
}

// enqueueGraph enqueues graph maintenance after a removal (payload {}, DedupePendingOrRunning) in tx.
func enqueueGraph(ctx context.Context, tx port.Tx, b port.BankRef) error {
	_, _, err := tx.Queue().Enqueue(ctx, port.Job{
		Bank: b, Kind: domain.OperationKindGraphMaintenance, SerialKey: graphKey,
		Payload: emptyPayload, Dedupe: port.DedupePendingOrRunning,
	})
	return err
}

// rederiveKey is the serial key of a unit's rederive: its document's key, so it never overlaps a retain of
// that document. A unit without a document (not produced by retain) falls back to its own key.
func rederiveKey(u domain.Unit) string {
	if u.DocumentID == "" {
		return "unit:" + string(u.ID)
	}
	return "doc:" + string(u.DocumentID)
}

// enqueueRederive enqueues rederive_memory for u in tx (DedupeNone, request overrides on the job); entities
// go into the payload (nil = re-resolve the unit's current names).
func enqueueRederive(
	ctx context.Context, tx port.Tx, u domain.Unit, req UpdateMemoryRequest, entities []string,
) (port.OperationID, error) {
	payload, err := json.Marshal(RederivePayload{
		UnitID: u.ID, Entities: entities, ResolveEntities: req.ResolveEntities,
	})
	if err != nil {
		return "", fmt.Errorf("encode rederive payload: %w", err)
	}
	id, _, err := tx.Queue().Enqueue(ctx, port.Job{
		Bank: u.Bank, Kind: domain.OperationKindRederiveMemory, SerialKey: rederiveKey(u),
		Payload: payload, Overrides: req.Overrides, Dedupe: port.DedupeNone,
	})
	return id, err
}
