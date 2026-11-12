package main

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
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
// Python analogy: urllib.parse.urlsplit(dsn), printing only the host part and
// path. Go returns (value, err) instead of raising, so each parse attempt is an
// `if err == nil` branch rather than a try/except.
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
		// Go's %q (used by some error messages) escapes quotes and backslashes.
		if q := strconv.Quote(p); q[1:len(q)-1] != p {
			secrets = append(secrets, q[1:len(q)-1])
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
	// Longest first, so a secret that contains another one is replaced whole.
	secrets := dsnSecrets(dsn)[1:]
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, s := range secrets {
		msg = replaceSecret(msg, s)
	}
	return msg
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// replaceSecret swaps every occurrence of secret in msg for "***". If the secret
// begins or ends with a letter/digit, a match glued to another letter/digit is
// skipped: a short password such as "p" must not turn "map" into "ma***" and
// make the error unreadable. Delimited uses (":p@", "password=p") are still hit.
func replaceSecret(msg, secret string) string {
	if secret == "" {
		return msg
	}
	checkStart, checkEnd := isWordByte(secret[0]), isWordByte(secret[len(secret)-1])
	var b strings.Builder
	pos := 0
	for {
		i := strings.Index(msg[pos:], secret)
		if i < 0 {
			break
		}
		i += pos
		end := i + len(secret)
		glued := (checkStart && i > 0 && isWordByte(msg[i-1])) ||
			(checkEnd && end < len(msg) && isWordByte(msg[end]))
		if glued {
			b.WriteString(msg[pos : i+1])
			pos = i + 1
			continue
		}
		b.WriteString(msg[pos:i])
		b.WriteString("***")
		pos = end
	}
	b.WriteString(msg[pos:])
	return b.String()
}
