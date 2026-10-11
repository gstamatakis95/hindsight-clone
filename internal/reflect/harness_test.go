package reflect_test

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/ai"
	"memd/internal/clock"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/fake"
	mockai "memd/internal/mocks/ai"
	"memd/internal/prompts"
	"memd/internal/recall"
	"memd/internal/recall/arms"
	rf "memd/internal/reflect"
	"memd/internal/store"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"regexp"
	"strings"
	"testing"
	"time"
)

var bank = domain.BankRef{Tenant: "t1", Bank: "b1"}

// uid is the unit id of a readable fixture name (Postgres stores unit ids as uuid).
func uid(name string) domain.UnitID { return pgtest.ID[domain.UnitID](name) }

// uids maps uid over names.
func uids(names ...string) []domain.UnitID { return pgtest.IDs[domain.UnitID](names...) }

// sameVector is the embedding of every unit, page and query: every stored unit is a semantic hit of every recall.
var sameVector = func() []float32 {
	v := make([]float32, domain.EmbeddingDim)
	v[0] = 1
	return v
}()

// harness wires a reflect agent over a real recall engine on its own database; the model calls go to the fakes.
type harness struct {
	t     *testing.T
	store *postgres.DB
	llm   *fake.LLM
	emb   *mockai.Embedder
	clock *clock.Manual
	arms  []recall.Arm // the recall arms; nil = the built-ins
	r     *rf.Agent
}

// newHarness creates bank b1 (with bank config cfg), stores units (embedded alike, so recall returns every one of
// the requested types) and builds the agent.
func newHarness(t *testing.T, cfg domain.BankConfig, units ...domain.Unit) *harness {
	t.Helper()
	h := &harness{t: t, store: pgtest.NewStore(t), llm: fake.NewLLM(), clock: clock.NewManual(time.Time{})}
	h.emb = &mockai.Embedder{
		EmbedQueryFunc: func(context.Context, string) ([]float32, error) { return sameVector, nil },
	}
	h.tx(func(ctx context.Context, tx store.Tx) error {
		if err := tx.Banks().Create(ctx, domain.Bank{Ref: bank, Name: "Bank One", Config: cfg}); err != nil {
			return err
		}
		seq, err := tx.Seq(ctx, bank)
		if err != nil {
			return err
		}
		for i := range units {
			units[i].Seq, units[i].Embedding = seq, sameVector
		}
		if len(units) == 0 {
			return nil
		}
		return tx.Units().Insert(ctx, units)
	})
	h.build()
	return h
}

// tx runs fn in a transaction and fails the test on error.
func (h *harness) tx(fn func(ctx context.Context, tx store.Tx) error) {
	h.t.Helper()
	ctx := context.Background()
	if err := h.store.InTx(ctx, func(tx store.Tx) error { return fn(ctx, tx) }); err != nil {
		h.t.Fatal(err)
	}
}

// build (re)creates the agent over the harness's current dependencies.
func (h *harness) build() {
	server, err := config.ServerDefaults(domain.BankConfig{Models: &domain.ModelBindings{
		Default: &domain.ModelBinding{Model: "test-model"},
	}})
	if err != nil {
		h.t.Fatal(err)
	}
	tok, err := ai.NewTokenizer()
	if err != nil {
		h.t.Fatal(err)
	}
	resolver, router := config.NewResolver(h.store, server), ai.NewRouter()
	registered := h.arms
	if registered == nil {
		r := h.store.Read()
		registered = []recall.Arm{arms.NewVector(r), arms.NewText(r), arms.NewGraph(r, h.store.Graph()), arms.NewTemporal(r)}
	}
	engine := recall.New(recall.Deps{
		DB: h.store, Arms: registered, Config: resolver, Embedder: h.emb, Router: router, Clock: h.clock,
		Tokens: tok,
	})
	h.r = rf.New(rf.Deps{DB: h.store, Config: resolver, Router: router, LLM: h.llm, Clock: h.clock, Recall: engine})
}

// failEmbed makes every query embedding fail with err, so every recall fails with it.
func (h *harness) failEmbed(err error) {
	h.emb.EmbedQueryFunc = func(context.Context, string) ([]float32, error) { return nil, err }
}

// addPage stores a mental-model page (embedded like everything else).
func (h *harness) addPage(id domain.MentalModelID, name, content string) {
	h.t.Helper()
	m := domain.MentalModel{Bank: bank, Spec: domain.MentalModelSpec{ID: id, Name: name}}
	h.tx(func(ctx context.Context, tx store.Tx) error {
		if err := tx.MentalModels().Create(ctx, m); err != nil {
			return err
		}
		m.ContentMD, m.Embedding = content, sameVector
		return tx.MentalModels().Update(ctx, m)
	})
}

