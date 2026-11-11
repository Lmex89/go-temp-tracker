package store

import (
	"regexp"
	"strings"
)

var (
	postgresURLPattern        = regexp.MustCompile(`(?i)postgres(?:ql)?://[^\s"'<>]+`)
	postgresCredentialPattern = regexp.MustCompile(`(?i)\b(user|password|passfile)\s*=\s*('[^']*'|"[^"]*"|[^\s]+)`)
	postgresUserErrorPattern  = regexp.MustCompile(`(?i)(\bfor user\s+)("[^"]*"|'[^']*'|[^\s]+)`)
)

// SafeDatabaseError keeps the database cause while redacting connection details.
func SafeDatabaseError(err error, connectionDetails ...string) string {
	message := err.Error()
	for _, detail := range connectionDetails {
		if detail != "" {
			message = strings.ReplaceAll(message, detail, "database connection details redacted")
		}
	}
	message = postgresURLPattern.ReplaceAllString(message, "PostgreSQL connection details redacted")
	message = postgresCredentialPattern.ReplaceAllString(message, "$1=<redacted>")
	return postgresUserErrorPattern.ReplaceAllString(message, "$1<redacted>")
}
