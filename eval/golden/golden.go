package golden

import (
	"encoding/json"
	"fmt"
	"memd/eval/harness"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// fixture is one golden file: sessions to retain and recall checks to run against them.
type fixture struct {
	Name     string    `json:"name"`
	Sessions []session `json:"sessions"`
	Checks   []check   `json:"checks"`
}

type session struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Context string    `json:"context"`
	Content string    `json:"content"`
}

type check struct {
	ID       string   `json:"id"`
	Category string   `json:"category"`
	Query    string   `json:"query"`
	Accept   []string `json:"accept"`
}

// Load reads every *.yaml fixture in dir, in file-name order, one conversation per fixture. Fixtures are
// written in YAML's JSON-compatible flow style (decision 700) and decoded strictly: unknown fields are
// errors.
func Load(dir string) ([]harness.Conversation, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("golden: list fixtures: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("golden: no *.yaml fixtures in %s", dir)
	}
	sort.Strings(paths)
	names := map[string]string{}
	convs := make([]harness.Conversation, 0, len(paths))
	for _, p := range paths {
		c, err := loadFile(p)
		if err != nil {
			return nil, err
		}
		if prev, dup := names[c.ID]; dup {
			return nil, fmt.Errorf("golden: %s: name %q already used by %s", p, c.ID, prev)
		}
		names[c.ID] = p
		convs = append(convs, c)
	}
	return convs, nil
}

// loadFile decodes and validates one fixture.
func loadFile(path string) (harness.Conversation, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return harness.Conversation{}, fmt.Errorf("golden: %w", err)
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var fx fixture
	if err := dec.Decode(&fx); err != nil {
		return harness.Conversation{}, fmt.Errorf("golden: %s: %w", path, err)
	}
	if dec.More() {
		return harness.Conversation{}, fmt.Errorf("golden: %s: trailing content after the fixture", path)
	}
	if err := fx.validate(); err != nil {
		return harness.Conversation{}, fmt.Errorf("golden: %s: %w", path, err)
	}
	return fx.conversation(), nil
}

func (fx fixture) validate() error {
	if fx.Name == "" || len(fx.Sessions) == 0 || len(fx.Checks) == 0 {
		return fmt.Errorf("needs a name, sessions and checks")
	}
	// Session ids are document ids: a repeat would silently upsert over the earlier session.
	ids := map[string]bool{}
	for i, s := range fx.Sessions {
		if s.ID == "" || s.Content == "" {
			return fmt.Errorf("sessions[%d]: needs id and content", i)
		}
		if ids["s/"+s.ID] {
			return fmt.Errorf("sessions[%d]: duplicate id %q", i, s.ID)
		}
		ids["s/"+s.ID] = true
	}
	for i, c := range fx.Checks {
		if c.ID == "" || c.Query == "" || len(c.Accept) == 0 {
			return fmt.Errorf("checks[%d]: needs id, query and accept", i)
		}
		if ids["c/"+c.ID] {
			return fmt.Errorf("checks[%d]: duplicate id %q", i, c.ID)
		}
		ids["c/"+c.ID] = true
	}
	return nil
}

func (fx fixture) conversation() harness.Conversation {
	c := harness.Conversation{ID: fx.Name}
	for _, s := range fx.Sessions {
		c.Sessions = append(c.Sessions, harness.Session(s))
	}
	for _, ch := range fx.Checks {
		cat := ch.Category
		if cat == "" {
			cat = fx.Name
		}
		c.Questions = append(c.Questions, harness.Question{
			ID: ch.ID, Category: cat, Query: ch.Query, Answer: ch.Accept[0], Accept: ch.Accept,
		})
	}
	return c
}
