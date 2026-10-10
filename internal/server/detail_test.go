package server

import (
	"context"
	"encoding/json"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
	"time"

	pb "memd/gen/memory/v1"
)

// detailModels is a stub MentalModelAdmin that records the detail it was asked for and returns model.
type detailModels struct {
	MentalModelAdmin
	// model is returned by GetMentalModel and ListMentalModels.
	model domain.MentalModel
	// got is the detail of the last read.
	got domain.Detail
}

// GetMentalModel records detail and returns the model.
func (s *detailModels) GetMentalModel(
	_ context.Context, _ port.BankRef, _ port.MentalModelID, detail domain.Detail,
) (domain.MentalModel, error) {
	s.got = detail
	return s.model, nil
}

// ListMentalModels records the detail and returns a one-model page.
func (s *detailModels) ListMentalModels(_ context.Context, req ListMentalModelsRequest) (MentalModelPage, error) {
	s.got = req.Detail
	return MentalModelPage{Items: []domain.MentalModel{s.model}}, nil
}

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

// TestMentalModelDetail checks the detail trimming of mentalModelToProto and the RPC defaults (Get: FULL,
// List: METADATA), including the detail passed to MentalModelAdmin.
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

	rpcs := []struct {
		name   string
		detail pb.Detail
		wantD  domain.Detail
		want   presence
		call   func(*mentalModelServer, pb.Detail) (*pb.MentalModel, error)
	}{
		{"get default", pb.Detail_DETAIL_UNSPECIFIED, domain.DetailFull, presence{true, true, true, true}, getModel},
		{"get metadata", pb.Detail_DETAIL_METADATA, domain.DetailMetadata, presence{}, getModel},
		{"list default", pb.Detail_DETAIL_UNSPECIFIED, domain.DetailMetadata, presence{}, listModel},
		{"list content", pb.Detail_DETAIL_CONTENT, domain.DetailContent, presence{true, true, false, false}, listModel},
	}
	for _, tc := range rpcs {
		t.Run(tc.name, func(t *testing.T) {
			stub := &detailModels{model: m}
			got, err := tc.call(newMentalModelServer(Deps{MentalModels: stub}), tc.detail)
			if err != nil {
				t.Fatal(err)
			}
			if stub.got != tc.wantD {
				t.Errorf("engine detail = %q, want %q", stub.got, tc.wantD)
			}
			if p := presenceOf(t, got); p != tc.want {
				t.Errorf("got %+v, want %+v", p, tc.want)
			}
		})
	}
}

// getModel calls GetMentalModel at detail.
func getModel(s *mentalModelServer, detail pb.Detail) (*pb.MentalModel, error) {
	ctx := WithTenant(context.Background(), "t")
	resp, err := s.GetMentalModel(ctx, &pb.GetMentalModelRequest{BankId: "b", MentalModelId: "m1", Detail: detail})
	return resp.GetMentalModel(), err
}

// listModel calls ListMentalModels at detail and returns the only model.
func listModel(s *mentalModelServer, detail pb.Detail) (*pb.MentalModel, error) {
	ctx := WithTenant(context.Background(), "t")
	resp, err := s.ListMentalModels(ctx, &pb.ListMentalModelsRequest{BankId: "b", Detail: detail})
	if err != nil {
		return nil, err
	}
	return resp.GetMentalModels()[0], nil
}
