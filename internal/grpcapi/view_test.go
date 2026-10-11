package grpcapi

import (
	"encoding/json"
	"memd/internal/domain"
	"testing"
	"time"

	pb "memd/gen/memory/v1"
)

// viewModel is a model with every view-dependent field set.
func viewModel() domain.MentalModel {
	return domain.MentalModel{
		Spec:                 domain.MentalModelSpec{ID: "m1", Name: "page"},
		Bank:                 domain.BankRef{Tenant: "t", Bank: "b"},
		ContentMD:            "# page",
		StructuredOutputJSON: json.RawMessage(`{"a":1}`),
		Version:              3,
		SourceIDs:            []domain.UnitID{"u1"},
		LastTrace:            &domain.RefreshTrace{Outcome: domain.RefreshOutcomeContentWritten, DurationMS: 5},
		CreatedAt:            time.Unix(100, 0),
	}
}

// presence reports which view-dependent fields of m are set.
type presence struct {
	// content is content_md, structured is structured_output_json, sources is source_memory_ids, trace is
	// last_trace.
	content, structured, sources, trace bool
}

// presenceOf reads the view-dependent fields of m; it also checks the always-present ones.
func presenceOf(t *testing.T, m *pb.MentalModel) presence {
	t.Helper()
	if m.GetSpec().GetId() != "m1" || m.GetVersion() != 3 {
		t.Fatalf("basic fields missing: %v", m)
	}
	return presence{
		content: m.GetContentMd() != "", structured: m.GetStructuredOutputJson() != "",
		sources: len(m.GetSourceMemoryIds()) > 0, trace: m.GetLastTrace() != nil,
	}
}

// TestMentalModelView checks the view trimming of mentalModelToProto: BASIC is the spec plus status, CONTENT
// adds content and structured output, FULL adds source ids and the last trace.
func TestMentalModelView(t *testing.T) {
	m := viewModel()
	trims := []struct {
		view domain.MentalModelView
		want presence
	}{
		{domain.MentalModelViewBasic, presence{}},
		{domain.MentalModelViewContent, presence{content: true, structured: true}},
		{domain.MentalModelViewFull, presence{content: true, structured: true, sources: true, trace: true}},
	}
	for _, tc := range trims {
		if got := presenceOf(t, mentalModelToProto(m, tc.view)); got != tc.want {
			t.Errorf("view %q: got %+v, want %+v", tc.view, got, tc.want)
		}
	}
}
