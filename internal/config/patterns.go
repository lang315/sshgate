package config

import (
	"regexp"
	"strings"
)

// RedactKinds is every kind RedactPatterns reports, in the fixed order the
// AI's note line lists them.
var RedactKinds = []string{"private_key", "password", "secret", "auth_header", "url_password", "token"}

var (
	// A whole PEM private key block; with no END line it runs to the end.
	pemRe = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY(?: BLOCK)?-----.*?(?:-----END [A-Z ]*PRIVATE KEY(?: BLOCK)?-----|\z)`)
	// A credential header's name and value: "Name: v", "\"Name\": \"v\"",
	// or nginx's `Name "v"`. The value runs to a quote or the end of line.
	headerRe = regexp.MustCompile(`(?i)(^|[^a-z0-9_-])(proxy-authorization|authorization|set-cookie|cookie|x-api-key)(["']?[ \t]*:[ \t]*["']?|[ \t]+["'])([^\r\n"'` + "`" + `]*[^\s"'` + "`" + `])`)
	// scheme://user:password@host
	urlRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^\s:/@"']*:)([^\s@/"']+)@`)
	// A key that may name a secret, a separator, and a value. Which keys
	// really count is decided in code (keyKind): RE2 cannot split words.
	kvRe = regexp.MustCompile(`(?i)(["']?)(-{0,2}[a-z0-9_.-]*?(?:pass|pwd|secret|token|key|auth|credential)[a-z0-9_.-]*)(["']?)([ \t]*(?::=|==|=|:)[ \t]*|[ \t]+)("(?:[^"\\\r\n]|\\.)*"|'[^'\r\n]*'|[^\s"'` + "`" + `]+)`)
	// Bare tokens with a known prefix.
	tokenRe = regexp.MustCompile(`\b(?:(?:AKIA|ASIA)[A-Z0-9]{16}\b|(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|xox[abpr]-[A-Za-z0-9-]{10,}|(?:sk-|sk_live_|rk_live_)[A-Za-z0-9_-]{20,}|eyJ[A-Za-z0-9_-]{5,}\.eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{10,})`)
)

// nginxPass are nginx directives whose last word is "pass" but whose value
// is an upstream address, not a password.
var nginxPass = map[string]bool{"proxy_pass": true, "fastcgi_pass": true, "uwsgi_pass": true, "scgi_pass": true, "grpc_pass": true, "memcached_pass": true}

// RedactPatterns masks values that look like secrets, whether or not
// sshgate knows them, and counts the masks by kind (nil when none). Each
// is replaced by "[REDACTED:<kind>]"; after a key only the value is, so
// the key and separator stay. It is pure, and idempotent: a marker is a
// placeholder, which every rule keeps.
//
// ponytail: five linear RE2 passes over the whole output, about 0.25 s per
// MiB; pre-filter lines by trigger word if huge outputs make that matter.
func RedactPatterns(s string) (string, map[string]int) {
	var counts map[string]int
	mark := func(kind string) string {
		if counts == nil {
			counts = map[string]int{}
		}
		counts[kind]++
		return "[REDACTED:" + kind + "]"
	}
	// Order matters: a PEM block, a header, or a URL password is masked
	// before the key-value rule sees it, and the key-value rule before a
	// bare token, so each secret is counted once.
	s = pemRe.ReplaceAllStringFunc(s, func(string) string { return mark("private_key") })
	s = replaceSubmatch(headerRe, s, func(s string, m []int) string {
		name, v := strings.ToLower(s[m[4]:m[5]]), s[m[8]:m[9]]
		kept := ""
		if name == "authorization" || name == "proxy-authorization" {
			if scheme, rest, ok := strings.Cut(v, " "); ok && isWord(scheme) {
				rest = strings.TrimLeft(rest, " ")
				kept, v = v[:len(v)-len(rest)], rest
			}
		}
		if keepValue(v) {
			return s[m[0]:m[1]]
		}
		return s[m[0]:m[8]] + kept + mark("auth_header")
	})
	s = replaceSubmatch(urlRe, s, func(s string, m []int) string {
		if keepValue(s[m[4]:m[5]]) {
			return s[m[0]:m[1]]
		}
		return s[m[0]:m[4]] + mark("url_password") + "@"
	})
	s = replaceSubmatch(kvRe, s, func(s string, m []int) string {
		whole := s[m[0]:m[1]]
		key, sep := s[m[4]:m[5]], s[m[8]:m[9]]
		kind := keyKind(key)
		if kind == "" {
			return whole
		}
		if strings.TrimSpace(sep) == "" && !spacedPair(s, m, key, kind) {
			return whole
		}
		v := s[m[10]:m[11]]
		open, closing := "", ""
		if v[0] == '"' || v[0] == '\'' {
			open, closing, v = v[:1], v[len(v)-1:], v[1:len(v)-1]
		} else if t := strings.TrimRight(v, ",;"); t != "" {
			closing, v = v[len(t):], t
		}
		if keepValue(v) {
			return whole
		}
		return s[m[0]:m[10]] + open + mark(kind) + closing
	})
	s = tokenRe.ReplaceAllStringFunc(s, func(t string) string {
		if !strings.ContainsAny(t, "0123456789") {
			return t // a hyphenated name, not a random token
		}
		return mark("token")
	})
	return s, counts
}

