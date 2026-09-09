package executor

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestCallbackExpireAt(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		result  ExecuteResult
		want    time.Time
		invalid bool
	}{
		{name: "unlimited"},
		{name: "ttl", result: ExecuteResult{TTL: 20 * time.Minute}, want: now.Add(20 * time.Minute)},
		{name: "absolute", result: ExecuteResult{ExpireAt: now.Add(time.Hour)}, want: now.Add(time.Hour)},
		{name: "already_expired", result: ExecuteResult{ExpireAt: now.Add(-time.Hour)}, want: now.Add(-time.Hour)},
		{name: "negative", result: ExecuteResult{TTL: -time.Second}, invalid: true},
		{name: "both", result: ExecuteResult{TTL: time.Minute, ExpireAt: now}, invalid: true},
		{name: "max_duration", result: ExecuteResult{TTL: time.Duration(math.MaxInt64)}, want: now.Add(time.Duration(math.MaxInt64))},
		{name: "invalid_year", result: ExecuteResult{ExpireAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.result.CallbackExpireAt(now)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid expiry accepted")
				}
				return
			}
			if err != nil || !got.Equal(tc.want) {
				t.Fatalf("expiry=%v error=%v want=%v", got, err, tc.want)
			}
		})
	}
	for _, input := range []string{`{"ttl":9223372036854775808}`, `{"ttl":NaN}`, `{"ttl":1.5}`, `{"ttl":1e999}`} {
		var result ExecuteResult
		if err := json.Unmarshal([]byte(input), &result); err == nil {
			t.Fatalf("invalid TTL accepted: %s", input)
		}
	}
}
