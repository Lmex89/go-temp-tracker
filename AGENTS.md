# Sensor Temperature Tracker — Agent Instructions

## Mandatory: Python-to-Go Docstring Style

**Every code modification MUST include explanatory comments** written for a Python backend developer learning Go. This is **required**, not optional.

### Comment Style Guidelines

When adding or modifying code, always add comments that:

1. **Compare Go concepts to Python equivalents** — e.g., "struct is like a Python dataclass", "goroutine is like threading.Thread but lighter", "interface is like Python's ABC/Protocol"
2. **Explain Go-specific syntax** — e.g., "the `&` means 'address of' — like getting a reference in Python", "`defer` is like Python's context manager (`with ...`)"
3. **Note key differences** — e.g., "Go returns (value, error) instead of raising exceptions", "maps must be created with `make()` before use", "capitalized = exported/public, lowercase = private"
4. **Use inline comments liberally** — Explain what each block does, similar to Python docstrings but inline (Go doesn't have docstrings on functions like Python)

### Example Comment Patterns

```go
// SensorReader is an *interface* (like a Python ABC or Protocol, but implicit).
// Any type with a Read() method automatically satisfies it — no "implements" keyword.

// Read() returns (map, error) — Go's way of handling errors instead of try/except.
// You MUST check if err != nil before using the result.

// defer db.Close() runs when the function returns — like Python's "with db:" context manager.
// It ensures cleanup (close connections, files, etc.) even if an error occurs.

// make(map[string]float64) creates an empty map — like {} in Python.
// But in Go, you MUST use make() for maps, slices, and channels before using them.

// range over a slice gives (index, value) — like Python's enumerate().
// We use _ for the index because Go doesn't allow unused variables.
```

### Enforcement

- **No pull request or change should be accepted without these comments**
- If you see code without comments, add them before committing
- Comments should be in English (code language) but can reference Spanish terms if helpful
- Target audience: experienced Python developer, Go beginner

---

## Essential Commands

- **Build**: `go build -o temp-tracker .`
- **Run**: `./temp-tracker` (defaults: port 8080, interval 30s, retain 8760h)
- **Custom run**: `./temp-tracker -port 9090 -interval 60 -retain 48`
- **Debug logging**: `LOG_LEVEL=DEBUG ./temp-tracker`

## Key Notes

- **Linux-only**: Requires `/sys/class/thermal/thermal_zone*` sensors
- **Runtime artifacts**: `temps.db` (SQLite) is created automatically
- **Entrypoint**: `main.go` wires up SensorReader, Store, and Poller, then starts the HTTP server
- **Dashboard**: Served from `static/index.html`
- **Logging**: Uses `logger.go` with leveled logging (DEBUG/INFO/WARN/ERROR/FATAL), controlled by `LOG_LEVEL` env var
- **API endpoints**:
  - `GET /api/temps?hours=N` — Historical readings (default 1h)
  - `GET /api/current` — Latest reading per sensor
  - `GET /` — Serves web dashboard
- **Data flow**: `sensor.go` (SensorReader) → `poller.go` (Poller) → `db.go` (Store) → `handler.go`
- **Time conversion**: `timeutil.go` provides TimeConverter (America/Merida zone)
- **Files**: `logger.go`, `main.go`, `sensor.go`, `poller.go`, `db.go`, `timeutil.go`, `handler.go`, `static/index.html`
