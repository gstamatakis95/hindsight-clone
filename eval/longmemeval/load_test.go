package longmemeval_test

import (
	"memd/eval/harness"
	"memd/eval/longmemeval"
	"reflect"
	"strings"
	"testing"
	"time"
)

const chatCtx = "Chat session between the user and an AI assistant."

func at(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

func TestLoadSample(t *testing.T) {
	got, err := longmemeval.Load("testdata/sample.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []harness.Conversation{
		{
			ID: "q1",
			Sessions: []harness.Session{
				{
					ID: "sess_a", Time: at(2023, 5, 10, 9, 30), Context: chatCtx,
					Content: "user: I am flying to Paris today.\nassistant: Have a great trip!",
				},
				{
					ID: "sess_b", Time: at(2023, 5, 20, 10, 0), Context: chatCtx,
					Content: "user: I just got back from Rome.\nassistant: Welcome back! How was it?",
				},
			},
			Questions: []harness.Question{{
				ID: "q1", Category: "temporal-reasoning", Query: "How many days passed between my two trips?",
				Answer: "10", Kind: harness.KindTemporal, QueryTime: at(2023, 5, 30, 23, 40),
			}},
		},
		{
			ID: "q2_abs",
			Sessions: []harness.Session{{
				ID: "sess_c", Time: at(2023, 5, 31, 18, 15), Context: chatCtx,
				Content: "user: My dog is called Rex.\nassistant: Rex is a classic name.",
			}},
			Questions: []harness.Question{{
				ID: "q2_abs", Category: "knowledge-update", Query: "What is the name of my cat?",
				Answer: "You did not mention a cat.", Kind: harness.KindAbstention, QueryTime: at(2023, 6, 1, 8, 0),
			}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// instanceJSON builds a one-instance dataset with the given extra fields.
func instanceJSON(id, qtype, fields string) string {
	return `[{"question_id":"` + id + `","question_type":"` + qtype + `","question":"q?","answer":"a",` +
		`"question_date":"2023/05/30 (Tue) 23:40",` + fields + `}]`
}

func TestParseKind(t *testing.T) {
	empty := `"haystack_session_ids":[],"haystack_dates":[],"haystack_sessions":[]`
	tests := []struct {
		id, qtype, want string
	}{
		{"a", "temporal-reasoning", harness.KindTemporal},
		{"b", "knowledge-update", harness.KindUpdate},
		{"c", "single-session-preference", harness.KindPreference},
		{"d", "multi-session", harness.KindDefault},
		{"e", "single-session-user", harness.KindDefault},
		{"f_abs", "temporal-reasoning", harness.KindAbstention},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			got, err := longmemeval.Parse(strings.NewReader(instanceJSON(tc.id, tc.qtype, empty)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if k := got[0].Questions[0].Kind; k != tc.want {
				t.Fatalf("kind = %q, want %q", k, tc.want)
			}
		})
	}
}

func TestParseMissingSessionIDs(t *testing.T) {
	fields := `"haystack_dates":["2023/05/10 (Wed) 09:30"],"haystack_sessions":[[{"role":"user","content":"hi"}]]`
	got, err := longmemeval.Parse(strings.NewReader(instanceJSON("q", "multi-session", fields)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id := got[0].Sessions[0].ID; id != "s0" {
		t.Fatalf("session id = %q, want s0", id)
	}
}

func TestParseDuplicateSessionIDsKeepsBoth(t *testing.T) {
	fields := `"haystack_session_ids":["a","a"],` +
		`"haystack_dates":["2023/05/10 (Wed) 09:30","2023/05/11 (Thu) 09:30"],` +
		`"haystack_sessions":[[{"role":"user","content":"one"}],[{"role":"user","content":"two"}]]`
	got, err := longmemeval.Parse(strings.NewReader(instanceJSON("q", "multi-session", fields)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if a, b := got[0].Sessions[0].ID, got[0].Sessions[1].ID; a == b {
		t.Fatalf("duplicate session ids %q would upsert over each other", a)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name, input string
	}{
		{"bad json", `[{`},
		{"lengths", instanceJSON("bad_len", "x",
			`"haystack_session_ids":["a","b"],"haystack_dates":["2023/05/10 (Wed) 09:30"],`+
				`"haystack_sessions":[[{"role":"user","content":"hi"}]]`)},
		{"session date", instanceJSON("bad_date", "x",
			`"haystack_session_ids":["a"],"haystack_dates":["10 May 2023"],`+
				`"haystack_sessions":[[{"role":"user","content":"hi"}]]`)},
		{"question date", strings.Replace(instanceJSON("bad_qdate", "x",
			`"haystack_dates":[],"haystack_sessions":[]`), "2023/05/30 (Tue) 23:40", "yesterday", 1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := longmemeval.Parse(strings.NewReader(tc.input))
			if err == nil {
				t.Fatal("Parse: want error")
			}
			if strings.HasPrefix(tc.name, "bad json") {
				return
			}
			if !strings.Contains(err.Error(), "bad_") {
				t.Fatalf("error %q does not name the question_id", err)
			}
		})
	}
}
