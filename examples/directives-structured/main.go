package main

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/examples/internal/exampleutil"
	"slices"

	pb "memd/gen/memory/v1"
)

// The shape the caller's code wants back from Reflect.
const incidentSchema = `{
  "type": "object",
  "properties": {
    "summary":  {"type": "string"},
    "services": {"type": "array", "items": {"type": "string"}},
    "severity": {"type": "string", "enum": ["low", "medium", "high"]}
  },
  "required": ["summary", "services", "severity"],
  "additionalProperties": false
}`

func main() {
	exampleutil.Run(func(ctx context.Context, c *exampleutil.Client) error {
		s := &scenario{c: c, bank: exampleutil.UniqueID("directives")}
		return exampleutil.Steps(ctx, s.retain, s.standingDirective, s.switchOff, s.structuredOutput)
	})
}

type scenario struct {
	c           *exampleutil.Client
	bank        string
	directiveID string
}

func (s *scenario) retain(ctx context.Context) error {
	if _, err := s.c.Memory.Retain(ctx, &pb.RetainRequest{BankId: s.bank, Items: []*pb.RetainItem{{
		DocumentId: "incident-77",
		Content: "Incident 77: the payments service returned errors for 20 minutes. " +
			"The checkout service retried too aggressively and made the outage worse. " +
			"The on-call engineer rolled back the release.",
	}}}); err != nil {
		return fmt.Errorf("retain: %w", err)
	}
	return exampleutil.WaitIdle(ctx, s.c, s.bank)
}

// ask runs the same question with the extra settings of req.
func (s *scenario) ask(ctx context.Context, req *pb.ReflectRequest) (*pb.ReflectResponse, error) {
	req.BankId, req.Query = s.bank, "What happened in incident 77?"
	resp, err := s.c.Memory.Reflect(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("reflect: %w", err)
	}
	return resp, nil
}

// standingDirective adds a rule every Reflect on the bank follows. The ids of the directives that shaped an
// answer come back in based_on, and PreviewPrompt shows how the rule reaches the model without calling it.
func (s *scenario) standingDirective(ctx context.Context) error {
	exampleutil.Section("a standing directive")
	created, err := s.c.Config.CreateDirective(ctx, &pb.CreateDirectiveRequest{BankId: s.bank, Directive: &pb.Directive{
		Name: "blameless",
		Rules: []string{
			"Describe what happened to systems, never blame a person.",
			"Mention the rollback if there was one.",
		},
		Active: true,
	}})
	if err != nil {
		return fmt.Errorf("create directive: %w", err)
	}
	s.directiveID = created.GetDirective().GetId()
	resp, err := s.ask(ctx, &pb.ReflectRequest{})
	if err != nil {
		return err
	}
	fmt.Println("applied directives:", resp.GetBasedOn().GetDirectiveIds())
	if !slices.Contains(resp.GetBasedOn().GetDirectiveIds(), s.directiveID) {
		return fmt.Errorf("expected the directive to be applied")
	}
	preview, err := s.c.Config.PreviewPrompt(ctx, &pb.PreviewPromptRequest{
		BankId: s.bank, Operation: pb.PreviewPromptRequest_PROMPT_OPERATION_REFLECT,
	})
	if err != nil {
		return fmt.Errorf("preview prompt: %w", err)
	}
	for _, msg := range preview.GetPreview().GetMessages() {
		for _, b := range msg.GetBlocks() {
			if b.GetSection() == "directives" && b.GetActive() {
				fmt.Printf("prompt block (%s): %s\n", b.GetSource(), exampleutil.ShortText(b.GetText(), 100))
			}
		}
	}
	return nil
}

// switchOff deactivates the directive (a masked field left unset clears it, so active=false needs "active" in the
// mask), then adds a rule for a single call. A request can add rules but never remove a stored one.
func (s *scenario) switchOff(ctx context.Context) error {
	exampleutil.Section("switch the directive off, add one for a single call")
	if _, err := s.c.Config.UpdateDirective(ctx, &pb.UpdateDirectiveRequest{
		BankId: s.bank, DirectiveId: s.directiveID, Directive: &pb.Directive{Active: false},
		UpdateMask: exampleutil.Paths("active"),
	}); err != nil {
		return fmt.Errorf("update directive: %w", err)
	}
	resp, err := s.ask(ctx, &pb.ReflectRequest{})
	if err != nil {
		return err
	}
	fmt.Println("applied directives:", resp.GetBasedOn().GetDirectiveIds())
	if len(resp.GetBasedOn().GetDirectiveIds()) != 0 {
		return fmt.Errorf("expected no directive while inactive")
	}
	resp, err = s.ask(ctx, &pb.ReflectRequest{
		ExtraDirectives: []*pb.Directive{{Name: "brief", Rules: []string{"Answer in one sentence."}, Active: true}},
	})
	if err != nil {
		return err
	}
	fmt.Println("one-off answer:", exampleutil.ShortText(resp.GetText(), 160))
	return nil
}

// structuredOutput makes Reflect answer in prose, then project the answer into the caller's JSON Schema.
func (s *scenario) structuredOutput(ctx context.Context) error {
	exampleutil.Section("structured output")
	resp, err := s.ask(ctx, &pb.ReflectRequest{ResponseSchemaJson: incidentSchema})
	if err != nil {
		return err
	}
	if resp.GetStructuredOutputError() != "" {
		return fmt.Errorf("structured pass failed: %s", resp.GetStructuredOutputError())
	}
	var incident struct {
		Summary  string   `json:"summary"`
		Services []string `json:"services"`
		Severity string   `json:"severity"`
	}
	if err := json.Unmarshal([]byte(resp.GetStructuredOutputJson()), &incident); err != nil {
		return fmt.Errorf("decode structured output: %w", err)
	}
	fmt.Printf("severity=%s services=%v\nsummary=%s\n",
		incident.Severity, incident.Services, exampleutil.ShortText(incident.Summary, 120))
	exampleutil.PrintUsage("structured reflect", resp.GetUsage())
	if incident.Summary == "" || incident.Severity == "" {
		return fmt.Errorf("expected a filled-in incident record")
	}
	return nil
}
