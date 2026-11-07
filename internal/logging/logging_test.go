package logging

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

var levelRe = regexp.MustCompile(`\[(\w+)\]`)

// Table-driven test (like pytest.mark.parametrize): each case builds a logger
// for a LOG_LEVEL value, emits one line per level and checks which survive.
func TestLevelFiltering(t *testing.T) {
	tests := []struct {
		name  string
		level string
		want  []string
	}{
		{"debug shows all", "DEBUG", []string{"DEBUG", "INFO", "WARN", "ERROR"}},
		{"info is default for empty", "", []string{"INFO", "WARN", "ERROR"}},
		{"lowercase accepted", "warn", []string{"WARN", "ERROR"}},
		{"error only", "ERROR", []string{"ERROR"}},
		{"unknown falls back to info", "bogus", []string{"INFO", "WARN", "ERROR"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := New(&buf, tt.level)
			l.Debug("m")
			l.Info("m")
			l.Warn("m")
			l.Error("m")

			var got []string
			for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
				if line == "" {
					continue
				}
				// The timestamp has spaces so only the level matches \[(\w+)\].
				got = append(got, levelRe.FindAllStringSubmatch(line, -1)[0][1])
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("levels = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLineFormat(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, "INFO").Info("hello %d", 42)
	re := regexp.MustCompile(`^\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}\] \[INFO\] hello 42\n$`)
	if !re.MatchString(buf.String()) {
		t.Fatalf("unexpected line: %q", buf.String())
	}
}
