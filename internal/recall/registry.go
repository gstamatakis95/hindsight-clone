package recall

import (
	"fmt"
	"maps"
	"memd/internal/domain"
	"slices"
	"strings"
)

// registry holds the registered arms in registration order, which is also the fusion order.
type registry struct {
	arms     []Arm
	byName   map[string]Arm
	windowed map[string]bool
}

// newRegistry registers arms. An empty or repeated name is a wiring bug and panics.
func newRegistry(arms []Arm) registry {
	r := registry{arms: arms, byName: make(map[string]Arm, len(arms)), windowed: map[string]bool{}}
	for _, a := range arms {
		name := a.Name()
		if name == "" {
			panic("recall: an arm has an empty name")
		}
		if _, dup := r.byName[name]; dup {
			panic(fmt.Sprintf("recall: arm %q is registered twice", name))
		}
		r.byName[name] = a
		if w, ok := a.(WindowArm); ok && w.MatchesWindow() {
			r.windowed[name] = true
		}
	}
	return r
}

// names returns the registered arm names in registration order.
func (r registry) names() []string {
	out := make([]string, len(r.arms))
	for i, a := range r.arms {
		out[i] = a.Name()
	}
	return out
}

// check returns domain.ErrInvalidArgument when names holds an unregistered or repeated arm name.
func (r registry) check(field string, names []string) error {
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if _, ok := r.byName[n]; !ok {
			return fmt.Errorf("%w: %s: unknown recall arm %q (registered: %s)", domain.ErrInvalidArgument, field, n,
				strings.Join(r.names(), ", "))
		}
		if seen[n] {
			return fmt.Errorf("%w: %s: recall arm %q is listed twice", domain.ErrInvalidArgument, field, n)
		}
		seen[n] = true
	}
	return nil
}

// checkRequest validates a request's arm names: the arms list and the min_scores.arms keys.
func (r registry) checkRequest(req Request) error {
	if err := r.check("arms", req.Arms); err != nil {
		return err
	}
	return r.check("min_scores.arms", slices.Sorted(maps.Keys(req.MinScores.Arms)))
}

// selected returns the arms a recall runs, in registration order: the request's arms when it names any, else the
// effective recall.arms, else every registered arm. Names in recall.arms that are not registered are ignored
// (config validation rejects them when the config is written).
func (r registry) selected(req Request, cfg domain.BankConfig) []Arm {
	names := req.Arms
	if len(names) == 0 && cfg.Recall != nil {
		names = cfg.Recall.Arms
	}
	if len(names) == 0 {
		return r.arms
	}
	var out []Arm
	for _, a := range r.arms {
		if slices.Contains(names, a.Name()) {
			out = append(out, a)
		}
	}
	return out
}
