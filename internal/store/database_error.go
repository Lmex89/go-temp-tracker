package store

import (
	"regexp"
	"strings"
)

var (
	postgresURLPattern = regexp.MustCompile(`(?i)postgres(?:ql)?://[^\s"'<>]+`)
	// Keyword/value DSN fields that identify where or as whom we connect.
	postgresKeywordPattern   = regexp.MustCompile(`(?i)\b(user|password|passfile|host|hostaddr|dbname)\s*=\s*('[^']*'|"[^"]*"|[^\s]+)`)
	postgresUserErrorPattern = regexp.MustCompile(`(?i)(\bfor user\s+)("[^"]*"|'[^']*'|[^\s]+)`)
)

// SafeDatabaseError keeps the database cause (e.g. "connection refused") but
// redacts PostgreSQL connection details: the given dsn verbatim, any
// postgres:// URL, and keyword/value fields such as host= or password=.
// Pass "" when there is no DSN (SQLite paths are not secret and stay visible).
// Python analogy: re.sub() over str(exc) before logging it.
func SafeDatabaseError(err error, dsn string) string {
	message := err.Error()
	if dsn != "" {
		message = strings.ReplaceAll(message, dsn, "database connection details redacted")
	}
	message = postgresURLPattern.ReplaceAllString(message, "PostgreSQL connection details redacted")
	message = postgresKeywordPattern.ReplaceAllString(message, "$1=<redacted>")
	return postgresUserErrorPattern.ReplaceAllString(message, "$1<redacted>")
}
