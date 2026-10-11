package config

import (
	"errors"
	"memd/internal/domain"
	"reflect"
	"testing"
)

// TestResetPaths checks reset paths go back to unset while untouched fields (including an explicit clear)
// survive, "*" resets everything, and no paths or an unknown path is INVALID_ARGUMENT.
func TestResetPaths(t *testing.T) {
	base := func() domain.BankConfig {
		return domain.BankConfig{
			Retain:       &domain.RetainConfig{Mission: "keep", EntityLabels: []domain.EntityLabel{}},
			Observations: &domain.ObservationConfig{DedupThreshold: ptr(0.9)},
			Recall:       &domain.RecallConfig{RRFK: ptr(60), RecencyAlpha: ptr(0.2)},
			Models: &domain.ModelBindings{
				Extract: &domain.ModelBinding{Model: "small", Temperature: ptr(0.3)},
			},
		}
	}
	got, err := ResetPaths(base(), []string{"recall.rrf_k", "retain.entity_labels", "retain.mission", "models.extract"})
	if err != nil {
		t.Fatal(err)
	}
	want := base()
	want.Recall = &domain.RecallConfig{RecencyAlpha: ptr(0.2)}
	want.Retain = &domain.RetainConfig{}
	want.Models = &domain.ModelBindings{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if got, err := ResetPaths(base(), []string{ResetAll}); err != nil || !reflect.DeepEqual(got, domain.BankConfig{}) {
		t.Fatalf("reset all = %+v, %v", got, err)
	}
	for _, paths := range [][]string{{"bogus"}, nil} {
		if _, err := ResetPaths(base(), paths); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Errorf("reset %v: err = %v, want ErrInvalidArgument", paths, err)
		}
	}
}
