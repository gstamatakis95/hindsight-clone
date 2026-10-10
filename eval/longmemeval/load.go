package longmemeval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"memd/eval/harness"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// dateLayout is the LongMemEval timestamp format, for example "2023/05/30 (Tue) 23:40".
const dateLayout = "2006/01/02 (Mon) 15:04"

// sessionContext is the extractor context attached to every LongMemEval session.
const sessionContext = "Chat session between the user and an AI assistant."

// instance is one LongMemEval question with its haystack of chat sessions.
type instance struct {
	QuestionID         string          `json:"question_id"`
	QuestionType       string          `json:"question_type"`
	Question           string          `json:"question"`
	Answer             json.RawMessage `json:"answer"`
	QuestionDate       string          `json:"question_date"`
	HaystackSessionIDs []string        `json:"haystack_session_ids"`
	HaystackDates      []string        `json:"haystack_dates"`
	HaystackSessions   [][]turn        `json:"haystack_sessions"`
}

// turn is one chat message inside a haystack session.
type turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Load reads a LongMemEval JSON file (longmemeval_s, _m or _oracle) and returns one conversation per
// question.
func Load(path string) ([]harness.Conversation, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("longmemeval: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

// Parse decodes a LongMemEval JSON array and returns one conversation per question: the haystack
// sessions sorted chronologically and the single question with its judge kind.
func Parse(r io.Reader) ([]harness.Conversation, error) {
	var insts []instance
	if err := json.NewDecoder(r).Decode(&insts); err != nil {
		return nil, fmt.Errorf("longmemeval: decode: %w", err)
	}
	convs := make([]harness.Conversation, 0, len(insts))
	for i := range insts {
		c, err := convert(&insts[i])
		if err != nil {
			return nil, err
		}
		convs = append(convs, c)
	}
	return convs, nil
}

// convert maps one instance to a harness conversation.
func convert(in *instance) (harness.Conversation, error) {
	sessions, err := sessionsOf(in)
	if err != nil {
		return harness.Conversation{}, err
	}
	qt, err := parseDate(in.QuestionDate)
	if err != nil {
		return harness.Conversation{}, fmt.Errorf("longmemeval: %s: question_date: %w", in.QuestionID, err)
	}
	answer, err := answerText(in.Answer)
	if err != nil {
		return harness.Conversation{}, fmt.Errorf("longmemeval: %s: answer: %w", in.QuestionID, err)
	}
	q := harness.Question{
		ID:        in.QuestionID,
		Category:  in.QuestionType,
		Query:     in.Question,
		Answer:    answer,
		Kind:      kindOf(in.QuestionID, in.QuestionType),
		QueryTime: qt,
	}
	return harness.Conversation{ID: in.QuestionID, Sessions: sessions, Questions: []harness.Question{q}}, nil
}

// sessionsOf builds the haystack sessions of an instance, sorted by time.
func sessionsOf(in *instance) ([]harness.Session, error) {
	n := len(in.HaystackSessions)
	if len(in.HaystackDates) != n || (len(in.HaystackSessionIDs) != 0 && len(in.HaystackSessionIDs) != n) {
		return nil, fmt.Errorf("longmemeval: %s: haystack lengths differ: %d sessions, %d dates, %d ids",
			in.QuestionID, n, len(in.HaystackDates), len(in.HaystackSessionIDs))
	}
	sessions := make([]harness.Session, 0, n)
	seen := make(map[string]bool, n)
	for i, turns := range in.HaystackSessions {
		t, err := parseDate(in.HaystackDates[i])
		if err != nil {
			return nil, fmt.Errorf("longmemeval: %s: haystack_dates[%d]: %w", in.QuestionID, i, err)
		}
		id := ""
		if len(in.HaystackSessionIDs) > 0 {
			id = in.HaystackSessionIDs[i]
		}
		if id == "" || seen[id] {
			// A repeated id would make the second session upsert over the first; keep both.
			id += "s" + strconv.Itoa(i)
		}
		seen[id] = true
		sessions = append(sessions, harness.Session{
			ID:      id,
			Time:    t,
			Context: sessionContext,
			Content: render(turns),
		})
	}
	sort.SliceStable(sessions, func(a, b int) bool { return sessions[a].Time.Before(sessions[b].Time) })
	return sessions, nil
}

// render formats chat turns as "role: content" lines.
func render(turns []turn) string {
	lines := make([]string, 0, len(turns))
	for _, t := range turns {
		lines = append(lines, t.Role+": "+t.Content)
	}
	return strings.Join(lines, "\n")
}

// parseDate parses a LongMemEval timestamp as UTC.
func parseDate(s string) (time.Time, error) {
	t, err := time.ParseInLocation(dateLayout, s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse date %q: %w", s, err)
	}
	return t, nil
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

// kindOf picks the judge kind from the question id and type.
func kindOf(id, qtype string) string {
	if strings.HasSuffix(id, "_abs") {
		return harness.KindAbstention
	}
	switch qtype {
	case "temporal-reasoning":
		return harness.KindTemporal
	case "knowledge-update":
		return harness.KindUpdate
	case "single-session-preference":
		return harness.KindPreference
	default:
		return harness.KindDefault
	}
}
