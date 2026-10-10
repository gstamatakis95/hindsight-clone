package locomo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"memd/eval/harness"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// dateLayouts are the LoCoMo session timestamp formats, for example "1:56 pm on 8 May, 2023".
var dateLayouts = []string{"3:04 pm on 2 January, 2006", "3:04 pm on 2 Jan, 2006"}

// sessionKey matches the conversation keys that hold a session's turns.
var sessionKey = regexp.MustCompile(`^session_(\d+)$`)

// adversarial is the LoCoMo category with no gold answer; its questions are skipped.
const adversarial = 5

// categoryNames maps LoCoMo category numbers to report bucket names. The names follow the mapping most
// published LoCoMo evals use; it is not stated in the dataset itself, so the number stays in the name
// (decision 703).
var categoryNames = map[int]string{1: "1-multi-hop", 2: "2-temporal", 3: "3-open-domain", 4: "4-single-hop"}

// sample is one LoCoMo conversation with its questions.
type sample struct {
	SampleID     string                     `json:"sample_id"`
	Conversation map[string]json.RawMessage `json:"conversation"`
	QA           []qa                       `json:"qa"`
}

// qa is one LoCoMo question.
type qa struct {
	Question string          `json:"question"`
	Answer   json.RawMessage `json:"answer"`
	Category int             `json:"category"`
}

// turn is one utterance inside a LoCoMo session.
type turn struct {
	Speaker     string `json:"speaker"`
	Text        string `json:"text"`
	BlipCaption string `json:"blip_caption"`
}

// Load reads a LoCoMo JSON file (locomo10.json) and returns one conversation per sample.
func Load(path string) ([]harness.Conversation, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("locomo: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

// Parse decodes a LoCoMo JSON array and returns one conversation per sample: its sessions ordered by
// session number and its questions, skipping adversarial (category 5) ones.
func Parse(r io.Reader) ([]harness.Conversation, error) {
	var samples []sample
	if err := json.NewDecoder(r).Decode(&samples); err != nil {
		return nil, fmt.Errorf("locomo: decode: %w", err)
	}
	convs := make([]harness.Conversation, 0, len(samples))
	for i := range samples {
		c, err := convert(&samples[i])
		if err != nil {
			return nil, fmt.Errorf("locomo: %s: %w", samples[i].SampleID, err)
		}
		convs = append(convs, c)
	}
	return convs, nil
}

// convert maps one sample to a harness conversation.
func convert(s *sample) (harness.Conversation, error) {
	sessions, err := sessionsOf(s)
	if err != nil {
		return harness.Conversation{}, err
	}
	var queryTime time.Time
	if len(sessions) > 0 {
		queryTime = sessions[len(sessions)-1].Time
	}
	questions := make([]harness.Question, 0, len(s.QA))
	for i, q := range s.QA {
		if q.Category == adversarial {
			continue
		}
		answer, err := answerText(q.Answer)
		if err != nil {
			return harness.Conversation{}, fmt.Errorf("qa[%d]: %w", i, err)
		}
		kind := harness.KindDefault
		if q.Category == 2 {
			kind = harness.KindTemporal
		}
		questions = append(questions, harness.Question{
			ID:        s.SampleID + "-q" + strconv.Itoa(i),
			Category:  categoryName(q.Category),
			Query:     q.Question,
			Answer:    answer,
			Kind:      kind,
			QueryTime: queryTime,
		})
	}
	return harness.Conversation{ID: s.SampleID, Sessions: sessions, Questions: questions}, nil
}

// sessionsOf builds the sample's sessions ordered by session number.
func sessionsOf(s *sample) ([]harness.Session, error) {
	var nums []int
	for key := range s.Conversation {
		if m := sessionKey.FindStringSubmatch(key); m != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				return nil, fmt.Errorf("session key %q: %w", key, err)
			}
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	speakerA, speakerB, err := speakers(s.Conversation)
	if err != nil {
		return nil, err
	}
	sessCtx := "Conversation between " + speakerA + " and " + speakerB + "."
	sessions := make([]harness.Session, 0, len(nums))
	for _, n := range nums {
		sess, err := session(s, n)
		if err != nil {
			return nil, err
		}
		sess.Context = sessCtx
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

// session decodes session number n of a sample, without its context.
func session(s *sample, n int) (harness.Session, error) {
	key := "session_" + strconv.Itoa(n)
	var turns []turn
	if err := json.Unmarshal(s.Conversation[key], &turns); err != nil {
		return harness.Session{}, fmt.Errorf("%s: decode: %w", key, err)
	}
	var date string
	if err := json.Unmarshal(s.Conversation[key+"_date_time"], &date); err != nil {
		return harness.Session{}, fmt.Errorf("%s_date_time: decode: %w", key, err)
	}
	t, err := parseDate(date)
	if err != nil {
		return harness.Session{}, fmt.Errorf("%s_date_time: %w", key, err)
	}
	return harness.Session{ID: s.SampleID + "-" + key, Time: t, Content: render(turns)}, nil
}

// speakers decodes the two speaker names of a conversation.
func speakers(conv map[string]json.RawMessage) (a, b string, err error) {
	if err := json.Unmarshal(conv["speaker_a"], &a); err != nil {
		return "", "", fmt.Errorf("speaker_a: %w", err)
	}
	if err := json.Unmarshal(conv["speaker_b"], &b); err != nil {
		return "", "", fmt.Errorf("speaker_b: %w", err)
	}
	return a, b, nil
}

// render formats turns as "speaker: text" lines, noting shared images by their caption.
func render(turns []turn) string {
	lines := make([]string, 0, len(turns))
	for _, t := range turns {
		line := t.Speaker + ": " + t.Text
		if t.BlipCaption != "" {
			line += " [shared an image: " + t.BlipCaption + "]"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// parseDate parses a LoCoMo session timestamp as UTC.
func parseDate(s string) (time.Time, error) {
	var firstErr error
	for _, layout := range dateLayouts {
		t, err := time.ParseInLocation(layout, s, time.UTC)
		if err == nil {
			return t, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return time.Time{}, fmt.Errorf("parse date %q: %w", s, firstErr)
}

// categoryName maps a LoCoMo category number to its report bucket name.
func categoryName(c int) string {
	if name, ok := categoryNames[c]; ok {
		return name
	}
	return "category-" + strconv.Itoa(c)
}

// answerText renders a JSON string or number answer as text; null or absent is empty.
func answerText(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	if raw[0] != '"' {
		return string(raw), nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("decode answer: %w", err)
	}
	return s, nil
}
