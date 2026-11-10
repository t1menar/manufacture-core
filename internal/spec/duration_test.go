package spec

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"500ms":  500 * time.Millisecond,
		"30s":    30 * time.Second,
		"1h30m":  90 * time.Minute,
		"7d":     7 * 24 * time.Hour,
		"1d2h3m": 26*time.Hour + 3*time.Minute,
		"0s":     0,
	} {
		got, err := ParseDuration(in)
		if err != nil || got.Std() != want {
			t.Errorf("%q: got %v, %v; want %v", in, got.Std(), err, want)
		}
	}
	for _, bad := range []string{"", "s", "10", "1.5h", "10y", "-1s", "99999999999999999999s", "106752d"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestDurationString(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                                     "0s",
		90 * time.Minute:                      "1h30m",
		49 * time.Hour:                        "2d1h",
		1500 * time.Millisecond:               "1s500ms",
		time.Millisecond + 5*time.Microsecond: "1ms5µs",
	} {
		if got := Duration(d).String(); got != want {
			t.Errorf("%v: got %q, want %q", d, got, want)
		}
	}
}

func TestDurationJSONRoundTrip(t *testing.T) {
	var d Duration
	if err := json.Unmarshal([]byte(`"2d12h"`), &d); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(d)
	if err != nil || string(b) != `"2d12h"` {
		t.Errorf("got %s, %v", b, err)
	}
	if err := json.Unmarshal([]byte(`30`), &d); err == nil {
		t.Error("numbers must be rejected")
	}
}
