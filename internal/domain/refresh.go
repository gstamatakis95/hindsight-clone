package domain

import "time"

// DefaultRefreshDebounceSeconds is refresh_debounce_seconds when no level sets it.
const DefaultRefreshDebounceSeconds = 30

// Reasons RefreshDueAt gives for an automatic refresh's due time.
const (
	// RefreshReasonDebounce: parked for refresh_debounce_seconds so a burst joins one refresh.
	RefreshReasonDebounce = "debounce"
	// RefreshReasonIntervalFloor: parked until min_refresh_interval_seconds after the last refresh.
	RefreshReasonIntervalFloor = "min_refresh_interval"
)

// RefreshDueAt is when an automatic refresh of m may run: now + refresh_debounce_seconds (mc's, else
// DefaultRefreshDebounceSeconds; a negative value counts as 0), or the end of the interval floor when that
// is later. The floor is the model trigger's min_refresh_interval_seconds, else mc's; it applies only once
// m has been refreshed. reason is RefreshReasonDebounce or RefreshReasonIntervalFloor. mc may be nil.
func RefreshDueAt(m MentalModel, mc *MentalModelConfig, now time.Time) (at time.Time, reason string) {
	debounce, floor := DefaultRefreshDebounceSeconds, 0
	if mc != nil {
		if mc.RefreshDebounceSeconds != nil {
			debounce = *mc.RefreshDebounceSeconds
		}
		if mc.MinRefreshIntervalSeconds != nil {
			floor = *mc.MinRefreshIntervalSeconds
		}
	}
	if m.Spec.Trigger.MinRefreshIntervalSeconds != nil {
		floor = *m.Spec.Trigger.MinRefreshIntervalSeconds
	}
	at, reason = now.Add(time.Duration(max(debounce, 0))*time.Second), RefreshReasonDebounce
	if floor > 0 && m.LastRefreshedAt != nil {
		if end := m.LastRefreshedAt.Add(time.Duration(floor) * time.Second); end.After(at) {
			at, reason = end, RefreshReasonIntervalFloor
		}
	}
	return at, reason
}
