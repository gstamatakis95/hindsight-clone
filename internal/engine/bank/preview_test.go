package bank

import (
	"encoding/json"
	"errors"
	"memd/internal/domain"
	"memd/internal/port"
	"strings"
	"testing"
)

// previewText joins every active block of every message.
func previewText(p domain.PromptPreview) string {
	var sb strings.Builder
	for _, m := range p.Messages {
		for _, b := range m.Blocks {
			if b.Active {
				sb.WriteString(b.Text)
				sb.WriteString("\n")
			}
		}
	}
	return sb.String()
}

// configBlock returns the config block for field in the system message.
func configBlock(p domain.PromptPreview, section string) (domain.PromptBlock, bool) {
	for _, b := range p.Messages[0].Blocks {
		if b.Source == domain.PromptSourceConfig && b.Section == section {
			return b, true
		}
	}
	return domain.PromptBlock{}, false
}

func TestPreview(t *testing.T) {
	base := domain.BankConfig{
		RetainMission: "track tea", ObservationsMission: "group habits", ReflectMission: "answer kindly",
		Disposition: &domain.Disposition{Skepticism: 4},
	}
	dirs := dirStub{{ID: "d1", Name: "polite", Rules: []string{"never swear"}, Active: true}}
	p := NewPromptPreviewer(cfgStub{base: base}, dirs)
	b := port.BankRef{Tenant: "t", Bank: "b"}
	cases := []struct {
		op       domain.PromptOperation
		o        domain.BankConfig
		contains []string
		schema   bool
		section  string
	}{
		{domain.PromptOperationRetain, domain.BankConfig{}, []string{"track tea", phChunk}, true, "mission"},
		{domain.PromptOperationRetain, domain.BankConfig{
			ExtractionMode: domain.ExtractionModeCustom, CustomExtractionPrompt: "only tea facts",
		}, []string{"only tea facts"}, true, "custom_prompt"},
		{domain.PromptOperationConsolidation, domain.BankConfig{}, []string{"group habits", phFact}, true, "mission"},
		{
			domain.PromptOperationReflect,
			domain.BankConfig{},
			[]string{"answer kindly", "never swear", phQuery},
			false, "directives",
		},
		{
			domain.PromptOperationSynthesize,
			domain.BankConfig{},
			[]string{"never swear", phObservation},
			false,
			"directives",
		},
		{domain.PromptOperationMentalModelDelta, domain.BankConfig{}, []string{phBlock, phFact}, true, ""},
	}
	for _, tc := range cases {
		t.Run(string(tc.op)+"/"+string(tc.o.ExtractionMode), func(t *testing.T) {
			got, err := p.Preview(t.Context(), b, tc.op, tc.o)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Messages) != 2 || got.Messages[0].Role != domain.PromptRoleSystem ||
				got.Messages[1].Role != domain.PromptRoleUser {
				t.Fatalf("messages = %+v", got.Messages)
			}
			text := previewText(got)
			for _, want := range tc.contains {
				if !strings.Contains(text, want) {
					t.Errorf("preview lacks %q", want)
				}
			}
			if strings.Contains(text, zeroNow) {
				t.Error("zero time leaked into the preview")
			}
			if tc.schema != (got.ResponseSchemaJSON != "") || (tc.schema && !json.Valid([]byte(got.ResponseSchemaJSON))) {
				t.Errorf("schema = %q, want present=%v", got.ResponseSchemaJSON, tc.schema)
			}
			if tc.section != "" {
				if blk, ok := configBlock(got, tc.section); !ok || !blk.Active {
					t.Errorf("config block %q = %+v, %v", tc.section, blk, ok)
				}
			}
		})
	}
}

func TestPreviewChunksSkipsAndUnknownOp(t *testing.T) {
	p := NewPromptPreviewer(cfgStub{}, dirStub{})
	b := port.BankRef{Tenant: "t", Bank: "b"}
	got, err := p.Preview(t.Context(), b, domain.PromptOperationRetain,
		domain.BankConfig{ExtractionMode: domain.ExtractionModeChunks})
	if err != nil || got.SkippedReason == "" || len(got.Messages) != 0 {
		t.Errorf("chunks preview = %+v %v, want skipped with no messages", got, err)
	}
	if _, err := p.Preview(t.Context(), b, domain.PromptOperationUnspecified, domain.BankConfig{}); !errors.Is(err,
		domain.ErrInvalidArgument) {
		t.Errorf("unspecified op: %v", err)
	}
	got, err = p.Preview(t.Context(), b, domain.PromptOperationReflect, domain.BankConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if blk, ok := configBlock(got, "mission"); !ok || blk.Active {
		t.Errorf("unset reflect mission block = %+v, want inactive", blk)
	}
}
