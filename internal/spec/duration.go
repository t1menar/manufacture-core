package spec

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Duration is a time.Duration written as "<int><unit>..." with units ms, s, m, h, d
// ("90s", "1h30m", "7d"). Days are 24 hours. The zero value means "not set".
type Duration time.Duration

var durationUnits = []struct {
	suffix string
	unit   time.Duration
}{
	// "ms" must be tried before "m" and "s".
	{"ms", time.Millisecond},
	{"s", time.Second},
	{"m", time.Minute},
	{"h", time.Hour},
	{"d", 24 * time.Hour},
}

// ParseDuration parses the spec duration format.
func ParseDuration(s string) (Duration, error) {
	if s == "" {
		return 0, errors.New("empty duration")
	}
	var total time.Duration
	for rest := s; rest != ""; {
		digits := len(rest) - len(strings.TrimLeft(rest, "0123456789"))
		if digits == 0 {
			return 0, fmt.Errorf("invalid duration %q: expected a number", s)
		}
		n, err := strconv.ParseInt(rest[:digits], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		rest = rest[digits:]
		unit, ok := time.Duration(0), false
		for _, u := range durationUnits {
			if strings.HasPrefix(rest, u.suffix) {
				unit, ok = u.unit, true
				rest = rest[len(u.suffix):]
				break
			}
		}
		if !ok {
			return 0, fmt.Errorf("invalid duration %q: unit must be one of ms, s, m, h, d", s)
		}
		if time.Duration(n) > math.MaxInt64/unit || total > math.MaxInt64-time.Duration(n)*unit {
			return 0, fmt.Errorf("invalid duration %q: too large", s)
		}
		total += time.Duration(n) * unit
	}
	return Duration(total), nil
}

// Std returns the value as time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// String formats the duration in the spec format, using the largest exact units.
func (d Duration) String() string {
	if d == 0 {
		return "0s"
	}
	var b strings.Builder
	rest := time.Duration(d)
	for i := len(durationUnits) - 1; i >= 0; i-- {
		u := durationUnits[i]
		if n := rest / u.unit; n > 0 {
			b.WriteString(strconv.FormatInt(int64(n), 10))
			b.WriteString(u.suffix)
			rest -= n * u.unit
		}
	}
	if rest > 0 { // sub-millisecond remainder cannot be written in the spec format
		b.WriteString(rest.String())
	}
	return b.String()
}

// MarshalJSON writes the duration as a string.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// UnmarshalJSON reads a duration string.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errors.New("duration must be a string such as \"30s\"")
	}
	v, err := ParseDuration(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}
