package plot

import (
	"math"
	"testing"
	"time"
)

func TestWorldClockBoundaries(t *testing.T) {
	for _, tc := range []struct {
		clock   string
		minutes int
		want    string
	}{
		{"1358-03-29 09:00", 49, "1358-03-29 09:49"},
		{"1358-03-29 23:55", 10, "1358-03-30 00:05"},
		{"1358-04-30 23:59", 1, "1358-05-01 00:00"},
		{"2189-12-31 23:55", 10, "2190-01-01 00:05"},
		{"2000-02-28 23:59", 1, "2000-02-29 00:00"},
		{"1900-02-28 23:59", 1, "1900-03-01 00:00"},
		{"0001-01-01 00:00", 0, "0001-01-01 00:00"},
		{"9999-12-31 23:59", 0, "9999-12-31 23:59"},
		{"第 3 日 23:59", 1, "第 4 日 00:00"},
	} {
		t.Run(tc.clock, func(t *testing.T) {
			got, err := AdvanceClock(tc.clock, tc.minutes)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			before, _ := ClockMinute(tc.clock)
			after, _ := ClockMinute(got)
			if after-before != tc.minutes {
				t.Fatal("clock advance and scheduler disagree")
			}
		})
	}
}

func TestWorldClockRejectsInvalidData(t *testing.T) {
	for _, clock := range []string{"", "1358-02-29 09:00", "2000-02-30 09:00", "0000-01-01 00:00", "10000-01-01 00:00", "1358-3-29 09:00", "1358-03-29 9:00", "1358-03-29 24:00", "1358-03-29 09:60", "1358-03-29 09:00 extra", "第 0 日 09:00", "第 1 日 24:00", "第 1 日 09:00 extra", "第 99999999999999999999 日 00:00"} {
		if _, err := ClockMinute(clock); err == nil {
			t.Errorf("accepted %q", clock)
		}
		if _, err := AdvanceClock(clock, 0); err == nil {
			t.Errorf("advanced invalid %q", clock)
		}
	}
	for _, tc := range []struct {
		clock   string
		minutes int
	}{{"9999-12-31 23:59", 1}, {"1358-03-29 09:00", -1}, {"1358-03-29 09:00", math.MaxInt}} {
		if _, err := AdvanceClock(tc.clock, tc.minutes); err == nil {
			t.Errorf("accepted %#v", tc)
		}
	}
}

func TestWorldClockIgnoresHostTimezone(t *testing.T) {
	original := time.Local
	time.Local = time.FixedZone("fixture", -11*60*60)
	defer func() { time.Local = original }()
	got, err := AdvanceClock("1358-03-29 23:55", 10)
	if err != nil || got != "1358-03-30 00:05" {
		t.Fatalf("host timezone changed clock: %s %v", got, err)
	}
}
