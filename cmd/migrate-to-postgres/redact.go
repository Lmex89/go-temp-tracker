package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Two ways a password can appear in a PostgreSQL DSN. Regexes (not url.Parse)
// because a malformed DSN is exactly when parsing fails and errors echo it.
var (
	urlPasswordRe     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://[^:/@?#]*:([^@]*)@`)
	keywordPasswordRe = regexp.MustCompile(`(?i)\bpassword\s*=\s*('(?:[^'\\]|\\.)*'|\S+)`)
)

// describeTarget returns "host:port/database" for log lines: enough to tell
// which server was meant, never the user info or the query string.
func describeTarget(dsn string) string {
	if cfg, err := pgx.ParseConfig(dsn); err == nil {
		return fmt.Sprintf("%s:%d/%s", cfg.Host, cfg.Port, cfg.Database)
	}
	// Unparseable: best effort for the URL form, otherwise say nothing.
	if u, err := url.Parse(dsn); err == nil && u.Host != "" {
		return u.Host + u.Path
	}
	return "<unparseable PostgreSQL target>"
}

// dsnSecrets lists every string that must never reach the output: the whole
// DSN plus the password in each spelling it may take inside error messages.
func dsnSecrets(dsn string) []string {
	secrets := []string{dsn}
	add := func(p string) {
		if p == "" {
			return
		}
		secrets = append(secrets, p)
		if dec, err := url.PathUnescape(p); err == nil && dec != p {
			secrets = append(secrets, dec)
		}
	}
	if cfg, err := pgx.ParseConfig(dsn); err == nil {
		add(cfg.Password)
	}
	if m := urlPasswordRe.FindStringSubmatch(dsn); m != nil {
		add(m[1])
	}
	for _, m := range keywordPasswordRe.FindAllStringSubmatch(dsn, -1) {
		add(m[1])
		add(strings.Trim(m[1], "'"))
	}
	return secrets
}

// redactText removes the DSN and its password from msg (an error text). The
// cause (refused, timeout, invalid port, auth failed...) is kept. Python
// analogy: str(exc).replace(secret, "***") before logging.
func redactText(msg, dsn string) string {
	// The whole DSN first (it contains the password), shown as host:port/db.
	msg = strings.ReplaceAll(msg, dsn, describeTarget(dsn))
	for _, s := range dsnSecrets(dsn)[1:] {
		msg = strings.ReplaceAll(msg, s, "***")
	}
	return msg
}
