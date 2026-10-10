package retain

import (
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"testing"
	"time"
)

// seedUnits inserts n facts that are all similar and all said within an hour, and returns them.
func (e *env) seedUnits(prefix string, n int, at time.Time) []domain.Unit {
	e.t.Helper()
	us := make([]domain.Unit, n)
	for i := range us {
		text := fmt.Sprintf("alice likes green tea %s%d", prefix, i)
		when := at.Add(time.Duration(i) * time.Minute)
		us[i] = domain.Unit{
			ID: port.UnitID(fmt.Sprintf("%s%d", prefix, i)), Bank: testBank, Type: domain.FactTypeWorld,
			Text: text, Embedding: e.emb.Vector(text), MentionedAt: &when,
		}
	}
	e.inTx(func(tx port.Tx) error {
		seq, err := tx.Seq(e.ctx, testBank)
		if err != nil {
			return err
		}
		for i := range us {
			us[i].Seq = seq
		}
		return tx.Units().Insert(e.ctx, us)
	})
	return us
}

// linkCounts returns how many links of kind touch each unit.
func (e *env) linkCounts(ids []port.UnitID, kind domain.LinkKind) map[port.UnitID]int {
	e.t.Helper()
	out := map[port.UnitID]int{}
	e.inTx(func(tx port.Tx) error {
		for _, id := range ids {
			n, err := tx.Links().Count(e.ctx, id, kind)
			if err != nil {
				return err
			}
			out[id] = n
		}
		return nil
	})
	return out
}

// TestLinkerCapsHold: no unit, old or new, ends up with more temporal or semantic neighbours than its cap,
// across two linking rounds; links are symmetric, and causal links follow the cause map.
func TestLinkerCapsHold(t *testing.T) {
	e := newEnv(t, 0)
	at := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	capN := 2
	cfg := domain.BankConfig{Retain: &domain.RetainTuning{
		TemporalLinkCap: &capN, SemanticLinkK: &capN, SemanticLinkMinSimilarity: ptr(0.1),
	}}
	var all []port.UnitID
	for round, prefix := range []string{"a", "b"} {
		us := e.seedUnits(prefix, 6, at)
		causes := CauseMap{}
		if round == 0 {
			causes[us[1].ID] = []port.UnitID{us[0].ID}
		}
		e.inTx(func(tx port.Tx) error {
			return NewLinker().LinkWith(e.ctx, tx, testBank, us, causes, cfg)
		})
		for _, u := range us {
			all = append(all, u.ID)
		}
		for _, kind := range []domain.LinkKind{domain.LinkSemantic, domain.LinkTemporal} {
			total := 0
			for id, n := range e.linkCounts(all, kind) {
				if n%2 != 0 || n/2 > capN {
					t.Errorf("round %d: %s has %d %s links, want an even count of at most %d pairs", round, id, n, kind, capN)
				}
				total += n
			}
			if total == 0 {
				t.Errorf("round %d: no %s links at all", round, kind)
			}
		}
	}
	n := e.linkCounts([]port.UnitID{"a0", "a1", "a2"}, domain.LinkCausal)
	if n["a0"] != 1 || n["a1"] != 1 || n["a2"] != 0 {
		t.Errorf("causal counts = %v, want a0 -> a1 only", n)
	}
	hits, err := e.store.Read().ExpandGraph(e.ctx, port.GraphQuery{
		Bank: testBank, Seeds: []port.UnitID{"a1"}, MaxHops: 1, Kinds: []domain.LinkKind{domain.LinkCausal}, Limit: 10,
	})
	if err != nil || len(hits) != 0 {
		t.Errorf("causal edge runs effect -> cause: %v %v", hits, err)
	}
}

// TestLinkerTimelessAndBankConfig: timeless units get no temporal links; Link reads the bank's own tuning.
func TestLinkerTimelessAndBankConfig(t *testing.T) {
	e := newEnv(t, 0)
	one := 1
	e.inTx(func(tx port.Tx) error {
		return tx.Banks().SetConfig(e.ctx, testBank, domain.BankConfig{Retain: &domain.RetainTuning{TemporalLinkCap: &one}})
	})
	us := e.seedUnits("t", 4, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	e.inTx(func(tx port.Tx) error { return NewLinker().Link(e.ctx, tx, testBank, us, nil) })
	for id, n := range e.linkCounts([]port.UnitID{"t0", "t1", "t2", "t3"}, domain.LinkTemporal) {
		if n > 2 {
			t.Errorf("%s has %d temporal links, bank cap is 1 neighbour", id, n)
		}
	}
	timeless := e.seedUnits("x", 2, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	for i := range timeless {
		timeless[i].Timeless = true
	}
	e.inTx(func(tx port.Tx) error {
		return NewLinker().LinkWith(e.ctx, tx, testBank, timeless, nil, domain.BankConfig{})
	})
	if n := e.linkCounts([]port.UnitID{"x0", "x1"}, domain.LinkTemporal); n["x0"] != 0 || n["x1"] != 0 {
		t.Errorf("timeless units got temporal links: %v", n)
	}
}
