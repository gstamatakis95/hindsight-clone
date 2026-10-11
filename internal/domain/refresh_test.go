package domain

import (
	"testing"
	"time"
)

// TestRefreshDueAt pins the automatic refresh's due time: the debounce (default 30 s; 0 means none, a
// negative value counts as 0), or the end of the interval floor when later, the model's floor winning over
// the config's; the floor applies only once the model has been refreshed.
func TestRefreshDueAt(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ptr := func(v int) *int { return &v }
	last := now.Add(-10 * time.Second)
	model := func(floor *int, refreshed bool) MentalModel {
		trigger := RefreshTrigger{MinRefreshIntervalSeconds: floor}
		m := MentalModel{Spec: MentalModelSpec{Trigger: trigger}}
		if refreshed {
			m.LastRefreshedAt = &last
		}
		return m
	}
	cases := []struct {
		name   string
		m      MentalModel
		mc     *MentalModelConfig
		want   time.Time
		reason string
	}{
		{"default debounce", model(nil, true), nil, now.Add(30 * time.Second), RefreshReasonDebounce},
		{
			"zero debounce", model(nil, true), &MentalModelConfig{RefreshDebounceSeconds: ptr(0)}, now,
			RefreshReasonDebounce,
		},
		{
			"negative debounce", model(nil, true), &MentalModelConfig{RefreshDebounceSeconds: ptr(-5)}, now,
			RefreshReasonDebounce,
		},
		{
			"config floor later", model(nil, true), &MentalModelConfig{MinRefreshIntervalSeconds: ptr(60)},
			last.Add(time.Minute), RefreshReasonIntervalFloor,
		},
		{
			"model floor wins", model(ptr(5), true), &MentalModelConfig{MinRefreshIntervalSeconds: ptr(600)},
			now.Add(30 * time.Second), RefreshReasonDebounce,
		},
		{"floor needs a refresh", model(ptr(600), false), nil, now.Add(30 * time.Second), RefreshReasonDebounce},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at, reason := RefreshDueAt(tc.m, tc.mc, now)
			if !at.Equal(tc.want) || reason != tc.reason {
				t.Errorf("RefreshDueAt = %v %s, want %v %s", at, reason, tc.want, tc.reason)
			}
		})
	}
}
