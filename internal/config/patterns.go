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
	// An END line with no BEGIN of that kind before it: the tail of a key
	// whose head fell outside a windowed capture. Masks from the very start
	// of the text through the first END.
	orphanEndRe = regexp.MustCompile(`(?s)\A.*?-----END [A-Z ]*PRIVATE KEY(?: BLOCK)?-----`)
	// A masked block's marker counts as a BEGIN, so a second pass leaves an
	// END that follows one alone.
	pemBeginRe = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY(?: BLOCK)?-----|\[REDACTED:private_key\]`)
	// A credential header's name and value: "Name: v", "\"Name\": \"v\"",
	// or nginx's `Name "v"`. The value runs to a quote or the end of line.
	headerRe = regexp.MustCompile(`(?i)(^|[^a-z0-9_-])(proxy-authorization|authorization|set-cookie|cookie|x-api-key)(["']?[ \t]*:[ \t]*["']?|[ \t]+["'])([^\r\n"'` + "`" + `]*[^\s"'` + "`" + `])`)
	// scheme://user:password@host
	urlRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^\s:/@"']*:)([^\s@/"']+)@`)
	// A key that may name a secret, a separator, and a value. Which keys
	// really count is decided in code (keyKind): RE2 cannot split words.
	// The value's unquoted form never starts with '>', so "=>" is never
	// mistaken for "=" followed by a ">"-led value; nor with a bracket, so a
	// flow map's opening "{" ("auth: {password: x}") is not the parent key's
	// value and the pair inside is found. A quote with no match on the same
	// line ("*[^\r\n]*) is masked to the end of the line. Bracket-led
	// values are a second pass (kvBracketRe).
	kvHead      = `(?i)(["']?)(-{0,2}[a-z0-9_.-]*?(?:pass|pwd|secret|token|key|auth|credential)[a-z0-9_.-]*)(["']?)([ \t]*(?::=|==|=>|=|:)[ \t]*|[ \t]+)`
	kvRe        = regexp.MustCompile(kvHead + `("(?:[^"\\\r\n]|\\.)*"|'[^'\r\n]*'|"[^\r\n]*|'[^\r\n]*|[^\s"'` + "`" + `>(){}\[\]][^\s"'` + "`" + `]*)`)
	kvBracketRe = regexp.MustCompile(kvHead + `([(){}\[\]][^\s"'` + "`" + `]*)`)
	// Bare tokens with a known prefix.
	tokenRe = regexp.MustCompile(`\b(?:(?:AKIA|ASIA)[A-Z0-9]{16}\b|(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|xox[abpr]-[A-Za-z0-9-]{10,}|(?:sk-|sk_live_|sk_test_|rk_live_|rk_test_)[A-Za-z0-9_-]{20,}|eyJ[A-Za-z0-9_-]{5,}\.eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{10,})`)
	// A code expression: an identifier or dotted path immediately followed
	// by '(' or '['. No digits, so a password like "x9(..." doesn't count.
	exprPrefixRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z_.]*[(\[]`)
)

// nginxPass are nginx directives whose last word is "pass" but whose value
// is an upstream address, not a password.
var nginxPass = map[string]bool{"proxy_pass": true, "fastcgi_pass": true, "uwsgi_pass": true, "scgi_pass": true, "grpc_pass": true, "memcached_pass": true}

// redisPasswordKeys are redis config keys that are whole words, not the
// last word of a compound, so keyKind's word rules would otherwise miss
// them.
var redisPasswordKeys = map[string]bool{"requirepass": true, "masterauth": true}

// RedactPatterns masks values that look like secrets, whether or not
// sshgate knows them, and counts the masks by kind (nil when none). Each
// is replaced by "[REDACTED:<kind>]"; after a key only the value is, so
// the key and separator stay. It is pure, and idempotent: a marker is a
// placeholder, which every rule keeps.
//
// ponytail: RE2 passes over the whole output, about 0.6 s per MiB measured,
// linear as long as kvPass's nesting is capped: uncapped, a whitespace-free
// chain like "key=" repeated cost 20 s at 64 KiB (quadratic). Capped at depth
// 4 it costs 39 ms at 64 KiB and 373 ms at 1 MiB, so a secret behind a 5th
// nested rejected key is not found. Pre-filter lines by trigger word if huge
// outputs make the linear cost matter.
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
	// An END is an orphan only when it precedes every BEGIN; an END after a
	// BEGIN is just the far end of a block (or a stray literal) and stays.
	if end := orphanEndRe.FindStringIndex(s); end != nil {
		if begin := pemBeginRe.FindStringIndex(s); begin == nil || end[1] <= begin[0] {
			s = mark("private_key") + s[end[1]:]
		}
	}
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
	// kvPass recurses into a rejected or kept value, so a real secret pair
	// glued inside it (a query string, a connection string, ...) is still
	// found. The value handed to the recursive call is always shorter than
	// the match it came from, and the depth is capped (see the ponytail note
	// above: each level re-scans the rest of a whitespace-free value).
	var kvPass func(*regexp.Regexp, string, int) string
	kvPass = func(re *regexp.Regexp, str string, depth int) string {
		return replaceSubmatch(re, str, func(s string, m []int) string {
			key, sep := s[m[4]:m[5]], s[m[8]:m[9]]
			v := s[m[10]:m[11]]
			open, closing := "", ""
			stray := false
			switch {
			case v[0] == '"' || v[0] == '\'':
				q := v[0]
				if len(v) >= 2 && v[len(v)-1] == q {
					open, closing, v = v[:1], v[len(v)-1:], v[1:len(v)-1]
				} else {
					open, v = v[:1], v[1:] // unterminated: no closing quote
					// `"password: " + pw`: the quote closes a string the key
					// sits inside, it does not open a value.
					stray = s[m[2]:m[3]] == string(q) && m[6] == m[7]
				}
			default:
				if t := strings.TrimRight(v, ",;"); t != "" {
					closing, v = v[len(t):], t
				}
			}
			kind := keyKind(key)
			switch {
			case kind == "", stray:
			case strings.TrimSpace(sep) == "" && !spacedPair(s, m, key, kind, depth > 0):
			case keepValue(v), strings.ContainsRune("{[(", rune(v[0])) && (strings.HasSuffix(v, ":") || strings.HasSuffix(v, "=>")):
				// a flow map's first key is not a value
			default:
				return s[m[0]:m[10]] + open + mark(kind) + closing
			}
			if depth >= 4 {
				return s[m[0]:m[1]]
			}
			return s[m[0]:m[10]] + open + kvPass(re, v, depth+1) + closing
		})
	}
	s = kvPass(kvRe, s, 0)
	s = kvPass(kvBracketRe, s, 0)
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
// (`password v`). Prose ("Failed password for root") is neither. A nested
// call (inside a rejected value, where its edges are not line edges) takes
// only the long-flag form.
//
// Both walks are O(the whitespace run next to the match), not O(line
// length): a long line with no newline in it must stay cheap to check.
func spacedPair(s string, m []int, key, kind string, nested bool) bool {
	v := s[m[10]:m[11]]
	if strings.HasPrefix(key, "--") {
		return v[0] != '-'
	}
	if kind != "password" || nested {
		return false
	}
	i := m[0] - 1
	for i >= 0 && (s[i] == ' ' || s[i] == '\t') {
		i--
	}
	if i >= 0 && s[i] != '\n' {
		return false
	}
	j := m[1]
	for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\r') {
		j++
	}
	return j >= len(s) || s[j] == '\n'
}

// keyKind says which kind of secret a key names: "password", "secret", or
// "" for none. It matches whole words (split on non-alphanumerics and
// camelCase), case-insensitively: password and passwd inside any word, pwd
// and secret as any word, and pass, token, auth, apikey, credential(s),
// and api/access/private/secret + key only as the last word(s), so
// passFile, tokenTtlSeconds, SSH_AUTH_SOCK and credential.helper stay.
// requirepass and masterauth (redis) are password keys even though they
// are single fused words with no "pass"/"auth" word boundary.
func keyKind(key string) string {
	if key == "PWD" || nginxPass[strings.ToLower(key)] {
		return "" // the shell's working directory; nginx upstreams
	}
	if redisPasswordKeys[strings.ToLower(key)] {
		return "password"
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

// keepValue reports a value that is not a secret: empty, a placeholder, a
// filesystem path, or a code/template expression.
func keepValue(v string) bool {
	l := strings.ToLower(v)
	switch {
	case v == "", strings.Trim(v, "*") == "", strings.Trim(l, "x") == "" && len(l) >= 3:
		return true
	case strings.HasPrefix(v, "<") && strings.HasSuffix(v, ">"):
		return true
	case strings.HasPrefix(v, "[REDACTED:"):
		return true // a marker is a placeholder, so redaction stays idempotent
	case l == "changeme", l == "replace_me", l == "null", l == "none", l == "true", l == "false",
		l == "yes", l == "no", l == "on", l == "off",
		l == "(none)", l == "(null)", l == "[filtered]", l == "[redacted]":
		return true
	case strings.Trim(v, "{}[]()") == "":
		return true // a nested block's opening bracket, not a value
	case exprPrefixRe.MatchString(v), strings.HasPrefix(v, "${"), strings.HasPrefix(v, "{{"),
		len(v) >= 2 && v[0] == '$' && (isAlnum(v[1]) || v[1] == '_' || v[1] == '('),
		len(v) >= 2 && v[0] == '%' && strings.ContainsRune("svdq", rune(v[1])),
		strings.HasPrefix(l, "process.env."):
		return true
	case isPathValue(v):
		return true
	}
	return false
}

// isPathValue reports an absolute or home-relative filesystem path: sshd
// and TLS configs commonly name one where a secret-looking key lives.
func isPathValue(v string) bool {
	if !strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "~/") {
		return false
	}
	if strings.ContainsAny(v, " \t") {
		return false
	}
	return strings.Count(v, "/") >= 2
}

// redactMargin widens each window so a secret that starts before the kept
// part of the output is still seen whole (PEM keys are far smaller).
const redactMargin = 64 << 10

// RedactCap masks r's vault secrets, then RedactPatterns, and caps the result
// at max like CapOutput. Output longer than two margin-widened halves is only
// pattern-masked in its head and tail windows: the middle is dropped by the
// cap anyway, so masking cost stays bounded however much a command prints.
// The truncation marker counts bytes dropped from the vault-redacted output.
func RedactCap(r *Redactor, s string, max int) (string, map[string]int) {
	s = r.Redact(s)
	w := max/2 + redactMargin
	if len(s) <= 2*w {
		out, c := RedactPatterns(s)
		return CapOutput(out, max), c
	}
	// Align windows to line boundaries to avoid splitting secrets at cut edges.
	hw, tw := s[:w], s[len(s)-w:]
	if i := strings.LastIndexByte(hw, '\n'); i >= 0 {
		hw = hw[:i+1]
	}
	if i := strings.IndexByte(tw, '\n'); i >= 0 {
		tw = tw[i+1:]
	}
	head, c := RedactPatterns(hw)
	tail, more := RedactPatterns(tw)
	for k, n := range more {
		if c == nil {
			c = map[string]int{}
		}
		c[k] += n
	}
	// An orphan END in the tail window can mask most of it, so clamp.
	half := max / 2
	if len(head) > half {
		head = head[:half]
	}
	if len(tail) > half {
		tail = tail[len(tail)-half:]
	}
	return head + truncMarker(len(s)-max) + tail, c
}

// RedactCapStreams applies RedactCap to stdout and stderr and merges counts
// (nil when none).
func RedactCapStreams(r *Redactor, stdout, stderr string, max int) (string, string, map[string]int) {
	stdout, counts := RedactCap(r, stdout, max)
	stderr, more := RedactCap(r, stderr, max)
	for k, n := range more {
		if counts == nil {
			counts = map[string]int{}
		}
		counts[k] += n
	}
	return stdout, stderr, counts
}
