package server

import (
	"memd/internal/config"
	"memd/internal/domain"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "memd/gen/memory/v1"
)

// TestEmptyRepeatedOverrideIsNotAClear sends a Recall override that sets only recall.rrf_k over the wire
// and merges it over a tenant level with list and map fields: proto3 can't tell an absent repeated field from
// an empty one, so the converter must leave them nil and the tenant's lists must survive the merge.
func TestEmptyRepeatedOverrideIsNotAClear(t *testing.T) {
	rrf := int32(7)
	wire, err := proto.Marshal(&pb.RecallRequest{
		BankId: "b", Query: "q", Overrides: &pb.BankConfig{Recall: &pb.RecallConfig{RrfK: &rrf}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var req pb.RecallRequest
	if err := proto.Unmarshal(wire, &req); err != nil {
		t.Fatal(err)
	}
	in, err := recallRequestFromProto(&req)
	if err != nil {
		t.Fatal(err)
	}
	o := in.Overrides
	if o == nil || o.Recall == nil {
		t.Fatalf("overrides lost: %+v", o)
	}
	if o.Recall.EnabledStrategies != nil || o.Recall.StrategyBoosts != nil || o.EntityLabels != nil ||
		o.ConsolidationStrategies != nil || o.Models != nil {
		t.Fatalf("absent lists decoded as explicit clears: %+v", o)
	}
	tenant := domain.BankConfig{
		Recall: &domain.RecallConfig{
			EnabledStrategies: []string{"semantic"}, StrategyBoosts: map[string]float64{"semantic": 2},
		},
		EntityLabels: []domain.EntityLabel{{Key: "k"}},
	}
	got := config.Merge(tenant, *o)
	if len(got.Recall.EnabledStrategies) != 1 || len(got.Recall.StrategyBoosts) != 1 || len(got.EntityLabels) != 1 {
		t.Fatalf("tenant lists cleared by the override: %+v", got)
	}
	if got.Recall.RRFK == nil || *got.Recall.RRFK != 7 {
		t.Fatalf("rrf_k not applied: %+v", got.Recall)
	}
}
