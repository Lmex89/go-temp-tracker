// Package alerts evaluates sustained rules and delivers their events.
package alerts

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Rule struct {
	ID         string
	MetricType string
	Sensor     string
	Threshold  float64
	Duration   time.Duration
}

type Config struct {
	Enabled bool
	ChatID  string
	Rules   []Rule
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var safeToken = regexp.MustCompile(`^[A-Za-z0-9:_-]+$`)

// LoadConfig validates everything before the caller opens a database.
func LoadConfig(path, publicDir, token string) (Config, error) {
	var cfg Config
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, errors.New("cannot read alert configuration")
	}
	var raw struct {
		Enabled bool   `json:"enabled"`
		ChatID  string `json:"chat_id"`
		Rules   []struct {
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
	if !raw.Enabled {
		return cfg, nil
	}
	file, err := filepath.EvalSymlinks(path)
	if err != nil {
		return cfg, errors.New("cannot resolve alert configuration")
	}
	file, err = filepath.Abs(file)
	if err != nil {
		return cfg, errors.New("cannot resolve alert configuration")
	}
	public, err := filepath.EvalSymlinks(publicDir)
	if err != nil {
		return cfg, errors.New("cannot resolve public directory")
	}
	public, err = filepath.Abs(public)
	if err != nil {
		return cfg, errors.New("cannot resolve public directory")
	}
	rel, err := filepath.Rel(public, file)
	if err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return cfg, errors.New("active alert configuration must be outside public directory")
	}
	if strings.TrimSpace(raw.ChatID) == "" {
		return cfg, errors.New("alert chat_id is required")
	}
	if token == "" || !safeToken.MatchString(token) {
		return cfg, errors.New("valid TELEGRAM_BOT_TOKEN is required")
	}
	if len(raw.Rules) == 0 {
		return cfg, errors.New("at least one alert rule is required")
	}
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
		case "cpu":
			if r.Sensor != "cpu/total" || *r.Threshold < 0 || *r.Threshold > 100 {
				return Config{}, errors.New("CPU rules require cpu/total and threshold between 0 and 100")
			}
		case "temperature":
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
	return cfg, nil
}
