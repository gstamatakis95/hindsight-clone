package reflect_test

import (
	"context"
	"encoding/json"
	"fmt"
	"memd/internal/adapter/postgres"
	"memd/internal/adapter/postgres/pgtest"
	"memd/internal/config"
	"memd/internal/domain"
	"memd/internal/engine/recall"
	rf "memd/internal/engine/reflect"
	"memd/internal/engine/routing"
	"memd/internal/fake"
	mockreflect "memd/internal/mocks/engine/reflect"
	"memd/internal/port"
	"memd/internal/prompts"
	"strings"
	"testing"
	"time"
)

var bank = port.BankRef{Tenant: "t1", Bank: "b1"}

// harness wires a reflector over fakes and mocks.
type harness struct {
	t      *testing.T
	store  *postgres.Store
	llm    *fake.LLM
	clock  *fake.Clock
	rc     *mockreflect.Recaller
	pages  *mockreflect.PageReader
	reader port.Reader
	r      rf.Reflector
}

// newHarness creates bank b1 (with bank config cfg) and a reflector whose recall returns units.
func newHarness(t *testing.T, cfg domain.BankConfig, units ...domain.Unit) *harness {
	t.Helper()
	clock := fake.NewClock(time.Time{})
	h := &harness{t: t, store: pgtest.NewStore(t), llm: fake.NewLLM(), clock: clock}
	err := h.store.InTx(context.Background(), func(tx port.Tx) error {
		return tx.Banks().Create(context.Background(), domain.Bank{Ref: bank, Name: "Bank One", Config: cfg})
	})
	if err != nil {
		t.Fatal(err)
	}
	h.rc = &mockreflect.Recaller{}
	h.rc.RecallFunc = func(_ context.Context, req recall.RecallRequest) (recall.RecallResult, error) {
		var res recall.RecallResult
		for _, u := range units {
			if typeIn(u.Type, req.Types) {
				res.Results = append(res.Results, recall.Memory{Unit: u})
			}
		}
		return res, nil
	}
	h.pages = &mockreflect.PageReader{
		SearchPagesFunc: func(context.Context, port.BankRef, string, int) ([]domain.PageHit, error) { return nil, nil },
		ReadPagesFunc: func(context.Context, port.BankRef, []port.MentalModelID) ([]domain.MentalModel, error) {
			return nil, nil
		},
	}
	h.reader = h.store.Read()
	h.build()
	return h
}

// build (re)creates the reflector over the harness's current dependencies.
func (h *harness) build() {
	server, err := config.ServerDefaults(func(k string) string {
		if k == config.EnvModel {
			return "test-model"
		}
		return ""
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.r = rf.NewReflector(rf.ReflectorDeps{
		Store: h.store, Config: config.NewResolver(h.store, nil, server), Router: routing.NewRouter(), LLM: h.llm,
		Clock: h.clock, Tools: rf.DefaultTools(h.rc, h.pages, h.reader),
	})
}

// addDirective stores a directive and returns its id.
func (h *harness) addDirective(d domain.Directive) port.DirectiveID {
	h.t.Helper()
	d.Bank = bank
	var id port.DirectiveID
	err := h.store.InTx(context.Background(), func(tx port.Tx) error {
		var err error
		id, err = tx.Directives().Upsert(context.Background(), d)
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
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
func (h *harness) reflect(mod func(*rf.ReflectRequest)) (rf.ReflectResult, error) {
	req := rf.ReflectRequest{Bank: bank, Query: "What does Alice like?"}
	if mod != nil {
		mod(&req)
	}
	res, err := h.r.Reflect(context.Background(), req)
	if err != nil {
		return res, fmt.Errorf("test reflect: %w", err)
	}
	return res, nil
}

// calls builds a turn that calls the given tools; each call is name followed by its JSON arguments.
func calls(nameArgs ...string) fake.Reply {
	var tc []port.ToolCall
	for i := 0; i+1 < len(nameArgs); i += 2 {
		tc = append(tc, port.ToolCall{
			ID: fmt.Sprintf("c%d", i/2), Name: nameArgs[i], Arguments: json.RawMessage(nameArgs[i+1]),
		})
	}
	return fake.Reply{Chat: port.ChatResponse{Message: port.ChatMessage{ToolCalls: tc}}}
}

// recallCall is a turn calling recall.
func recallCall() fake.Reply { return calls(rf.ToolRecall, `{"reason":"r","query":"alice"}`) }

// doneCall is a turn calling done with answer and cited memory ids.
func doneCall(answer string, ids ...string) fake.Reply {
	b, _ := json.Marshal(map[string]any{"answer": answer, "memory_ids": ids})
	return calls(rf.ToolDone, string(b))
}

// text is a turn answering in prose with no tool call.
func text(s string) fake.Reply {
	return fake.Reply{Chat: port.ChatResponse{Message: port.ChatMessage{Content: s}}}
}

// withUsage sets a reply's token usage.
func withUsage(r fake.Reply, in, out int64) fake.Reply {
	r.Chat.Usage = port.Usage{InputTokens: in, OutputTokens: out}
	return r
}

// unit is a world fact.
func unit(id, text string) domain.Unit {
	return domain.Unit{ID: port.UnitID(id), Bank: bank, Type: domain.FactTypeWorld, Text: text, Seq: 1}
}

// typeIn reports whether t is in types (empty = all).
func typeIn(t domain.FactType, types []domain.FactType) bool {
	if len(types) == 0 {
		return true
	}
	for _, x := range types {
		if x == t {
			return true
		}
	}
	return false
}

// toolMessages returns the tool messages a recorded chat call sent.
func toolMessages(c fake.LLMCall) []string {
	var out []string
	for _, m := range c.Chat.Messages {
		if m.Role == port.ChatRoleTool {
			out = append(out, m.Content)
		}
	}
	return out
}

// systemPrompt returns the system message of a recorded chat call.
func systemPrompt(c fake.LLMCall) string {
	for _, m := range c.Chat.Messages {
		if m.Role == port.ChatRoleSystem {
			return m.Content
		}
	}
	return ""
}

// toolSpec returns the declaration of tool name in a recorded chat call.
func toolSpec(c fake.LLMCall, name string) (port.ToolSpec, bool) {
	for _, s := range c.Chat.Tools {
		if s.Name == name {
			return s, true
		}
	}
	return port.ToolSpec{}, false
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
