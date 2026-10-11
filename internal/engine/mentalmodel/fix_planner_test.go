package mentalmodel

import (
	"errors"
	"memd/internal/domain"
	"strings"
	"testing"
)

// TestDeltaEmptyRetryAfterAllRejectedFails checks that an empty retry after a fully rejected first reply
// is a failure that keeps the document, not a no-op success.
func TestDeltaEmptyRetryAfterAllRejectedFails(t *testing.T) {
	e := newEnv(t)
	before, _ := builtModel(e, domain.RefreshStrategySingleShot)
	e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
	e.llm.Script(domain.ModelSlotMentalModel, deltaPrompt,
		fakeJSON(opsJSON(map[string]any{"kind": "append_block", "section_id": "nope", "blocks_md": []string{"Bob."}})),
		fakeJSON(opsJSON()))
	out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{})
	if !errors.Is(err, ErrAllOpsRejected) || out.Trace.Outcome != domain.RefreshOutcomeFailed {
		t.Fatalf("err %v outcome %s", err, out.Trace.Outcome)
	}
	if n := len(e.llm.CallsFor(domain.ModelSlotMentalModel, deltaPrompt)); n != 2 {
		t.Fatalf("delta calls = %d, want 2", n)
	}
	if after := e.model("status"); after.ContentMD != before.ContentMD || after.Version != before.Version {
		t.Fatalf("document changed: %q v%d", after.ContentMD, after.Version)
	}
}

// TestDeltaRetryOnBadReferenceAfterPartialApply checks that a reply with one good op and one unknown block
// id is retried, showing only the bad op, and that the corrected reply replaces the first wholesale; an
// empty retry after a partial first reply changes nothing and succeeds.
func TestDeltaRetryOnBadReferenceAfterPartialApply(t *testing.T) {
	first := opsJSON(
		map[string]any{"kind": "append_block", "section_id": "status", "blocks_md": []string{"Bob joined."}},
		map[string]any{"kind": "remove_block", "section_id": "status", "block_id": "bnope"},
	)
	t.Run("corrected", func(t *testing.T) {
		e := newEnv(t)
		before, _ := builtModel(e, domain.RefreshStrategySingleShot)
		alice := splitMarkdown(before.ContentMD).Sections[0].Blocks[0].ID
		e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
		e.llm.Script(domain.ModelSlotMentalModel, deltaPrompt, fakeJSON(first), fakeJSON(opsJSON(
			map[string]any{"kind": "append_block", "section_id": "status", "blocks_md": []string{"Bob joined."}},
			map[string]any{"kind": "remove_block", "section_id": "status", "block_id": alice},
		)))
		out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{})
		if err != nil {
			t.Fatal(err)
		}
		calls := e.llm.CallsFor(domain.ModelSlotMentalModel, deltaPrompt)
		if len(calls) != 2 || !strings.Contains(calls[1].Structured.User, "unknown block_id: bnope") ||
			strings.Contains(calls[1].Structured.User, `"blocks_md":["Bob joined."]`) {
			t.Fatalf("want one retry listing only the bad op, got %d calls", len(calls))
		}
		m := e.model("status")
		if m.ContentMD != "## Status\n\nBob joined.\n" || len(out.Trace.DeltaOperations) != 2 {
			t.Fatalf("content %q ops %+v", m.ContentMD, out.Trace.DeltaOperations)
		}
	})
	badRetry := opsJSON(map[string]any{"kind": "remove_block", "section_id": "status", "block_id": "bstill-bad"})
	for name, retry := range map[string]string{
		"empty retry keeps attempt 1":    opsJSON(),
		"rejected retry keeps attempt 1": badRetry,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			before, _ := builtModel(e, domain.RefreshStrategySingleShot)
			bob := e.addUnit(domain.FactTypeWorld, "Bob joined", "proj")
			e.llm.Script(domain.ModelSlotMentalModel, deltaPrompt, fakeJSON(first), fakeJSON(retry))
			out, err := e.runner.Run(e.ctx, e.bank, "status", RefreshOptions{})
			if err != nil {
				t.Fatal(err)
			}
			m := e.model("status")
			if !strings.Contains(m.ContentMD, "Bob joined.") || m.Version != before.Version+1 ||
				m.LastSeqSeen < bob.Seq || len(out.Trace.DeltaOperations) != 2 {
				t.Fatalf("attempt 1's partial draft dropped: %q v%d seq %d ops %d", m.ContentMD, m.Version,
					m.LastSeqSeen, len(out.Trace.DeltaOperations))
			}
		})
	}
}