// RedactStreams masks r's vault secrets, then RedactPatterns, in stdout and
// stderr, and returns the two streams' counts merged (nil when none).
func RedactStreams(r *Redactor, stdout, stderr string) (string, string, map[string]int) {
	stdout, counts := RedactPatterns(r.Redact(stdout))
	stderr, more := RedactPatterns(r.Redact(stderr))
	for k, n := range more {
		if counts == nil {
			counts = map[string]int{}
		}
		counts[k] += n
	}
	return stdout, stderr, counts
}

// replaceSubmatch is ReplaceAllStringFunc with the submatch indexes, which
// the standard library does not offer.
func replaceSubmatch(re *regexp.Regexp, s string, fn func(s string, m []int) string) string {
	var b strings.Builder
	last := 0
	for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(s[last:m[0]])
		b.WriteString(fn(s, m))
		last = m[1]
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// spacedPair reports whether a key and value separated only by whitespace
// may be a secret: a long flag (`--token v`, the value not another flag),
// or, for a password, a conf-file line holding only the key and one value
// (`password v`). Prose ("Failed password for root") is neither.
func spacedPair(s string, m []int, key, kind string) bool {
	v := s[m[10]:m[11]]
	if strings.HasPrefix(key, "--") {
		return v[0] != '-'
	}
	if kind != "password" {
		return false
	}
	lineStart := strings.LastIndexByte(s[:m[0]], '\n') + 1
	if strings.TrimLeft(s[lineStart:m[0]], " \t") != "" {
		return false
	}
	rest := s[m[1]:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest) == ""
}

// keyKind says which kind of secret a key names: "password", "secret", or
// "" for none. It matches whole words (split on non-alphanumerics and
// camelCase), case-insensitively: password and passwd inside any word, pwd
// and secret as any word, and pass, token, auth, apikey, credential(s),
// and api/access/private/secret + key only as the last word(s), so
// passFile, tokenTtlSeconds, SSH_AUTH_SOCK and credential.helper stay.
func keyKind(key string) string {
	if key == "PWD" || nginxPass[strings.ToLower(key)] {
		return "" // the shell's working directory; nginx upstreams
	}
	w := splitWords(key)
	if len(w) == 0 {
		return ""
	}
	for _, x := range w {
		if strings.Contains(x, "password") || strings.Contains(x, "passwd") || x == "pwd" {
			return "password"
		}
	}
	for _, x := range w {
		if x == "secret" {
			return "secret"
		}
	}
	last, prev := w[len(w)-1], ""
	if len(w) > 1 {
		prev = w[len(w)-2]
	}
	switch {
	case last == "pass":
		return "password"
	case last == "token", last == "auth", last == "apikey", last == "credential", last == "credentials":
		return "secret"
	case last == "key" && (prev == "api" || prev == "access" || prev == "private" || prev == "secret"):
		return "secret"
	}
	return ""
}

// splitWords lowercases key's words: split on anything not a letter or
// digit, and at camelCase boundaries ("DBPassword" is "db", "password").
func splitWords(key string) []string {
	var words []string
	for _, part := range strings.FieldsFunc(key, func(r rune) bool { return !isAlnum(byte(r)) }) {
		start := 0
		for i := 1; i < len(part); i++ {
			upper, prevLower := isUpper(part[i]), isLower(part[i-1])
			acronymEnd := isUpper(part[i-1]) && i+1 < len(part) && isLower(part[i+1])
			if upper && (prevLower || acronymEnd) {
				words = append(words, strings.ToLower(part[start:i]))
				start = i
			}
		}
		words = append(words, strings.ToLower(part[start:]))
	}
	return words
}

func isUpper(c byte) bool { return 'A' <= c && c <= 'Z' }
func isLower(c byte) bool { return 'a' <= c && c <= 'z' }
func isAlnum(c byte) bool { return isUpper(c) || isLower(c) || '0' <= c && c <= '9' }

// isWord reports an auth scheme word such as "Bearer" or "AWS4-HMAC-SHA256".
func isWord(s string) bool {
	if s == "" || !isUpper(s[0]) && !isLower(s[0]) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isAlnum(s[i]) && s[i] != '-' && s[i] != '_' && s[i] != '.' {
			return false
		}
	}
	return true
}

// keepValue reports a value that is not a secret: empty, a placeholder
// (a marker included), or an expression in code or a template.
func keepValue(v string) bool {
	l := strings.ToLower(v)
	switch {
	case v == "", strings.Trim(v, "*") == "", strings.Trim(l, "x") == "" && len(l) >= 3:
		return true
	case strings.HasPrefix(v, "<") && strings.HasSuffix(v, ">"):
		return true
	case l == "changeme", l == "replace_me", l == "null", l == "none", l == "true", l == "false":
		return true
	case strings.ContainsAny(v, "([{"), strings.HasPrefix(v, "$"), strings.HasPrefix(v, "%"), strings.HasPrefix(l, "process.env"):
		return true
	}
	return false
}
