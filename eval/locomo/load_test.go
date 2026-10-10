package locomo_test

import (
	"memd/eval/harness"
	"memd/eval/locomo"
	"reflect"
	"strings"
	"testing"
	"time"
)

func at(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

func TestLoadSample(t *testing.T) {
	got, err := locomo.Load("testdata/sample.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx := "Conversation between Caroline and Melanie."
	last := at(2023, 7, 3, 19, 0)
	want := []harness.Conversation{{
		ID: "conv-1",
		Sessions: []harness.Session{
			{
				ID: "conv-1-session_1", Time: at(2023, 5, 8, 13, 56), Context: ctx,
				Content: "Caroline: Hey Mel, I went to a support group yesterday.\n" +
					"Melanie: That's great. Look at this! [shared an image: a photo of a sunset over a lake]",
			},
			{
				ID: "conv-1-session_2", Time: at(2023, 6, 27, 10, 37), Context: ctx,
				Content: "Melanie: I ran a charity race last Saturday.",
			},
			{
				ID: "conv-1-session_10", Time: last, Context: ctx,
				Content: "Caroline: I adopted a puppy!",
			},
		},
		Questions: []harness.Question{
			{
				ID: "conv-1-q0", Category: "temporal", Query: "When did Caroline go to the support group?",
				Answer: "7 May 2023", Kind: harness.KindTemporal, QueryTime: last,
			},
			{
				ID: "conv-1-q1", Category: "multi-hop", Query: "In what year did Melanie run the race?",
				Answer: "2023", Kind: harness.KindDefault, QueryTime: last,
			},
			{
				ID: "conv-1-q3", Category: "single-hop", Query: "What did Melanie photograph?",
				Answer: "a sunset", Kind: harness.KindDefault, QueryTime: last,
			},
			{
				ID: "conv-1-q4", Category: "category-7", Query: "What is Caroline's pet?",
				Answer: "a puppy", Kind: harness.KindDefault, QueryTime: last,
			},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := locomo.Load("testdata/does-not-exist.json"); err == nil {
		t.Fatal("Load of a missing file: want error")
	}
}

func TestParseDateLayouts(t *testing.T) {
	tests := []struct {
		in   string
		want time.Time
	}{
		{"1:56 pm on 8 May, 2023", at(2023, 5, 8, 13, 56)},
		{"12:09 am on 13 September, 2023", at(2023, 9, 13, 0, 9)},
		{"9:55 am on 22 Oct, 2023", at(2023, 10, 22, 9, 55)},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			in := `[{"sample_id":"s","conversation":{"speaker_a":"A","speaker_b":"B",` +
				`"session_1":[],"session_1_date_time":"` + tc.in + `"},"qa":[]}]`
			got, err := locomo.Parse(strings.NewReader(in))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if tm := got[0].Sessions[0].Time; !tm.Equal(tc.want) {
				t.Fatalf("time = %v, want %v", tm, tc.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name, conv string
	}{
		{"bad date", `"speaker_a":"A","speaker_b":"B","session_1":[],"session_1_date_time":"May 8"`},
		{"missing date", `"speaker_a":"A","speaker_b":"B","session_1":[]`},
		{"bad session", `"speaker_a":"A","speaker_b":"B","session_1":{},"session_1_date_time":"1:56 pm on 8 May, 2023"`},
		{"missing speaker", `"speaker_a":"A","session_1":[],"session_1_date_time":"1:56 pm on 8 May, 2023"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := `[{"sample_id":"bad-sample","conversation":{` + tc.conv + `},"qa":[]}]`
			_, err := locomo.Parse(strings.NewReader(in))
			if err == nil {
				t.Fatal("Parse: want error")
			}
			if !strings.Contains(err.Error(), "bad-sample") {
				t.Fatalf("error %q does not name the sample", err)
			}
		})
	}
}
