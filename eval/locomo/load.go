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
	"time"
)

// dateLayouts are the LoCoMo session timestamp formats, for example "1:56 pm on 8 May, 2023".
var dateLayouts = []string{"3:04 pm on 2 January, 2006", "3:04 pm on 2 Jan, 2006"}

// sessionKey matches the conversation keys that hold a session's turns.
var sessionKey = regexp.MustCompile(`^session_(\d+)$`)

// adversarial is the LoCoMo category whose questions have no answer in the conversation; it is excluded,
// as in Hindsight's published runs.
const adversarial = 5

// categoryNames maps LoCoMo category numbers to report buckets. The dataset does not name its
// categories; these names follow the questions themselves (category 1 questions cite several turns,
// category 4 questions one turn, category 3 questions ask for inference), and the number stays in the
// name so results can be matched with evals that label the numbers differently.
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

// Load reads a LoCoMo JSON file (locomo10.json) and returns one conversation per sample.
func Load(path string) ([]harness.Conversation, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("locomo: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

// Parse decodes a LoCoMo JSON array and returns one conversation per sample: its non-empty sessions in
// session-number order and its questions, without the adversarial ones.
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
	keys, err := sessionKeys(s)
	if err != nil {
		return harness.Conversation{}, err
	}
	sessions, err := sessionsOf(s, keys)
	if err != nil {
		return harness.Conversation{}, err
	}
	queryTime := referenceTime(s, keys)
	questions := make([]harness.Question, 0, len(s.QA))
	for i, q := range s.QA {
		if q.Category == adversarial {
			continue
		}
		answer, err := answerText(q.Answer)
		if err != nil {
			return harness.Conversation{}, fmt.Errorf("qa[%d]: %w", i, err)
		}
		questions = append(questions, harness.Question{
			ID:        s.SampleID + "_q" + strconv.Itoa(i),
			Category:  categoryName(q.Category),
			Query:     q.Question,
			Answer:    answer,
			Kind:      harness.KindLoCoMo,
			QueryTime: queryTime,
		})
	}
	return harness.Conversation{ID: s.SampleID, Sessions: sessions, Questions: questions}, nil
}

// sessionKeys returns the sample's session numbers in ascending order.
func sessionKeys(s *sample) ([]int, error) {
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
	return nums, nil
}

// referenceTime is the questions' query time. Hindsight's benchmark takes the date of the last session
// key in string order ("session_9" sorts after "session_19") whose date parses with the full month name,
// which in locomo10.json is session 9 rather than the final session; it is reproduced here so the answer
// prompt's current date, and so the scores, stay comparable. Zero when no session date parses.
func referenceTime(s *sample, nums []int) time.Time {
	keys := make([]string, 0, len(nums))
	for _, n := range nums {
		keys = append(keys, "session_"+strconv.Itoa(n))
	}
	sort.Strings(keys)
	for i := len(keys) - 1; i >= 0; i-- {
		var date string
		if json.Unmarshal(s.Conversation[keys[i]+"_date_time"], &date) != nil {
			continue
		}
		if t, err := time.ParseInLocation(dateLayouts[0], date, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}

// sessionsOf builds the sample's sessions for the given session numbers, skipping empty ones.
func sessionsOf(s *sample, nums []int) ([]harness.Session, error) {
	speakerA, speakerB, err := speakers(s.Conversation)
	if err != nil {
		return nil, err
	}
	sessions := make([]harness.Session, 0, len(nums))
	for _, n := range nums {
		sess, empty, err := session(s, n)
		if err != nil {
			return nil, err
		}
		if empty {
			continue
		}
		sess.Context = fmt.Sprintf("Conversation between %s and %s (session_%d of %s)", speakerA, speakerB, n, s.SampleID)
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

// session decodes session number n of a sample, without its context. Its content is the session's turns
// as a compact JSON array, every field as in the dataset (speaker, dia_id, text, image captions).
func session(s *sample, n int) (harness.Session, bool, error) {
	key := "session_" + strconv.Itoa(n)
	var turns []json.RawMessage
	if err := json.Unmarshal(s.Conversation[key], &turns); err != nil {
		return harness.Session{}, false, fmt.Errorf("%s: decode: %w", key, err)
	}
	if len(turns) == 0 {
		return harness.Session{}, true, nil
	}
	var content bytes.Buffer
	if err := json.Compact(&content, s.Conversation[key]); err != nil {
		return harness.Session{}, false, fmt.Errorf("%s: compact: %w", key, err)
	}
	var date string
	if err := json.Unmarshal(s.Conversation[key+"_date_time"], &date); err != nil {
		return harness.Session{}, false, fmt.Errorf("%s_date_time: decode: %w", key, err)
	}
	t, err := parseDate(date)
	if err != nil {
		return harness.Session{}, false, fmt.Errorf("%s_date_time: %w", key, err)
	}
	return harness.Session{ID: s.SampleID + "_" + key, Time: t, Content: content.String()}, false, nil
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
