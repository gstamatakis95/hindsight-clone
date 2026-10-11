package worker_test

import (
	"context"
	"errors"
	"memd/internal/domain"
	"memd/internal/engine/mentalmodel"
	"memd/internal/port"
	"memd/internal/worker"
	"testing"

	mockcurate "memd/internal/mocks/engine/curate"
	mockdocs "memd/internal/mocks/engine/docs"
	mockmentalmodel "memd/internal/mocks/engine/mentalmodel"
	mockworker "memd/internal/mocks/worker"
)

// Accept (WP-20 item 3): refresh, rederive and retag report progress (stage = kind) through the job's
// checkpoint before their engine runs, and a cancelled checkpoint stops the job before the engine is called.
func TestHandlersReportProgress(t *testing.T) {
	kinds := []struct {
		kind    port.JobKind
		payload string
	}{
		{domain.OperationKindRefreshMentalModel, `{"mental_model_id":"m1"}`},
		{domain.OperationKindRederiveMemory, `{"unit_id":"u1"}`},
		{domain.OperationKindRetagDocument, `{"document_id":"d1"}`},
	}
	for _, k := range kinds {
		for _, cancelled := range []bool{false, true} {
			name := string(k.kind)
			if cancelled {
				name += "/cancelled"
			}
			t.Run(name, func(t *testing.T) {
				e := newHandlerEnv(t)
				engineCalls := 0
				var reports []domain.Progress
				cp := &mockworker.Checkpoint{ReportFunc: func(_ context.Context, p domain.Progress) error {
					if engineCalls > 0 {
						t.Error("progress reported after the engine ran")
					}
					reports = append(reports, p)
					if cancelled {
						return domain.ErrCancelled
					}
					return nil
				}}
				c := e.register(progressHandlers(&engineCalls, cp))
				err := c[k.kind](t.Context(), port.Job{ID: "op-1", Bank: hBank, Kind: k.kind, Payload: []byte(k.payload)})
				if len(reports) != 1 || reports[0].Stage != string(k.kind) || !reports[0].At.Equal(e.clock.Now()) {
					t.Fatalf("reports = %+v, want one at stage %s", reports, k.kind)
				}
				if cancelled {
					if !errors.Is(err, domain.ErrCancelled) || engineCalls != 0 {
						t.Fatalf("cancelled: err %v, engine calls %d; want ErrCancelled and no engine call", err, engineCalls)
					}
					return
				}
				if err != nil || engineCalls != 1 {
					t.Fatalf("err %v, engine calls %d; want nil and 1", err, engineCalls)
				}
			})
		}
	}
}

// progressHandlers are Handlers whose refresh, rederive and retag engines count their calls, with cp as
// every job's checkpoint.
func progressHandlers(calls *int, cp worker.Checkpoint) worker.Handlers {
	return worker.Handlers{
		Refresher: &mockmentalmodel.RefreshRunner{RunFunc: func(
			context.Context, port.BankRef, port.MentalModelID, mentalmodel.RefreshOptions,
		) (mentalmodel.RefreshOutcome, error) {
			*calls++
			return mentalmodel.RefreshOutcome{}, nil
		}},
		Rederiver:  &mockcurate.Rederiver{RederiveFunc: func(context.Context, port.Job) error { *calls++; return nil }},
		Retagger:   &mockdocs.Retagger{ApplyFunc: func(context.Context, port.Job) error { *calls++; return nil }},
		Checkpoint: func(port.Job) worker.Checkpoint { return cp },
	}
}
