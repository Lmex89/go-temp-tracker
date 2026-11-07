package sensor

import (
	"os"
	"path/filepath"
	"testing"
)

// write is a tiny helper: create parent dirs and write a file.
// In Python: Path(p).parent.mkdir(parents=True); Path(p).write_text(s)
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRead_HwmonLabelAndFallbackName(t *testing.T) {
	root := t.TempDir()
	hw := filepath.Join(root, "class/hwmon/hwmon0")
	write(t, filepath.Join(hw, "name"), "coretemp\n")
	write(t, filepath.Join(hw, "temp1_input"), "45000\n")
	write(t, filepath.Join(hw, "temp1_label"), "Core 0\n")
	write(t, filepath.Join(hw, "temp2_input"), "50500\n")

	got := NewLinuxThermalSensorAt(root).Read()

	want := map[string]float64{"coretemp/Core 0": 45.0, "coretemp/temp2_input": 50.5}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestRead_FallsBackToThermalZones(t *testing.T) {
	root := t.TempDir()
	zone := filepath.Join(root, "class/thermal/thermal_zone0")
	write(t, filepath.Join(zone, "temp"), "42000\n")
	write(t, filepath.Join(zone, "type"), "x86_pkg_temp\n")

	got := NewLinuxThermalSensorAt(root).Read()

	if got["x86_pkg_temp"] != 42.0 || len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}