// addDirective stores a directive and returns its id.
func (h *harness) addDirective(d domain.Directive) domain.DirectiveID {
	h.t.Helper()
	d.Bank = bank
	var id domain.DirectiveID
	h.tx(func(ctx context.Context, tx store.Tx) error {
		var err error
		id, err = tx.Directives().Create(ctx, d)
		return err
	})
	return id
}

// turns scripts the loop's tool-calling turns (the last repeats).
func (h *harness) turns(replies ...fake.Reply) {
	h.llm.Script(domain.ModelSlotReflect, prompts.ReflectSystem, replies...)
}

// loopCalls returns the recorded loop turns.
func (h *harness) loopCalls() []fake.LLMCall {
	return h.llm.CallsFor(domain.ModelSlotReflect, prompts.ReflectSystem)
}

// reflect runs a reflect with query "q" over the bank, adjusted by mod.
func (h *harness) reflect(mod func(*rf.Request)) (rf.Result, error) {
	req := rf.Request{Bank: bank, Query: "What does Alice like?"}
	if mod != nil {
		mod(&req)
	}
	res, err := h.r.Reflect(context.Background(), req)
	if err != nil {
		return res, fmt.Errorf("test reflect: %w", err)
	}
	return res, nil
}

// fixtureName matches a quoted unit fixture name ("u1", "o404") in tool-call arguments.
var fixtureName = regexp.MustCompile(`"([uo]\d+)"`)

// calls builds a turn that calls the given tools; each call is name followed by its JSON arguments, in which quoted
// unit fixture names ("u1") stand for their ids.
func calls(nameArgs ...string) fake.Reply {
	var tc []ai.ToolCall
	for i := 0; i+1 < len(nameArgs); i += 2 {
		args := fixtureName.ReplaceAllStringFunc(nameArgs[i+1], func(q string) string {
			return `"` + string(uid(strings.Trim(q, `"`))) + `"`
		})
		tc = append(tc, ai.ToolCall{ID: fmt.Sprintf("c%d", i/2), Name: nameArgs[i], Arguments: json.RawMessage(args)})
	}
	return fake.Reply{Chat: ai.ChatResponse{Message: ai.ChatMessage{ToolCalls: tc}}}
}

// recallCall is a turn calling recall.
func recallCall() fake.Reply { return calls(rf.ToolRecall, `{"reason":"r","query":"alice"}`) }

// doneCall is a turn calling done with answer and the cited unit fixtures.
func doneCall(answer string, names ...string) fake.Reply {
	b, _ := json.Marshal(map[string]any{"answer": answer, "memory_ids": names})
	return calls(rf.ToolDone, string(b))
}

// text is a turn answering in prose with no tool call.
func text(s string) fake.Reply {
	return fake.Reply{Chat: ai.ChatResponse{Message: ai.ChatMessage{Content: s}}}
}

// withUsage sets a reply's token usage.
func withUsage(r fake.Reply, in, out int64) fake.Reply {
	r.Chat.Usage = domain.Usage{InputTokens: in, OutputTokens: out}
	return r
}

// unit is a world fact of the bank with the fixture id name.
func unit(name, text string) domain.Unit {
	return domain.Unit{ID: uid(name), Bank: bank, Type: domain.FactTypeWorld, Text: text}
}

// toolMessages returns the tool messages a recorded chat call sent.
func toolMessages(c fake.LLMCall) []string {
	var out []string
	for _, m := range c.Chat.Messages {
		if m.Role == ai.ChatRoleTool {
			out = append(out, m.Content)
		}
	}
	return out
}

// systemPrompt returns the system message of a recorded chat call.
func systemPrompt(c fake.LLMCall) string {
	for _, m := range c.Chat.Messages {
		if m.Role == ai.ChatRoleSystem {
			return m.Content
		}
	}
	return ""
}

// toolSpec returns the declaration of tool name in a recorded chat call.
func toolSpec(c fake.LLMCall, name string) (ai.ToolSpec, bool) {
	for _, s := range c.Chat.Tools {
		if s.Name == name {
			return s, true
		}
	}
	return ai.ToolSpec{}, false
}

// containsAll reports whether s contains every part.
func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

func ptr[T any](v T) *T { return &v }
