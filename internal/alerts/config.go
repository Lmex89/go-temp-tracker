// Package alerts evaluates sustained rules and delivers their events.
// In Python terms this package is a small module with a config loader,
// a rule evaluator and two background "workers" (Telegram and Brevo).
package alerts

import (
	"encoding/json"
	"errors"
	"math"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"sensors-temp/internal/timeutil"
)

// Metric types that alert rules can watch. They match the metric_type column
// values written by the pollers. Go has no enum; typed constants are the idiom
// (like a Python Enum whose members are plain strings).
const (
	MetricCPU         = "cpu"
	MetricTemperature = "temperature"
)

// Rule is one sustained-threshold rule (like a frozen @dataclass in Python).
type Rule struct {
	ID         string
	MetricType string
	Sensor     string
	Threshold  float64
	Duration   time.Duration
}

// Config is the validated private configuration. A zero Config (Enabled=false)
// means "alerts off", the same as Python's None-ish default.
type Config struct {
	Enabled             bool
	ChatID              string
	BrevoSenderEmail    string
	BrevoSenderName     string
	BrevoRecipientEmail string
	Rules               []Rule
}

// regexp.MustCompile panics on a bad pattern at program start, like a
// module-level re.compile() in Python that fails on import.
var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var safeToken = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
var safeAPIKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var telegramUsername = regexp.MustCompile(`^@[A-Za-z][A-Za-z0-9_]{4,31}$`)
var telegramNumber = regexp.MustCompile(`^-?[1-9][0-9]*$`)

// LoadConfig validates everything before the caller opens a database.
// Errors never include file contents or paths, so secrets cannot leak to logs.
func LoadConfig(path, publicDir, token, apiKey string) (Config, error) {
	var cfg Config
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, errors.New("cannot read alert configuration")
	}
	// Anonymous struct with json tags: the Go version of json.loads() into a
	// TypedDict. Pointers (*float64) let us tell "missing" from "zero".
	var raw struct {
		Enabled             bool   `json:"enabled"`
		ChatID              string `json:"chat_id"`
		BrevoSenderEmail    string `json:"brevo_sender_email"`
		BrevoSenderName     string `json:"brevo_sender_name"`
		BrevoRecipientEmail string `json:"brevo_recipient_email"`
		Rules               []struct {
			ID         string   `json:"id"`
			MetricType string   `json:"metric_type"`
			Sensor     string   `json:"sensor"`
			Threshold  *float64 `json:"threshold"`
			Minutes    *float64 `json:"duration_minutes"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return cfg, errors.New("invalid alert configuration JSON")
	}
	if err := checkOutsidePublic(path, publicDir); err != nil {
		return cfg, err
	}
	if !raw.Enabled {
		return cfg, nil
	}
	// Fail at startup instead of running with alerts that can never fire:
	// every reading is discarded when the suspend-aware clock is missing.
	if timeutil.BootTime() < 0 {
		return cfg, errors.New("alerts require a suspend-aware monotonic clock (Linux CLOCK_BOOTTIME)")
	}
	if !validChatID(raw.ChatID) {
		return cfg, errors.New("valid alert chat_id is required")
	}
	if token == "" || !safeToken.MatchString(token) {
		return cfg, errors.New("valid TELEGRAM_BOT_TOKEN is required")
	}
	if !safeAPIKey.MatchString(apiKey) {
		return cfg, errors.New("valid BREVO_API_KEY is required")
	}
	if !validEmail(raw.BrevoSenderEmail) || !validEmail(raw.BrevoRecipientEmail) {
		return cfg, errors.New("valid Brevo sender and recipient email addresses are required")
	}
	if strings.TrimSpace(raw.BrevoSenderName) == "" || strings.IndexFunc(raw.BrevoSenderName, unicode.IsControl) >= 0 {
		return cfg, errors.New("valid Brevo sender name is required")
	}
	if len(raw.Rules) == 0 {
		return cfg, errors.New("at least one alert rule is required")
	}
	// map[string]bool is Go's usual stand-in for a Python set().
	seen := make(map[string]bool)
	for _, r := range raw.Rules {
		if !safeID.MatchString(r.ID) || seen[r.ID] {
			return Config{}, errors.New("alert rule IDs must be unique and contain only letters, digits, underscore or hyphen")
		}
		seen[r.ID] = true
		if r.Threshold == nil || math.IsNaN(*r.Threshold) || math.IsInf(*r.Threshold, 0) {
			return Config{}, errors.New("alert threshold must be finite")
		}
		switch r.MetricType {
		case MetricCPU:
			if r.Sensor != "cpu/total" || *r.Threshold < 0 || *r.Threshold > 100 {
				return Config{}, errors.New("CPU rules require cpu/total and threshold between 0 and 100")
			}
		case MetricTemperature:
			if strings.TrimSpace(r.Sensor) == "" || *r.Threshold < -273.15 {
				return Config{}, errors.New("temperature rules require a sensor and threshold at or above absolute zero")
			}
		default:
			return Config{}, errors.New("unsupported alert metric type")
		}
		if r.Minutes == nil || math.IsNaN(*r.Minutes) || math.IsInf(*r.Minutes, 0) || *r.Minutes*float64(time.Minute) < 1 || *r.Minutes*float64(time.Minute) >= float64(math.MaxInt64) {
			return Config{}, errors.New("alert duration must be positive minutes without overflow")
		}
		cfg.Rules = append(cfg.Rules, Rule{r.ID, r.MetricType, r.Sensor, *r.Threshold, time.Duration(*r.Minutes * float64(time.Minute))})
	}
	cfg.Enabled, cfg.ChatID = true, raw.ChatID
	cfg.BrevoSenderEmail, cfg.BrevoSenderName, cfg.BrevoRecipientEmail = raw.BrevoSenderEmail, raw.BrevoSenderName, raw.BrevoRecipientEmail
	return cfg, nil
}

// checkOutsidePublic rejects a configuration that the dashboard could serve:
// the path as given, its symlink-resolved parent and its final target must
// all live outside the public directory (which may not exist yet).
// shortcut: a hard link created inside static/ is not detected, add an Nlink check if operators need it.
func checkOutsidePublic(path, publicDir string) error {
	public, err := filepath.EvalSymlinks(publicDir)
	if errors.Is(err, os.ErrNotExist) {
		public, err = publicDir, nil
	}
	if err != nil {
		return errors.New("cannot resolve public directory")
	}
	publicInput, err1 := filepath.Abs(publicDir)
	public, err2 := filepath.Abs(public)
	input, err3 := filepath.Abs(path)
	parent, err4 := filepath.EvalSymlinks(filepath.Dir(path))
	file, err5 := filepath.EvalSymlinks(path)
	// errors.Join returns nil only when every argument is nil, like all() in Python.
	if errors.Join(err1, err2, err3, err4, err5) != nil {
		return errors.New("cannot resolve alert configuration")
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return errors.New("cannot resolve alert configuration")
	}
	file, err = filepath.Abs(file)
	if err != nil {
		return errors.New("cannot resolve alert configuration")
	}
	if !outside(publicInput, input) || !outside(public, filepath.Join(parent, filepath.Base(path))) || !outside(public, file) {
		return errors.New("alert configuration must be outside public directory")
	}
	return nil
}

// outside reports whether path is neither dir itself nor below it
// (Python: not Path(path).is_relative_to(dir)).
func outside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && (rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func validChatID(s string) bool {
	if telegramUsername.MatchString(s) {
		return true
	}
	if !telegramNumber.MatchString(s) {
		return false
	}
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && strings.Contains(s, "@")
}
