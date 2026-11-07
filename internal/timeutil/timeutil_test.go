package timeutil_test

import (
	"testing"

	"sensors-temp/internal/timeutil"
)

// Table-driven test, like pytest.mark.parametrize. Expected values are
// literals worked out by hand (Merida is UTC-6 all year, no DST since 2022).
func TestParseTimestampInput(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantUTC string // formatted with timeutil.DBLayout
		wantErr bool
	}{
		{"rfc3339 nano", "2024-01-15T10:30:00.000Z", "2024-01-15 10:30:00", false},
		{"rfc3339 with offset", "2024-01-15T10:30:00-06:00", "2024-01-15 16:30:00", false},
		{"db format is UTC", "2024-01-15 10:30:00", "2024-01-15 10:30:00", false},
		{"frontend T is Merida", "2024-01-15T10:30", "2024-01-15 16:30:00", false},
		{"frontend space is Merida", "2024-01-15 10:30", "2024-01-15 16:30:00", false},
		{"trims whitespace", "  2024-01-15 10:30:00  ", "2024-01-15 10:30:00", false},
		{"empty", "   ", "", true},
		{"garbage", "yesterday", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := timeutil.ParseTimestampInput(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if s := got.Format(timeutil.DBLayout); s != tc.wantUTC {
				t.Errorf("got %s, want %s", s, tc.wantUTC)
			}
			if got.Location().String() != "UTC" {
				t.Errorf("location = %s, want UTC", got.Location())
			}
		})
	}
}

func TestMeridaToLocal(t *testing.T) {
	var c timeutil.TimeConverter = timeutil.NewMeridaTimeConverter()
	if got, want := c.ToLocal("2024-01-15 10:30:00"), "2024-01-15T04:30:00-06:00"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	// Unparseable input is returned unchanged.
	if got := c.ToLocal("nope"); got != "nope" {
		t.Errorf("got %s, want nope", got)
	}
}
