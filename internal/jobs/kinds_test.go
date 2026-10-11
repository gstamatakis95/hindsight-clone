package jobs

import (
	"memd/internal/domain"
	"memd/internal/store"
	"reflect"
	"testing"
	"time"
)

// TestCatalogueRows pins the row every Enqueue helper builds: kind, serial key, dedupe policy, payload bytes,
// overrides, parent and run_at. These are part of the stored operations rows (dedupe and serial keys decide
// which jobs join or overlap), so a change here is a behaviour change.
func TestCatalogueRows(t *testing.T) {
	b := domain.BankRef{Tenant: "t", Bank: "b"}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ov := &domain.BankConfig{RetainMission: "m"}
	must := func(j store.Job, err error) store.Job {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	ts := now.Add(-time.Hour)
	cases := []struct {
		name string
		got  store.Job
		want store.Job
	}{
		{"retain_document", must(retainDocumentJob(b, RetainPayload{
			DocumentID: "d1", ContentHash: "h", BlobKey: "k", Tags: []string{"x"}, Timestamp: &ts,
			Entities: []RetainHint{{Text: "Alice", Type: "person"}}, SkipConsolidation: true, ChunkMaxTokens: 750,
		}, ov, "parent")), store.Job{
			Bank: b, Kind: domain.OperationKindRetainDocument, SerialKey: "doc:d1",
			Payload: []byte(`{"document_id":"d1","content_hash":"h","blob_key":"k","tags":["x"],` +
				`"timestamp":"2026-01-02T02:04:05Z","entities":[{"text":"Alice","type":"person"}],` +
				`"skip_consolidation":true,"chunk_max_tokens":750}`),
			Overrides: ov, ParentID: "parent", Dedupe: store.DedupeNone,
		}},
		{"retain_batch", retainBatchJob(b, 3), store.Job{
			Bank: b, Kind: domain.OperationKindRetainBatch, Payload: []byte(`{"documents":3}`), Dedupe: store.DedupeNone,
		}},
		{"consolidate", consolidateJob(b, store.DedupePending), store.Job{
			Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate", Payload: []byte(`{}`),
			Dedupe: store.DedupePending,
		}},
		{"consolidate (tick)", consolidateJob(b, store.DedupePendingOrRunning), store.Job{
			Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate", Payload: []byte(`{}`),
			Dedupe: store.DedupePendingOrRunning,
		}},
		{"scoped consolidate: neither is automatic", must(scopedConsolidateJob(b, nil, nil)), store.Job{
			Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate", Payload: []byte(`{}`),
			Dedupe: store.DedupePending,
		}},
		{
			"scoped consolidate: scopes", must(scopedConsolidateJob(b, []domain.TagSet{{Tags: []string{"a", "b"}}}, nil)),
			store.Job{
				Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate",
				Payload: []byte(`{"scopes":[{"tags":["a","b"]}]}`), Dedupe: store.DedupeNone,
			},
		},
		{"scoped consolidate: overrides only", must(scopedConsolidateJob(b, nil, ov)), store.Job{
			Bank: b, Kind: domain.OperationKindConsolidate, SerialKey: "consolidate", Payload: []byte(`{}`),
			Overrides: ov, Dedupe: store.DedupeNone,
		}},
		{"graph_maintenance", graphJob(b), store.Job{
			Bank: b, Kind: domain.OperationKindGraphMaintenance, SerialKey: "graph", Payload: []byte(`{}`),
			Dedupe: store.DedupePendingOrRunning,
		}},
		{
			"rederive_memory of a document's unit", must(rederiveJob(domain.Unit{ID: "u1", Bank: b, DocumentID: "d1"}, ov)),
			store.Job{
				Bank: b, Kind: domain.OperationKindRederiveMemory, SerialKey: "doc:d1", Payload: []byte(`{"unit_id":"u1"}`),
				Overrides: ov, Dedupe: store.DedupeNone,
			},
		},
		{"rederive_memory of a unit without document", must(rederiveJob(domain.Unit{ID: "u2", Bank: b}, nil)), store.Job{
			Bank: b, Kind: domain.OperationKindRederiveMemory, SerialKey: "unit:u2", Payload: []byte(`{"unit_id":"u2"}`),
			Dedupe: store.DedupeNone,
		}},
		{"retag_document", must(retagJob(b, "d1")), store.Job{
			Bank: b, Kind: domain.OperationKindRetagDocument, SerialKey: "doc:d1", Payload: []byte(`{"document_id":"d1"}`),
			Dedupe: store.DedupePending,
		}},
		{"delete_bank", deleteBankJob(b, store.DedupePendingOrRunning, time.Time{}), store.Job{
			Bank: b, Kind: domain.OperationKindDeleteBank, SerialKey: "bank", Payload: []byte(`{}`),
			Dedupe: store.DedupePendingOrRunning,
		}},
		{"delete_bank continuation", deleteBankJob(b, store.DedupePending, now), store.Job{
			Bank: b, Kind: domain.OperationKindDeleteBank, SerialKey: "bank", Payload: []byte(`{}`), RunAt: now,
			Dedupe: store.DedupePending,
		}},
		{
			"refresh (automatic, parked)", must(refreshJob(RefreshRequest{Bank: b, ID: "m1", RunAt: now.Add(time.Minute)}, now)),
			store.Job{
				Bank: b, Kind: domain.OperationKindRefreshMentalModel, SerialKey: "mm:m1",
				Payload: []byte(`{"mental_model_id":"m1"}`), RunAt: now.Add(time.Minute), Dedupe: store.DedupePending,
			},
		},
		{"refresh (manual: due now)", must(refreshJob(RefreshRequest{
			Bank: b, ID: "m1", Manual: true, ForceFull: true, Strategy: domain.RefreshStrategySingleShot, Overrides: ov,
			RunAt: now.Add(time.Hour),
		}, now)), store.Job{
			Bank: b, Kind: domain.OperationKindRefreshMentalModel, SerialKey: "mm:m1",
			Payload:   []byte(`{"mental_model_id":"m1","force_full":true,"strategy":"single_shot","manual":true}`),
			Overrides: ov, RunAt: now, Dedupe: store.DedupePending,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if string(tc.got.Payload) != string(tc.want.Payload) {
				t.Errorf("payload\n got %s\nwant %s", tc.got.Payload, tc.want.Payload)
			}
			tc.got.Payload, tc.want.Payload = nil, nil
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Errorf("row\n got %+v\nwant %+v", tc.got, tc.want)
			}
		})
	}
}
