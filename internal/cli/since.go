package cli

import (
	"strconv"
	"strings"
	"time"
)

// ParseSince reads a --since value: a date (2026-10-01) in local time, or a
// duration before ref (7d, 2w, 48h).
func ParseSince(s string, ref time.Time) (time.Time, error) {
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	for suffix, days := range map[string]int{"d": 1, "w": 7} {
		if n, ok := strings.CutSuffix(s, suffix); ok {
			if count, err := strconv.Atoi(n); err == nil && count >= 0 {
				return ref.AddDate(0, 0, -count*days), nil
			}
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return ref.Add(-d), nil
	}
	return time.Time{}, Fail(ExitUsage, "--since takes a date (2026-10-01) or a duration (7d, 2w, 48h), not %q", s)
}
