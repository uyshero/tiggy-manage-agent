package managedagents

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeAgentScheduleUsesConfiguredTimezone(t *testing.T) {
	from := time.Date(2026, time.July, 20, 0, 30, 0, 0, time.UTC)
	expression, timezone, next, err := NormalizeAgentSchedule("0 9 * * 1-5", "Asia/Shanghai", from)
	if err != nil {
		t.Fatalf("normalize schedule: %v", err)
	}
	if expression != "0 9 * * 1-5" || timezone != "Asia/Shanghai" {
		t.Fatalf("unexpected normalized values: expression=%q timezone=%q", expression, timezone)
	}
	want := time.Date(2026, time.July, 20, 1, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("expected next run %s, got %s", want, next)
	}
}

func TestNormalizeAgentScheduleRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		timezone   string
		contains   string
	}{
		{name: "cron", expression: "not a cron", timezone: "UTC", contains: "invalid cron_expression"},
		{name: "timezone", expression: "0 9 * * *", timezone: "Mars/Olympus", contains: "invalid timezone"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, err := NormalizeAgentSchedule(test.expression, test.timezone, time.Now())
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("expected %q error, got %v", test.contains, err)
			}
		})
	}
}
