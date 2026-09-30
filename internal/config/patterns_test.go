package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite testdata/redact/*.want from the current output")

const (
	positiveHeader = "# redact-test: secrets "
	negativeHeader = "# redact-test: negative"
)

// formatCounts renders counts in RedactKinds order, e.g. "password=2 secret=1".
func formatCounts(counts map[string]int) string {
	var parts []string
	for _, k := range RedactKinds {
		if n := counts[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", k, n))
		}
	}
	return strings.Join(parts, " ")
}

func total(counts map[string]int) int {
	n := 0
	for _, c := range counts {
		n += c
	}
	return n
}

// TestRedactCorpus runs RedactPatterns on every testdata/redact/<name>.in.
// Its first line is a header, stripped before redaction: "# redact-test:
// negative" (the body must come back byte for byte, with no counts) or
// "# redact-test: secrets <s1> <s2> ..." (the fake secrets that must be
// gone). A positive case's golden <name>.want is "# counts: <kind>=<n> ..."
// followed by the redacted body; -update rewrites it.
func TestRedactCorpus(t *testing.T) {
	ins, err := filepath.Glob(filepath.Join("testdata", "redact", "*.in"))
	if err != nil || len(ins) == 0 {
		t.Fatalf("no corpus: %v", err)
	}
	for _, in := range ins {
		t.Run(strings.TrimSuffix(filepath.Base(in), ".in"), func(t *testing.T) {
			raw, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			header, body, _ := strings.Cut(string(raw), "\n")
			got, counts := RedactPatterns(body)

			again, more := RedactPatterns(got)
			if again != got || more != nil {
				t.Errorf("not idempotent: second pass counted %v", more)
			}
			if added := strings.Count(got, "[REDACTED:") - strings.Count(body, "[REDACTED:"); added != total(counts) {
				t.Errorf("%d markers added, counts say %d (%v)", added, total(counts), counts)
			}

			switch {
			case header == negativeHeader:
				if got != body || counts != nil {
					t.Fatalf("negative case changed (counts %v):\n%s", counts, got)
				}
			case strings.HasPrefix(header, positiveHeader):
				if counts == nil {
					t.Fatal("positive case masked nothing")
				}
				for i, s := range strings.Fields(strings.TrimPrefix(header, positiveHeader)) {
					if strings.Contains(got, s) {
						t.Errorf("fake secret %d (%q) survived", i, s)
					}
				}
				want := "# counts: " + formatCounts(counts) + "\n" + got
				golden := strings.TrimSuffix(in, ".in") + ".want"
				if *update {
					if err := os.WriteFile(golden, []byte(want), 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				w, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("%v (run with -update to create it)", err)
				}
				if string(w) != want {
					t.Errorf("output differs from %s:\n--- got\n%s--- want\n%s", golden, want, w)
				}
			default:
				t.Fatalf("first line must be %q or %q<secrets>, got %q", negativeHeader, positiveHeader, header)
			}
		})
	}
}

// TestRedactPatternsRules pins each rule on one line. out "" means unchanged.
func TestRedactPatternsRules(t *testing.T) {
	cases := []struct{ name, in, out string }{
		// password: whole words, "pass" only last, password/passwd/pwd anywhere
		{"env", "DB_PASSWORD=hunter2x", "DB_PASSWORD=[REDACTED:password]"},
		{"export", "export DB_PASS=hunter2x", "export DB_PASS=[REDACTED:password]"},
		{"yaml", "  password: hunter2x", "  password: [REDACTED:password]"},
		{"ini spaced", "password = hunter2x", "password = [REDACTED:password]"},
		{"toml quoted", `db_password = "hunter 2x"`, `db_password = "[REDACTED:password]"`},
		{"json", `{"password": "hun\"ter2x"}`, `{"password": "[REDACTED:password]"}`},
		{"camel", "dbPassword: hunter2x", "dbPassword: [REDACTED:password]"},
		{"acronym", "DBPassword=hunter2x", "DBPassword=[REDACTED:password]"},
		{"pass last", "pass: hunter2x", "pass: [REDACTED:password]"},
		{"pwd word", "db_pwd=hunter2x", "db_pwd=[REDACTED:password]"},
		{"passwd", "passwd=hunter2x", "passwd=[REDACTED:password]"},
		{"password_hint accepted", "password_hint=blue", "password_hint=[REDACTED:password]"},
		{"passFile", "passFile=/etc/x", ""},
		{"passive", "passive=yes", ""},
		{"compass", "compass=north", ""},
		{"bypass", "bypass: cache", ""},
		{"PWD is cwd", "PWD=/home/u", ""},
		{"nginx proxy_pass", "    proxy_pass http://127.0.0.1:8080;", ""},
		{"long flag =", "mysql --password=hunter2x -h db", "mysql --password=[REDACTED:password] -h db"},
		{"long flag space", "mysql --password hunter2x -h db", "mysql --password [REDACTED:password] -h db"},
		{"flag then flag", "tool --password --stdin", ""},
		{"conf line", "  password hunter2x", "  password [REDACTED:password]"},
		{"prose", "Failed password for root from 10.0.0.1", ""},
		{"go assign", `password := r.FormValue("password")`, ""},
		{"trailing semicolon", "password=hunter2x;", "password=[REDACTED:password];"},
		// secret: "secret" as any word; token, auth, apikey, credential(s), api/access/private/secret key as the last
		{"token", "GITHUB_TOKEN=abc123def", "GITHUB_TOKEN=[REDACTED:secret]"},
		{"authToken", "//r.npmjs.org/:_authToken=abc123def", "//r.npmjs.org/:_authToken=[REDACTED:secret]"},
		{"auth", "auth: user:pw123", "auth: [REDACTED:secret]"},
		{"api_key", "api_key=abc123def", "api_key=[REDACTED:secret]"},
		{"apikey", "APIKEY=abc123def", "APIKEY=[REDACTED:secret]"},
		{"secret_key", "SECRET_KEY=abc123def", "SECRET_KEY=[REDACTED:secret]"},
		{"credentials", "credentials: abc123def", "credentials: [REDACTED:secret]"},
		{"long flag token", "run --token abc123def", "run --token [REDACTED:secret]"},
		{"token not last", "tokenTtlSeconds: 3600", ""},
		{"secret anywhere", "SECRET_KEY_BASE=abc123def", "SECRET_KEY_BASE=[REDACTED:secret]"},
		{"secretName accepted", "secretName: mwtn-env", "secretName: [REDACTED:secret]"},
		{"private_key_id", "private_key_id: 4f2a9c1e", ""},
		{"credential.helper", "credential.helper=store", ""},
		{"auth not last", "SSH_AUTH_SOCK=/tmp/agent.1", ""},
		{"author", "Author: Jane Doe", ""},
		{"secret conf line", "  auth_basic Restricted", ""},
		// placeholders and expressions
		{"empty", "DB_PASSWORD=", ""},
		{"stars", "DB_PASSWORD=***", ""},
		{"angle", "password: <password>", ""},
		{"changeme", "password: ChangeMe", ""},
		{"xxx", "password: xxxxxx", ""},
		{"replace_me", "password: REPLACE_ME", ""},
		{"null", `"password": null`, ""},
		{"false", "password: false", ""},
		{"env ref", "password: ${DB_PASS}", ""},
		{"dollar", "password=$DB_PASS", ""},
		{"template", "password: {{ .Values.pass }}", ""},
		{"printf", "password=%s", ""},
		{"process.env", "password: process.env.DB_PASSWORD,", ""},
		{"call", `password = os.getenv("X")`, ""},
		{"index", "password = request.form['password']", ""},
		{"literal", `password = "hunter2x"`, `password = "[REDACTED:password]"`},
		// private_key
		{"pem", "a\n-----BEGIN PRIVATE KEY-----\nMIIabc\n-----END PRIVATE KEY-----\nb", "a\n[REDACTED:private_key]\nb"},
		{"pem no end", "a\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3Bl", "a\n[REDACTED:private_key]"},
		{"pem public", "-----BEGIN PUBLIC KEY-----\nMIIabc\n-----END PUBLIC KEY-----", ""},
		{"pem in json", `"private_key": "-----BEGIN PRIVATE KEY-----\nMIIabc\n-----END PRIVATE KEY-----\n"`, `"private_key": "[REDACTED:private_key]\n"`},
		// auth_header
		{"bearer", "Authorization: Bearer abc.def.ghi", "Authorization: Bearer [REDACTED:auth_header]"},
		{"basic", "> authorization: Basic dXNlcjpwYXNz", "> authorization: Basic [REDACTED:auth_header]"},
		{"no scheme", "Authorization: abc123", "Authorization: [REDACTED:auth_header]"},
		{"curl -H", `curl -H "Authorization: Bearer abc123" https://x`, `curl -H "Authorization: Bearer [REDACTED:auth_header]" https://x`},
		{"proxy", "Proxy-Authorization: Basic dXNlcjpwYXNz", "Proxy-Authorization: Basic [REDACTED:auth_header]"},
		{"cookie", "Cookie: a=1; b=2", "Cookie: [REDACTED:auth_header]"},
		{"set-cookie", "< Set-Cookie: sid=abc; Path=/; HttpOnly", "< Set-Cookie: [REDACTED:auth_header]"},
		{"x-api-key", "X-Api-Key: abc123", "X-Api-Key: [REDACTED:auth_header]"},
		{"header var", `-H "Authorization: Bearer $TOKEN"`, ""},
		// url_password
		{"url", "postgres://app:hunter2x@db:5432/x", "postgres://app:[REDACTED:url_password]@db:5432/x"},
		{"url no user", "redis://:hunter2x@cache:6379", "redis://:[REDACTED:url_password]@cache:6379"},
		{"url port", "http://localhost:8080/path@x", ""},
		{"url var", "postgres://app:${PW}@db/x", ""},
		// token
		{"aws", "key AKIAIOSFODNN7EXAMPLE here", "key [REDACTED:token] here"},
		{"ghp", "ghp_R8mK2vQx7LpT4nWz9YbC3dFh6JsA1uEo5GiN", "[REDACTED:token]"},
		{"github_pat", "github_pat_11ABCDEFG0abcdefghij_klmnopqrstu", "[REDACTED:token]"},
		{"glpat", "glpat-Zq8Wm4tLp2Rk7XsHq9fT", "[REDACTED:token]"},
		{"slack", "xoxp-289471038-771028465", "[REDACTED:token]"},
		{"sk", "sk-proj-Zq8Wm4tLp2Rk7XsHq9fT3eVb", "[REDACTED:token]"},
		{"sk no digit", "sk-learn-something-very-long-name", ""},
		{"inside word", "task-Zq8Wm4tLp2Rk7XsHq9fT3eVb", ""},
		{"jwt", "t=eyJhbGciOi.eyJzdWIiOi.c2lnbmF0dXJl", "t=[REDACTED:token]"},
		{"kv wins", "GITHUB_TOKEN=ghp_R8mK2vQx7LpT4nWz9YbC3dFh6JsA1uEo5GiN", "GITHUB_TOKEN=[REDACTED:secret]"},
		{"sha", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  f", ""},
		{"uuid", "id: 7c1e9a3f-5b2d-4f8a-9c6e-0b2d4f6a8c1e", ""},
		// round-1 fixes: "=>" separator (I-2)
		{"php arrow", "'password' => 'hunter2x',", "'password' => '[REDACTED:password]',"},
		{"php arrow env default", "'password' => env('DB_PASSWORD', 'hunter2x'),", ""},
		{"ruby hash arrow", `:password => "hunter2x"`, `:password => "[REDACTED:password]"`},
		{"perl fat comma", "password => 'hunter2x'", "password => '[REDACTED:password]'"},
		// round-1 fixes: nested pairs behind a rejected/kept match (I-3)
		{"query string chain", "GET /api/items?sort_key=name&api_key=Zq8Wm4tLp2Rk7Xs HTTP/1.1", "GET /api/items?sort_key=name&api_key=[REDACTED:secret] HTTP/1.1"},
		{"oauth callback chain", "GET /cb?code=x&auth_method=pkce&access_token=Zq8Wm4tLp2Rk7Xs", "GET /cb?code=x&auth_method=pkce&access_token=[REDACTED:secret]"},
		{"ado net chain", "Server=db;Authentication=SqlPassword;Password=hunter2x;", "Server=db;Authentication=SqlPassword;Password=[REDACTED:password];"},
		{"jdbc chain", "jdbc:postgresql://db/app?sslkey=/x&password=hunter2x", "jdbc:postgresql://db/app?sslkey=/x&password=[REDACTED:password]"},
		{"keyfile chain", "keyfile=/etc/x,password=hunter2x", "keyfile=/etc/x,password=[REDACTED:password]"},
		{"quoted chain", `"authDb": "Server=db;Password=hunter2x"`, `"authDb": "Server=db;Password=[REDACTED:password]"`},
		// round-1 fixes: bracketed/symbol passwords are no longer kept as "expressions" (M-1)
		{"bracket password", `DB_PASSWORD='x9(Kq2]mLp7Rw1s'`, `DB_PASSWORD='[REDACTED:password]'`},
		{"brace password", `"password": "Hq9!fT3e{Vb6Ny1u"`, `"password": "[REDACTED:password]"`},
		// round-1 fixes: sshd config placeholders and paths (M-3)
		{"sshd password auth no", "PasswordAuthentication no", ""},
		{"sshd -T passwordauth", "passwordauthentication yes", ""},
		{"sshd permit empty", "permitemptypasswords no", ""},
		{"private key path", "private_key: /etc/ssl/private/key.pem", ""},
		// round-1 fixes: unterminated quote masked to end of line (M-5)
		{"unterminated quote", `DB_PASSWORD="hunter2x`, `DB_PASSWORD="[REDACTED:password]`},
		// round-1 cheap additions
		{"sk_test", "sk_" + "test_Zq8Wm4tLp2Rk7XsHq9fT3eVb", "[REDACTED:token]"},
		{"rk_test", "rk_" + "test_Zq8Wm4tLp2Rk7XsHq9fT3eVb", "[REDACTED:token]"},
		{"redis requirepass", "requirepass supersecret123", "requirepass [REDACTED:password]"},
		{"redis masterauth", "masterauth supersecret123", "masterauth [REDACTED:password]"},
		// round-1: orphan END with no preceding BEGIN of that kind
		{"pem orphan end", "a\n-----END PRIVATE KEY-----\nb", "[REDACTED:private_key]\nb"},
		// round 2
		{"pem extra end", "x\n-----BEGIN RSA PRIVATE KEY-----\nk\n-----END RSA PRIVATE KEY-----\ny\n-----END RSA PRIVATE KEY-----\nz", "x\n[REDACTED:private_key]\ny\n-----END RSA PRIVATE KEY-----\nz"},
		{"pem end before begin", "a\n-----END PRIVATE KEY-----\nb\n-----BEGIN PRIVATE KEY-----\nk\n-----END PRIVATE KEY-----\nc", "[REDACTED:private_key]\nb\n[REDACTED:private_key]\nc"},
		{"paren-led password", "DB_PASSWORD=(Kq2]mLp7Rw1s9", "DB_PASSWORD=[REDACTED:password]"},
		{"bracket-led password", "DB_PASSWORD=[Kq2mLp7Rw1s9", "DB_PASSWORD=[REDACTED:password]"},
		{"brace-led password", "DB_PASSWORD={Kq2mLp7Rw1s9", "DB_PASSWORD=[REDACTED:password]"},
		{"yaml brace-led password", "password: {Kq2mLp7Rw1s9", "password: [REDACTED:password]"},
		// round 3: flow maps and bracketed placeholders
		{"flow map", "auth: {password: hunter2x}", "auth: {password: [REDACTED:password]"},
		{"flow map quoted", `auth: {password: "hunter2x"}`, `auth: {password: "[REDACTED:password]"}`},
		{"flow map ruby", `auth: {:password=>"hunter2x"}`, `auth: {:password=>"[REDACTED:password]"}`},
		{"flow list", "auth: [password: hunter2x]", "auth: [password: [REDACTED:password]"},
		{"flow map benign", "auth: {enabled: true}", ""},
		{"placeholder none", "Password: (none)", ""},
		{"placeholder filtered", "password: [FILTERED]", ""},
		{"placeholder redacted", "password: [REDACTED]", ""},
		{"placeholder angle", "password: <NONE>", ""},
		{"json nested block", `"auth": {`, ""},
		{"yaml empty map", "auth: {}", ""},
		{"nested spaced prose", `"authMessage": "Password expired"`, ""},
		{"nested spaced prose 2", `"auth_error": "password incorrect"`, ""},
		{"string concat", `log("password: " + pw)`, ""},
		{"string concat single", `echo 'token: ' + x`, ""},
		{"command substitution", "export DB_PASSWORD=$(cat /run/secrets/db)", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := c.out
			if want == "" {
				want = c.in
			}
			got, counts := RedactPatterns(c.in)
			if got != want {
				t.Fatalf("got  %q\nwant %q", got, want)
			}
			again, moreCounts := RedactPatterns(got)
			if again != got {
				t.Fatalf("not idempotent: %q", again)
			}
			if moreCounts != nil {
				t.Fatalf("second pass counted %v", moreCounts)
			}
			if c.out == "" {
				if counts != nil {
					t.Fatalf("unchanged but counted %v", counts)
				}
			} else {
				sum := 0
				for _, n := range counts {
					sum += n
				}
				if want := strings.Count(got, "[REDACTED:") - strings.Count(c.in, "[REDACTED:"); sum != want {
					t.Fatalf("counts %v sum to %d, %d new markers", counts, sum, want)
				}
			}
		})
	}
}

// TestRedactScaling pins the linear cost: a long line with no newline (I-1)
// and a whitespace-free chain of rejected or kept keys (N-1) must stay cheap.
// Doubling the input should cost well under 3x time (quadratic code would cost
// about 4x); take the min of 3 runs to avoid flakiness.
func TestRedactScaling(t *testing.T) {
	for _, unit := range []string{"pass ", "key=", "password=${x}", "auth: {"} {
		timeFor := func(n int) time.Duration {
			s := strings.Repeat(unit, n/len(unit))
			best := time.Duration(1<<63 - 1)
			for i := 0; i < 3; i++ {
				start := time.Now()
				RedactPatterns(s)
				if d := time.Since(start); d < best {
					best = d
				}
			}
			return best
		}
		small := timeFor(64 * 1024)
		large := timeFor(128 * 1024)
		if large > small*3 {
			t.Fatalf("%q looks quadratic: 64KiB took %v, 128KiB took %v", unit, small, large)
		}
	}
}

func TestRedactStreams(t *testing.T) {
	red := NewRedactor("vaultpw1")
	out, errOut, counts := RedactStreams(red, "DB_PASSWORD=vaultpw1\nTOKEN=abc123def\n", "password=hunter2x\n")
	if out != "DB_PASSWORD=***\nTOKEN=[REDACTED:secret]\n" || errOut != "password=[REDACTED:password]\n" {
		t.Fatalf("got %q %q", out, errOut)
	}
	if len(counts) != 2 || counts["secret"] != 1 || counts["password"] != 1 {
		t.Fatalf("counts %v", counts)
	}
	if _, _, c := RedactStreams(red, "hello\n", ""); c != nil {
		t.Fatalf("nothing masked, counts %v", c)
	}
}
