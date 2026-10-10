package fake_test

import (
	"memd/internal/fake"
	"sync"
	"testing"
	"time"
)

func TestClockDefaultStart(t *testing.T) {
	c := fake.NewClock(time.Time{})
	if got := c.Now(); !got.Equal(fake.DefaultClockStart) {
		t.Errorf("Now() = %v, want %v", got, fake.DefaultClockStart)
	}
	want := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	if !fake.DefaultClockStart.Equal(want) {
		t.Errorf("DefaultClockStart = %v, want %v", fake.DefaultClockStart, want)
	}
}

func TestClockSetAdvance(t *testing.T) {
	start := time.Date(2024, time.March, 10, 12, 0, 0, 0, time.UTC)
	c := fake.NewClock(start)
	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}
	if got := c.Now(); !got.Equal(start) {
		t.Errorf("second Now() = %v, want unchanged %v", got, start)
	}
	if got, want := c.Advance(90*time.Minute), start.Add(90*time.Minute); !got.Equal(want) {
		t.Errorf("Advance = %v, want %v", got, want)
	}
	if got, want := c.Now(), start.Add(90*time.Minute); !got.Equal(want) {
		t.Errorf("Now() after Advance = %v, want %v", got, want)
	}
	if got, want := c.Advance(-2*time.Hour), start.Add(-30*time.Minute); !got.Equal(want) {
		t.Errorf("Advance(-2h) = %v, want %v", got, want)
	}
	back := time.Date(2020, time.June, 1, 0, 0, 0, 0, time.UTC)
	c.Set(back)
	if got := c.Now(); !got.Equal(back) {
		t.Errorf("Now() after Set = %v, want %v", got, back)
	}
}

func TestClockConcurrent(t *testing.T) {
	c := fake.NewClock(time.Time{})
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			c.Advance(time.Second)
			_ = c.Now()
		})
	}
	wg.Wait()
	if got, want := c.Now(), fake.DefaultClockStart.Add(50*time.Second); !got.Equal(want) {
		t.Errorf("Now() = %v, want %v", got, want)
	}
}
