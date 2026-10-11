package grpcapi

import (
	"encoding/json"
	"memd/internal/domain"
	"testing"
	"time"

	pb "memd/gen/memory/v1"
)

// detailModel is a model with every detail-dependent field set.
func detailModel() domain.MentalModel {
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

// presence reports which detail-dependent fields of m are set.
type presence struct {
	// content is content_md, structured is structured_output_json, sources is source_ids, trace is
	// last_trace.
	content, structured, sources, trace bool
}

// presenceOf reads the detail-dependent fields of m; it also checks the always-present ones.
func presenceOf(t *testing.T, m *pb.MentalModel) presence {
	t.Helper()
	if m.GetSpec().GetId() != "m1" || m.GetVersion() != 3 || m.GetBankId() != "b" {
		t.Fatalf("metadata missing: %v", m)
	}
	return presence{
		content: m.GetContentMd() != "", structured: m.GetStructuredOutputJson() != "",
		sources: len(m.GetSourceIds()) > 0, trace: m.GetLastTrace() != nil,
	}
}

// TestMentalModelDetail checks the detail trimming of mentalModelToProto: METADATA is the spec plus status, CONTENT
// adds content and structured output, FULL adds source ids and the last trace.
func TestMentalModelDetail(t *testing.T) {
	m := detailModel()
	trims := []struct {
		detail domain.Detail
		want   presence
	}{
		{domain.DetailMetadata, presence{}},
		{domain.DetailContent, presence{content: true, structured: true}},
		{domain.DetailFull, presence{content: true, structured: true, sources: true, trace: true}},
	}
	for _, tc := range trims {
		if got := presenceOf(t, mentalModelToProto(m, tc.detail)); got != tc.want {
			t.Errorf("detail %q: got %+v, want %+v", tc.detail, got, tc.want)
		}
	}
}
