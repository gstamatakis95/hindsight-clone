package mentalmodel

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
)

// RefreshQueuer queues one model's automatic refresh inside the caller's transaction. It implements
// bank.RefreshQueuer and curate.RefreshQueuer (WP-13 decision 1303): invalidate queues refreshes of the
// models citing a removed unit, CreateBankFromTemplate queues first builds.
type RefreshQueuer interface {
	// QueueRefresh enqueues (or joins) a refresh of model id in tx; the caller has already called
	// tx.Seq(b). A paused model, or one that is gone (deleted meanwhile), is skipped and returns ("", nil).
	// A model never refreshed yet (no last_refreshed_at) is due now and runs in full; otherwise the
	// refresh is parked for the debounce or the interval floor.
	QueueRefresh(ctx context.Context, tx port.Tx, b port.BankRef, id port.MentalModelID) (port.OperationID, error)
}

// refreshQueuer is the RefreshQueuer. It reads only through tx: no config resolver (which opens its own
// transaction) and no change detector, so the debounce and floor come from the model's overrides, then
// the bank's own config, then the defaults (decision 1204).
type refreshQueuer struct{ clock port.Clock }

var _ RefreshQueuer = (*refreshQueuer)(nil)

// NewRefreshQueuer returns the in-transaction RefreshQueuer.
func NewRefreshQueuer(clock port.Clock) RefreshQueuer { return &refreshQueuer{clock: clock} }

// QueueRefresh applies the pause rule, then parks or runs the refresh now.
func (q *refreshQueuer) QueueRefresh(
	ctx context.Context, tx port.Tx, b port.BankRef, id port.MentalModelID,
) (port.OperationID, error) {
	m, err := tx.MentalModels().Get(ctx, b, id)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("mentalmodel: queue refresh %s: %w", id, err)
	}
	if m.AutoRefreshPaused {
		return "", nil
	}
	now := q.clock.Now()
	req := RefreshRequest{Bank: b, ID: id}
	if m.LastRefreshedAt == nil {
		req.ForceFull = true
	} else {
		cfg, err := txConfig(ctx, tx, m)
		if err != nil {
			return "", err
		}
		req.RunAt, _ = dueAt(m, cfg, now)
	}
	opID, _, err := EnqueueRefresh(ctx, tx, req, now)
	if err != nil {
		return "", fmt.Errorf("mentalmodel: queue refresh %s: %w", id, err)
	}
	return opID, nil
}

// txConfig is the bank's own config with the model's overrides on top, read in tx (tenant and server
// levels are left out; dueAt falls back to the defaults).
func txConfig(ctx context.Context, tx port.Tx, m domain.MentalModel) (domain.BankConfig, error) {
	bank, err := tx.Banks().Get(ctx, m.Bank)
	if err != nil {
		return domain.BankConfig{}, fmt.Errorf("mentalmodel: bank config: %w", err)
	}
	cfg := bank.Config
	if m.Spec.Overrides != nil && m.Spec.Overrides.MentalModels != nil {
		mc := mergeMentalModelConfig(cfg.MentalModels, m.Spec.Overrides.MentalModels)
		cfg.MentalModels = &mc
	}
	return cfg, nil
}

// mergeMentalModelConfig overlays the debounce and floor of top on base.
func mergeMentalModelConfig(base, top *domain.MentalModelConfig) domain.MentalModelConfig {
	var out domain.MentalModelConfig
	if base != nil {
		out = *base
	}
	if top.RefreshDebounceSeconds != nil {
		out.RefreshDebounceSeconds = top.RefreshDebounceSeconds
	}
	if top.MinRefreshIntervalSeconds != nil {
		out.MinRefreshIntervalSeconds = top.MinRefreshIntervalSeconds
	}
	return out
}
