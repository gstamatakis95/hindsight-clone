package main

import (
	"context"
	"fmt"
	"memd/examples/internal/exampleutil"

	pb "memd/gen/memory/v1"
)

// The conversation the assistant had with Dana last week.
const conversation = `Dana: I just moved from Lisbon to Berlin for a job at a robotics startup.
Assistant: Congratulations! What will you work on?
Dana: Motion planning for warehouse robots. I'm vegetarian, so I'm looking for good restaurants near Kreuzberg.
Assistant: Noted. Anything else I should remember?
Dana: My sister Marta visits on the first weekend of every month, and she is allergic to peanuts.`

func main() { exampleutil.Run(run) }

func run(ctx context.Context, c *exampleutil.Client) error {
	bank := exampleutil.UniqueID("chat-assistant")

	// 1. Retain: hand over the raw conversation. memd extracts facts and entities; the bank is created on the
	// first write, with the server defaults.
	exampleutil.Section("retain the conversation")
	retained, err := c.Memory.Retain(ctx, &pb.RetainRequest{
		OrgId: c.Org, BankId: bank,
		Items: []*pb.RetainItem{{
			DocumentId: "chat-2026-10-04",
			Content:    conversation,
			Context:    "A chat between the assistant and the user Dana",
			Tags:       []string{"user:dana"},
		}},
	})
	if err != nil {
		return fmt.Errorf("retain: %w", err)
	}
	doc := retained.GetDocuments()[0]
	fmt.Printf("document %s: %d memories created\n", doc.GetDocumentId(), doc.GetMemoriesCreated())
	exampleutil.PrintUsage("retain", retained.GetUsage())

	// Observations (consolidated knowledge) are written in the background; wait for them before asking.
	if err := exampleutil.WaitIdle(ctx, c, bank); err != nil {
		return err
	}

	// 2. Recall: fetch the memories relevant to the user's next message, to put into your own prompt.
	exampleutil.Section("recall")
	recalled, err := c.Memory.Recall(ctx, &pb.RecallRequest{
		OrgId: c.Org, BankId:    bank,
		Query:     "Where should I take Dana for dinner?",
		MaxTokens: 1024,
	})
	if err != nil {
		return fmt.Errorf("recall: %w", err)
	}
	for _, m := range recalled.GetResults() {
		fmt.Println(" ", exampleutil.Fact(m, 100))
	}
	if err := exampleutil.Expect(len(recalled.GetResults()) > 0, "recall to return memories"); err != nil {
		return err
	}

	// 3. Reflect: let memd's agent search the memories and write the answer itself, citing what it used.
	exampleutil.Section("reflect")
	reflected, err := c.Memory.Reflect(ctx, &pb.ReflectRequest{
		OrgId: c.Org, BankId: bank,
		Query:  "What should I keep in mind when I plan a dinner for Dana and her sister?",
	})
	if err != nil {
		return fmt.Errorf("reflect: %w", err)
	}
	fmt.Println("answer:", exampleutil.ShortText(reflected.GetText(), 300))
	fmt.Println("based on:")
	for _, m := range reflected.GetBasedOn().GetMemories() {
		fmt.Println(" ", m.GetId()[:8], exampleutil.Fact(m, 80))
	}
	exampleutil.PrintUsage("reflect", reflected.GetUsage())
	return exampleutil.Expect(len(reflected.GetBasedOn().GetMemories()) > 0, "reflect to cite memories in based_on")
}
