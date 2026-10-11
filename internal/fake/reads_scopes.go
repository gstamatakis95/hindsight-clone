package fake

import (
	"context"
	"fmt"
	"maps"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strings"
)

// staleScopes returns, in input order and unchanged, those of scopes (evaluated against b; their own
// BankRef is ignored) that hold an unconsolidated fact of b. Duplicates are kept.
func staleScopes(ctx context.Context, st *state, b port.BankRef, scopes []port.ScopeKey) ([]port.ScopeKey, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxErr(err)
	}
	out := []port.ScopeKey{}
	for _, s := range scopes {
		if st.countUnconsolidated(b, s.Tags) > 0 {
			out = append(out, port.ScopeKey{BankRef: s.BankRef, Tags: slices.Clone(s.Tags)})
		}
	}
	return out, nil
}

// tagCounts counts the items of b's source carrying each tag (memories: live units, observations
// included; mental models: model tags; documents: document tags), keeping tags with the byte prefix prefix
// that sort strictly after after, ordered by tag, at most limit (<= 0 = all). An unknown source is
// domain.ErrInvalidArgument.
func tagCounts(
	ctx context.Context, st *state, b port.BankRef, source domain.TagSource, prefix, after string, limit int,
) ([]domain.TagCount, error) {
	if err := liveBank(ctx, st, b); err != nil {
		return nil, err
	}
	sets, err := st.tagSets(b, source)
	if err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	for _, tags := range sets {
		for _, tag := range slices.Compact(slices.Sorted(slices.Values(tags))) { // one count per item
			if strings.HasPrefix(tag, prefix) && (after == "" || tag > after) {
				counts[tag]++
			}
		}
	}
	out := []domain.TagCount{}
	for _, tag := range slices.Sorted(maps.Keys(counts)) {
		out = append(out, domain.TagCount{Tag: tag, Count: counts[tag]})
	}
	return capped(out, limit), nil
}

// tagSets returns the tag set of every item of b's source.
func (s *state) tagSets(b port.BankRef, source domain.TagSource) ([][]string, error) {
	var sets [][]string
	switch source {
	case domain.TagSourceUnspecified, domain.TagSourceMemories:
		for _, row := range s.units {
			if row.u.Bank == b {
				sets = append(sets, row.u.Tags)
			}
		}
	case domain.TagSourceMentalModels:
		for k, row := range s.models {
			if k.bank == b {
				sets = append(sets, modelOut(row).Spec.Tags)
			}
		}
	case domain.TagSourceDocuments:
		for k, row := range s.documents {
			if k.bank == b {
				sets = append(sets, row.d.Tags)
			}
		}
	default:
		return nil, fmt.Errorf("%w: tag source %q", domain.ErrInvalidArgument, source)
	}
	return sets, nil
}

// countObservations counts the live observations of s's bank whose scope key is s's; 0 for an unknown
// scope or bank.
func countObservations(ctx context.Context, st *state, s port.ScopeKey) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, ctxErr(err)
	}
	return int(st.countObservations(s.BankRef, domain.NewScopeKey(s.BankRef, s.Tags).String())), nil
}

// countObservations counts b's live observations with scope key key.
func (s *state) countObservations(b port.BankRef, key string) int64 {
	var n int64
	for _, row := range s.units {
		if row.u.Bank == b && row.u.Type == domain.FactTypeObservation && obsScopeKey(row.u) == key {
			n++
		}
	}
	return n
}

// listScopes returns up to limit (> 0) of b's observation_scopes rows with keys strictly after after ("" =
// from the start, so the shared scope, whose key is "", is listed first), in byte order of their keys,
// with their live observation and unconsolidated fact counts.
func listScopes(ctx context.Context, st *state, b port.BankRef, after string, limit int) ([]domain.ScopeInfo, error) {
	if err := checkLimit("ListScopes", limit); err != nil {
		return nil, err
	}
	if err := liveBank(ctx, st, b); err != nil {
		return nil, err
	}
	var keys []string
	for k := range st.scopes {
		if k.bank == b && (after == "" || k.key > after) { // "" = from the start, so the shared scope "" lists
			keys = append(keys, k.key)
		}
	}
	slices.Sort(keys)
	out := []domain.ScopeInfo{}
	for _, key := range capped(keys, limit) {
		tags := slices.Clone(st.scopes[scopeRowKey{b, key}])
		if tags == nil {
			tags = []string{}
		}
		out = append(out, domain.ScopeInfo{
			Scope:               domain.TagSet{Tags: tags},
			Observations:        st.countObservations(b, key),
			UnconsolidatedUnits: st.countUnconsolidated(b, tags),
		})
	}
	return out, nil
}

// newestSeqInScope returns the highest seq of q.Bank's live units passing q's type, tag and seq filters; 0
// when none does (an unknown bank included).
func newestSeqInScope(ctx context.Context, st *state, q port.ScopeQuery) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, ctxErr(err)
	}
	var top int64
	for _, row := range st.units {
		if admit(row.u, q.Bank, q.Types, q.Tags, q.Seq) {
			top = max(top, row.u.Seq)
		}
	}
	return top, nil
}
