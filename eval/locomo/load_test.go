package locomo_test

import (
	"memd/eval/harness"
	"memd/eval/locomo"
	"reflect"
	"testing"
	"time"
)

func at(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

// TestLoadSample checks the mapping that makes runs comparable with Hindsight's: sessions as JSON turns
// in session-number order (empty ones skipped), its context and ids, the category buckets, the
// adversarial exclusion, and the query time taken from the last session key in string order.
func TestLoadSample(t *testing.T) {
	got, err := locomo.Load("testdata/sample.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx := func(n string) string {
		return "Conversation between Caroline and Melanie (session_" + n + " of conv-1)"
	}
	// String order is session_1, session_10, session_2, session_3; session_3's abbreviated month does not
	// parse with the full-month layout, so the query time is session_2's.
	qt := at(2023, 6, 27, 10, 37)
	want := []harness.Conversation{{
		ID: "conv-1",
		Sessions: []harness.Session{
			{
				ID: "conv-1_session_1", Time: at(2023, 5, 8, 13, 56), Context: ctx("1"),
				Content: `[{"speaker":"Caroline","dia_id":"D1:1","text":"Hey Mel, I went to a support group yesterday."},` +
					`{"speaker":"Melanie","dia_id":"D1:2","text":"That's great. Look at this!",` +
					`"img_url":["https://example.com/sunset.jpg"],"blip_caption":"a photo of a sunset over a lake"}]`,
			},
			{
				ID: "conv-1_session_2", Time: at(2023, 6, 27, 10, 37), Context: ctx("2"),
				Content: `[{"speaker":"Melanie","dia_id":"D2:1","text":"I ran a charity race last Saturday."}]`,
			},
			{
				ID: "conv-1_session_10", Time: at(2023, 7, 3, 19, 0), Context: ctx("10"),
				Content: `[{"speaker":"Caroline","dia_id":"D10:1","text":"I adopted a puppy!"}]`,
			},
		},
		Questions: []harness.Question{
			{
				ID: "conv-1_q0", Category: "2-temporal", Query: "When did Caroline go to the support group?",
				Answer: "7 May 2023", Kind: harness.KindLoCoMo, QueryTime: qt,
			},
			{
				ID: "conv-1_q1", Category: "1-multi-hop", Query: "In what year did Melanie run the race?",
				Answer: "2023", Kind: harness.KindLoCoMo, QueryTime: qt,
			},
			{
				ID: "conv-1_q3", Category: "4-single-hop", Query: "What did Melanie photograph?",
				Answer: "a sunset", Kind: harness.KindLoCoMo, QueryTime: qt,
			},
			{
				ID: "conv-1_q4", Category: "category-7", Query: "What is Caroline's pet?",
				Answer: "a puppy", Kind: harness.KindLoCoMo, QueryTime: qt,
			},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load mismatch:\n got %+v\nwant %+v", got, want)
	}
}
