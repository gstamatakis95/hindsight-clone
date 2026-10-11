package bank

import (
	"encoding/json"
	"errors"
	"memd/internal/domain"
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
		Retain:       &domain.RetainConfig{Mission: "track tea"},
		Observations: &domain.ObservationConfig{Mission: "group habits"},
		Reflect:      &domain.ReflectConfig{Mission: "answer kindly", Disposition: &domain.Disposition{Skepticism: 4}},
	}
	b := domain.BankRef{Tenant: "t", Bank: "b"}
	p := newEnv(t).previewer(t, b, base,
		domain.Directive{Name: "polite", Rules: []string{"never swear"}, Active: true},
		domain.Directive{Name: "scoped", Rules: []string{"tagged rule"}, Tags: []string{"team"}, Active: true})
	cases := []struct {
		op       domain.PromptOperation
		o        domain.BankConfig
		contains []string
		schema   bool
		section  string
	}{
		{domain.PromptOperationRetain, domain.BankConfig{}, []string{"track tea", phChunk}, true, "mission"},
		{domain.PromptOperationRetain, domain.BankConfig{
			Retain: &domain.RetainConfig{ExtractionMode: domain.ExtractionModeCustom, CustomExtractionPrompt: "only tea facts"},
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
		t.Run(string(tc.op)+"/"+string(tc.o.GetRetain().ExtractionMode), func(t *testing.T) {
			got, err := p.Preview(t.Context(), b, tc.op, tc.o)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Messages) != 2 || got.Messages[0].Role != domain.PromptRoleSystem ||
				got.Messages[1].Role != domain.PromptRoleUser {
				t.Fatalf("messages = %+v", got.Messages)
			}
			checkPreviewText(t, previewText(got), tc.contains)
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

// checkPreviewText checks text holds every want, and neither the tagged directive (a preview has no request tags)
// nor the zero time.
func checkPreviewText(t *testing.T, text string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("preview lacks %q", w)
		}
	}
	for _, leak := range []string{"tagged rule", zeroNow} {
		if strings.Contains(text, leak) {
			t.Errorf("%q leaked into the preview", leak)
		}
	}
}

// TestPreviewRetainCausalLinksDefault checks that the retain preview treats an unset
// retain.extract_causal_links as retain does (on) and omits the causal section only when it is false.
func TestPreviewRetainCausalLinksDefault(t *testing.T) {
	b := domain.BankRef{Tenant: "t", Bank: "b"}
	p := newEnv(t).previewer(t, b, domain.BankConfig{})
	off := false
	for _, tc := range []struct {
		name string
		cfg  domain.BankConfig
		want bool
	}{
		{"no retain tuning", domain.BankConfig{}, true},
		{"retain tuning without the flag", domain.BankConfig{Retain: &domain.RetainConfig{}}, true},
		{"explicitly off", domain.BankConfig{Retain: &domain.RetainConfig{ExtractCausalLinks: &off}}, false},
	} {
		got, err := p.Preview(t.Context(), b, domain.PromptOperationRetain, tc.cfg)
		if err != nil {
			t.Fatal(err)
		}
		if has := strings.Contains(previewText(got), "CAUSAL RELATIONSHIPS"); has != tc.want {
			t.Errorf("%s: causal section shown = %v, want %v", tc.name, has, tc.want)
		}
	}
}

func TestPreviewChunksSkipsAndUnknownOp(t *testing.T) {
	b := domain.BankRef{Tenant: "t", Bank: "b"}
	p := newEnv(t).previewer(t, b, domain.BankConfig{})
	got, err := p.Preview(t.Context(), b, domain.PromptOperationRetain,
		domain.BankConfig{Retain: &domain.RetainConfig{ExtractionMode: domain.ExtractionModeChunks}})
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
