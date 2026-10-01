# Slice 4a: Output Redaction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mask values that look like secrets (private keys, passwords, tokens, credential headers, URL passwords) in every AI `exec`/`sudo-exec` output before it reaches the AI, tell the AI what was masked, and count it in the audit log.

**Architecture:** A pure function `config.RedactPatterns` (fixed Go `regexp` set, five passes) runs after the vault `Redactor` and before `CapOutput`; `config.RedactStreams` wraps both for stdout and stderr and merges the per-kind counts. The hub's `run` (shared by the approved and the auto-allowed path) uses it, returns the counts as `ExecResponse.Redacted` (JSON `redacted` on the MCP door) and writes them to the exec audit record; the bridge and standalone `FormatExec` render them as one final `note:` line.

**Tech Stack:** Go 1.26 standard library only (`regexp`, `strings`, `maps`); existing test seams (`Options.Dialer` + `fakeExec`, `sshtest`, `readAudit`, `findAutoRecord`).

**Spec:** `docs/superpowers/specs/2026-09-30-slice4a-output-redaction-design.md` (binding). Read it before any task.

## Global Constraints

- No new dependencies. `go.mod`/`go.sum` must not change.
- Every commit message ends with these two trailer lines (fill in your own model name):
  ```
  Co-Authored-By: <model> <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2
  ```
- Never stage `.DS_Store`, `.idea/`, or `docs/superpowers/plans/2026-09-30-slice4b-audit-viewer.md` (untracked, not yours). Stage files by explicit path; never `git add -A` or `git add .`.
- Stay on branch `feat/slice4a-redaction`. Do not push, do not open a PR.
- Checks before every commit: `go vet ./...` and `go test -race -timeout 900s ./...` (Docker integration tests skip themselves without Docker; that is fine, say so).
- Every `go test` command carries `-timeout`. `-update` exists only in `internal/config`: run it only as `go test ./internal/config -run TestRedactCorpus -update -timeout 120s` (other packages reject the flag).
- The live check never prints command output, a matched value, or a vault secret: only fixed command strings, byte counts, per-kind counts, and tripwire names.
- Every secret in testdata and tests is fake. Keep them exactly as written here: the goldens and the header lists depend on them.
- Security posture (CLAUDE.md "Conventions"): the command and description shown to the approver and written to the audit log stay vault-redacted only (not pattern-redacted); error texts, terminals, Send to tab, Files, and the auto-allow feed are unchanged. The UI-door protocol version does not change.
- Kinds and their order are fixed: `private_key, password, secret, auth_header, url_password, token` (`config.RedactKinds`, the spec's table order).

## Resolutions of spec ambiguities (binding for this plan)

These are decisions the spec left open or where its text conflicts with itself or with realistic output. Each one is pinned by a test in Task 1.

1. **Note order.** The spec's example `(password ×2, private_key ×1)` contradicts its own rule "kinds in table order"; table order wins: `(private_key ×1, password ×2)`.
2. **Singular note.** 1 masked value reads `note: sshgate redacted 1 value (token ×1); the value is withheld from AI clients`. The note is the last line and ends with `\n`.
3. **Key words.** `password`/`passwd` match inside any word, `pwd` and `secret` as any word (so `SECRET_KEY_BASE` matches; `secretName: x` is masked, an accepted false positive like `password_hint`). `pass`, `token`, `auth`, `apikey`, `credential(s)` and the pairs `api|access|private|secret` + `key` match only as the key's last word(s). This keeps `passFile`, `tokenTtlSeconds`, `SSH_AUTH_SOCK`, `credential.helper`, `auth_basic_user_file`, `apiKeyHeader`, `private_key_id` intact. `AWS_ACCESS_KEY_ID=AKIA…` is then masked by the `token` rule, not `secret`.
4. **Exclusions.** The exact key `PWD` (shell working directory) and nginx's `proxy_pass`, `fastcgi_pass`, `uwsgi_pass`, `scgi_pass`, `grpc_pass`, `memcached_pass` (upstream addresses) are not password keys.
5. **Whitespace-separated pairs.** `--flag value` counts for both password and secret keys (value not starting with `-`). A conf line `key value` (the key first on its line, one value, nothing after) counts only for password keys. Bare `-p v` is **not** covered: `-p` means port or parents in `ssh`, `mkdir`, `docker`, and the negative corpus would break.
6. **Unquoted values** run to whitespace or a quote; a trailing `,` or `;` stays outside the mask. A value with spaces is only fully masked when quoted.
7. **Separators** include `:=` and `==` (Go and comparisons), so `password := r.FormValue(...)` is read as an expression and kept.
8. **Tokens need a digit.** A prefix match with no digit (`sk-learn-some-long-package-name`) is kept.
9. **Hub tests use `fakeExec`, not `sshtest`.** `sshtest`'s exec echoes the command, and `SanitizeCommand` rejects newlines, so it cannot return a multi-line PEM. The full stack (bridge → MCP door → hub → `sshtest`) is covered in Task 3 by extending `TestEndToEndAutoAllow` with a one-line PEM, which `pemRe` also matches.
10. **Corpus files.** Each `.in` starts with one header line, stripped before redaction: `# redact-test: secrets <s1> <s2> …` or `# redact-test: negative`. Negative cases have **no** `.want`: the test asserts the body comes back byte for byte with nil counts (stronger than a golden that `-update` could bless). A positive case's `.want` is `# counts: <kind>=<n> …` then the redacted body.

## File Structure

| File | Task | Responsibility |
|---|---|---|
| `internal/config/patterns.go` (new) | 1 | `RedactKinds`, `RedactPatterns`, `RedactStreams` and their helpers |
| `internal/config/patterns_test.go` (new) | 1 | corpus golden + property test, per-rule table, `RedactStreams` test |
| `internal/config/testdata/redact/*.in`, `*.want` (new) | 1 | 18 positive and 13 negative cases, 18 goldens |
| `internal/broker/audit.go` | 2 | `AuditRecord.Redacted` |
| `internal/hub/hub.go` | 2 | `ExecResponse.Redacted`; `run` masks before the cap and records counts |
| `internal/hub/redact_test.go` (new) | 2 | approved path, auto path, nothing-masked path |
| `internal/mcpserver/tools.go` | 3 | `layoutExec(…, redacted)`, `redactionNote`, `FormatExec`, standalone tool text |
| `internal/mcpserver/bridge.go` | 3 | decode `redacted`, pass it to `layoutExec`, bridge tool text |
| `internal/mcpserver/tools_test.go`, `bridge_test.go` | 3 | note line, mask-before-cap |
| `cmd/sshgate/e2e_test.go` | 3 | full-stack redaction through `sshtest` |
| `internal/hub/live_test.go` | 4 | `openLiveVault` (extracted), `TestLiveRedact`, `tripwires` + its unit test |
| `README.md`, `PRODUCT.md`, `CLAUDE.md`, `docs/superpowers/ROADMAP.md` | 5 | docs |

Dependencies: Task 2 needs Task 1 (`RedactStreams`). Task 3 needs Task 1 (`RedactKinds`, `RedactStreams`) and Task 2 (the hub emits `redacted`, which the e2e reads). Task 4 needs Task 1 and the existing `redactorFor`. Task 5 is docs only but describes 1–4, so it runs last.

---

### Task 1: `config.RedactPatterns`, corpus, goldens

**Files:**
- Create: `internal/config/patterns.go`
- Create: `internal/config/patterns_test.go`
- Create: `internal/config/testdata/redact/` (31 `.in` files below; 18 `.want` files generated by `-update`)

**Interfaces:**
- Consumes: `config.Redactor` / `(*Redactor).Redact(string) string` (existing, `internal/config/redact.go`).
- Produces:
  - `var RedactKinds = []string{"private_key", "password", "secret", "auth_header", "url_password", "token"}`
  - `func RedactPatterns(s string) (string, map[string]int)` — counts are nil when nothing was masked.
  - `func RedactStreams(r *Redactor, stdout, stderr string) (string, string, map[string]int)` — vault redaction then `RedactPatterns` on each stream, counts merged, nil when nothing was masked. Does **not** cap.

- [ ] **Step 1: Create the corpus.** Run each block from the repo root exactly as written (the quoted `'EOF'` keeps `$`, backticks and tabs literal; `source_go.in` contains real tab characters).

First:

```bash
mkdir -p internal/config/testdata/redact
```

Positive cases (18):

`env_file.in`:

```bash
cat > internal/config/testdata/redact/env_file.in <<'EOF'
# redact-test: secrets Wm4tQz8vLp2Rk7Xs Hq9!fT3e#Vb6Ny1u 8d2f6a1c9e4b7f30a5d8c2e6b9f1a4d7 sk_live_51Hx-Qz8Wm4tLp2Rk7XsHq9fT3eVb6Ny1u2Ab AKIAIOSFODNN7EXAMPLE tQ8+Vx2mNz4kLp7Rw1sYb5Hc9Df3Gj6Ka0Ue8Io
# /etc/mwtn.env - managed by ansible, do not edit
APP_ENV=production
APP_PORT=8080
DB_HOST=10.0.3.12
DB_PORT=5432
DB_NAME=mwtn
DB_USER=mwtn_app
DB_PASSWORD=Wm4tQz8vLp2Rk7Xs
REDIS_URL=redis://10.0.3.20:6379/0
SMTP_HOST=smtp.mailgun.org
SMTP_USER=postmaster@mg.example.com
SMTP_PASS='Hq9!fT3e#Vb6Ny1u'
SESSION_SECRET=8d2f6a1c9e4b7f30a5d8c2e6b9f1a4d7
STRIPE_SECRET_KEY=sk_live_51Hx-Qz8Wm4tLp2Rk7XsHq9fT3eVb6Ny1u2Ab
AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
AWS_SECRET_ACCESS_KEY=tQ8+Vx2mNz4kLp7Rw1sYb5Hc9Df3Gj6Ka0Ue8Io
LOG_LEVEL=info
FEATURE_PASSWORD_RESET=true
BACKUP_PASSWORD=
EOF
```

`printenv.in`:

```bash
cat > internal/config/testdata/redact/printenv.in <<'EOF'
# redact-test: secrets ghp_R8mK2vQx7LpT4nWz9YbC3dFh6JsA1uEo5GiN npm_Fk3x9QvW2mLp8RtY6nBc4ZsH1jDa7eUo5Kg Pg7vLx2Qm9Rt4Wz
SHELL=/bin/bash
PWD=/home/deploy/app
LOGNAME=deploy
HOME=/home/deploy
LANG=en_US.UTF-8
TERM=xterm-256color
USER=deploy
SHLVL=1
SSH_AUTH_SOCK=/tmp/ssh-k2JdQ8vW/agent.4391
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
GITHUB_TOKEN=ghp_R8mK2vQx7LpT4nWz9YbC3dFh6JsA1uEo5GiN
NPM_TOKEN=npm_Fk3x9QvW2mLp8RtY6nBc4ZsH1jDa7eUo5Kg
PGPASSWORD=Pg7vLx2Qm9Rt4Wz
OLDPWD=/home/deploy
_=/usr/bin/printenv
EOF
```

`docker_inspect.in`:

```bash
cat > internal/config/testdata/redact/docker_inspect.in <<'EOF'
# redact-test: secrets Nq5Wz8Lp2Xv7Rk4T 3f9a7c1e5b2d8f4a6c0e9b7d5a3f1c8e mS4Kp9Wq2Xz7Lv1R
[
    {
        "Id": "4f2a9c1e7b3d5f8a0c6e2b9d4f7a1c3e5b8d0f2a4c6e9b1d3f5a7c9e0b2d4f6a",
        "Created": "2026-09-12T08:14:22.918273645Z",
        "Path": "docker-entrypoint.sh",
        "Args": [
            "postgres"
        ],
        "State": {
            "Status": "running",
            "Running": true,
            "Pid": 2231,
            "ExitCode": 0
        },
        "Image": "sha256:9b1f3e5a7c9d2b4f6a8c0e1d3f5b7a9c2e4d6f8b0a1c3e5d7f9b2a4c6e8d0f1b",
        "Name": "/mwtn-db-1",
        "Config": {
            "Hostname": "4f2a9c1e7b3d",
            "Env": [
                "POSTGRES_USER=mwtn",
                "POSTGRES_PASSWORD=Nq5Wz8Lp2Xv7Rk4T",
                "POSTGRES_DB=mwtn",
                "JWT_SECRET=3f9a7c1e5b2d8f4a6c0e9b7d5a3f1c8e",
                "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
                "PG_MAJOR=16",
                "PGDATA=/var/lib/postgresql/data"
            ],
            "Cmd": [
                "postgres"
            ],
            "Image": "postgres:16",
            "Labels": {
                "com.docker.compose.project": "mwtn",
                "com.docker.compose.service": "db",
                "com.example.backup.password": "mS4Kp9Wq2Xz7Lv1R"
            }
        }
    }
]
EOF
```

`kubectl_secret.in`:

```bash
cat > internal/config/testdata/redact/kubectl_secret.in <<'EOF'
# redact-test: secrets V200dFF6OHZMcDJSazdYcw== c2tfbGl2ZV81MUh4UXo4V200dExwMlJrN1hz aGVsbG8tdG9rZW4tZm9yLXRlc3RzLTEyMzQ1Ng==
apiVersion: v1
data:
  DB_HOST: MTAuMC4zLjEy
  DB_PASSWORD: V200dFF6OHZMcDJSazdYcw==
  STRIPE_API_KEY: c2tfbGl2ZV81MUh4UXo4V200dExwMlJrN1hz
  token: aGVsbG8tdG9rZW4tZm9yLXRlc3RzLTEyMzQ1Ng==
kind: Secret
metadata:
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"v1","data":{"DB_HOST":"MTAuMC4zLjEy","DB_PASSWORD":"V200dFF6OHZMcDJSazdYcw=="},"kind":"Secret","metadata":{"annotations":{},"name":"mwtn-env","namespace":"prod"},"type":"Opaque"}
  creationTimestamp: "2026-09-01T10:02:11Z"
  name: mwtn-env
  namespace: prod
  resourceVersion: "184223"
  uid: 7c1e9a3f-5b2d-4f8a-9c6e-0b2d4f6a8c1e
type: Opaque
EOF
```

`git_config.in`:

```bash
cat > internal/config/testdata/redact/git_config.in <<'EOF'
# redact-test: secrets ghs_T7kQ2mX9vLp4Rw8Zn3Yb6Cd1Fh5Js0AuEiGo Xk8mQ2vLp7Rt
user.name=Deploy Bot
user.email=deploy@example.com
core.editor=vim
credential.helper=store
init.defaultbranch=main
pull.rebase=false
url.https://deploy:Xk8mQ2vLp7Rt@git.example.com/.insteadof=https://git.example.com/
remote.origin.url=https://x-access-token:ghs_T7kQ2mX9vLp4Rw8Zn3Yb6Cd1Fh5Js0AuEiGo@github.com/example/mwtn.git
remote.origin.fetch=+refs/heads/*:refs/remotes/origin/*
branch.main.remote=origin
branch.main.merge=refs/heads/main
EOF
```

`curl_verbose.in`:

```bash
cat > internal/config/testdata/redact/curl_verbose.in <<'EOF'
# redact-test: secrets eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJkZXBsb3kiLCJpYXQiOjE3OTAwMDAwMDB9.Qm9ndXNTaWduYXR1cmVGb3JUZXN0c09ubHkxMjM 9c4e1a7f3b5d2e8c6a0f4b9d1e7c3a5f s%3Aq8Zk2WmXv7Lp4Rt9.Yb3Cd6Fh1Js5
*   Trying 10.0.3.30:443...
* Connected to api.internal.example.com (10.0.3.30) port 443
* ALPN: curl offers h2,http/1.1
* SSL connection using TLSv1.3 / TLS_AES_256_GCM_SHA384 / X25519 / RSASSA-PSS
* Server certificate:
*  subject: CN=api.internal.example.com
*  expire date: Mar 14 23:59:59 2027 GMT
> GET /v1/jobs?limit=10 HTTP/1.1
> Host: api.internal.example.com
> User-Agent: curl/8.5.0
> Accept: */*
> Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJkZXBsb3kiLCJpYXQiOjE3OTAwMDAwMDB9.Qm9ndXNTaWduYXR1cmVGb3JUZXN0c09ubHkxMjM
> X-Api-Key: 9c4e1a7f3b5d2e8c6a0f4b9d1e7c3a5f
>
< HTTP/1.1 200 OK
< Content-Type: application/json
< Content-Length: 23
< Set-Cookie: connect.sid=s%3Aq8Zk2WmXv7Lp4Rt9.Yb3Cd6Fh1Js5; Path=/; HttpOnly; Secure
<
{"jobs":[],"next":null}
* Connection #0 to host api.internal.example.com left intact
EOF
```

`pem_rsa.in`:

```bash
cat > internal/config/testdata/redact/pem_rsa.in <<'EOF'
# redact-test: secrets MIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gun 9z+40RQzuVaE8AkAFmxZzow3x+VJYKdjykkJ0iT9wCS0DRTXu269V264Vf/3jvre
-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gun
VTLw7onLRnrq0/IzW7yWR7QkrmBL7jTKEn5u+qKhbwKfBstIs+bMY2Zkp18gnTxK
LxoS2tFczGkPLPgizskuemMghRniWaoLcyehkd3qqGElvW/VDL5AaWTg0nLVkjRo
9z+40RQzuVaE8AkAFmxZzow3x+VJYKdjykkJ0iT9wCS0DRTXu269V264Vf/3jvre
-----END RSA PRIVATE KEY-----
ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7VJTUt9Us8cKjMzEfYyjiWA4R4/M2bS1GB4t7NXp98C3SC6dVMvDuictGeurT8jNbvJZHtCSuYEvuNMoSfm76oqFvAp8Gy0iz5sxjZmSnXyCdPEovGhLa0VzMaQ8s+CLOyS56YyCFGeJZqgtzJ6GR3eqoYSW9b9UMvkBpZODSctWSNGj3P7jRFDO5VoTwCQAWbFnOjDfH5Ulgp2PKSQnSJP3AJLQNFNe7br1XbrhV//eO+t5 deploy@web1
EOF
```

`pem_openssh.in`:

```bash
cat > internal/config/testdata/redact/pem_openssh.in <<'EOF'
# redact-test: secrets b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW AAAAEDx3kP9mQ2vL7rT4wZ8nY1bC6dF5hJ0sA3uE9iG2oK7tWrq7Xc4r1PzL0mYq3Hk9vJ2
-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACBq7Xc4r1PzL0mYq3Hk9vJ2dNf8sWb5tRe6uGi0aOyVkQAAAJgB2Nw8AdjcPA
AAAAtzc2gtZWQyNTUxOQAAACBq7Xc4r1PzL0mYq3Hk9vJ2dNf8sWb5tRe6uGi0aOyVkQ
AAAEDx3kP9mQ2vL7rT4wZ8nY1bC6dF5hJ0sA3uE9iG2oK7tWrq7Xc4r1PzL0mYq3Hk9vJ2
dNf8sWb5tRe6uGi0aOyVkQAAAA9kZXBsb3lAd2ViMS1wcm9kAQIDBAUG
-----END OPENSSH PRIVATE KEY-----
EOF
```

`pem_ec.in`:

```bash
cat > internal/config/testdata/redact/pem_ec.in <<'EOF'
# redact-test: secrets MHcCAQEEIKq3Zp8Lm2Vx7Rt4Wn9Yb6Cd1Fh5Js0AuEiGoKq3Zp8LoAoGCCqGSM49
-----BEGIN EC PARAMETERS-----
BggqhkjOPQMBBw==
-----END EC PARAMETERS-----
-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIKq3Zp8Lm2Vx7Rt4Wn9Yb6Cd1Fh5Js0AuEiGoKq3Zp8LoAoGCCqGSM49
AwEHoUQDQgAE3Xk9Pm2Qv7Lr4Tw8Zn1Yb6Cd5Fh0Js3Au9EiGo2Kq7Zp8Lm4Vx1Rt6W
n9Yb3Cd7Fh2Js5Au0EiGo8Kq==
-----END EC PRIVATE KEY-----
EOF
```

`pem_pgp.in`:

```bash
cat > internal/config/testdata/redact/pem_pgp.in <<'EOF'
# redact-test: secrets lQVYBGb3kQEBDADq7Xc4r1PzL0mYq3Hk9vJ2dNf8sWb5tRe6uGi0aOyVkQx3kP9m
-----BEGIN PGP PUBLIC KEY BLOCK-----

mQENBGb3kQEBCADq7Xc4r1PzL0mYq3Hk9vJ2dNf8sWb5tRe6uGi0aOyVkQx3kP9m
=Ab3Q
-----END PGP PUBLIC KEY BLOCK-----
-----BEGIN PGP PRIVATE KEY BLOCK-----

lQVYBGb3kQEBDADq7Xc4r1PzL0mYq3Hk9vJ2dNf8sWb5tRe6uGi0aOyVkQx3kP9m
Q2vL7rT4wZ8nY1bC6dF5hJ0sA3uE9iG2oK7tWrq7Xc4r1PzL0mYq3Hk9vJ2dNf8s
=Xk9P
-----END PGP PRIVATE KEY BLOCK-----
EOF
```

`pem_encrypted.in`:

```bash
cat > internal/config/testdata/redact/pem_encrypted.in <<'EOF'
# redact-test: secrets 3Xk9Pm2Qv7Lr4Tw8Zn1Yb6Cd5Fh0Js3Au9EiGo2Kq7Zp8Lm4Vx1Rt6Wn9Yb3Cd7F MIIFHDBOBgkqhkiG9w0BBQ0wQTApBgkqhkiG9w0BBQwwHAQIq7Xc4r1PzL0CAggA
-----BEGIN RSA PRIVATE KEY-----
Proc-Type: 4,ENCRYPTED
DEK-Info: AES-128-CBC,3F2A9C1E7B3D5F8A0C6E2B9D4F7A1C3E

3Xk9Pm2Qv7Lr4Tw8Zn1Yb6Cd5Fh0Js3Au9EiGo2Kq7Zp8Lm4Vx1Rt6Wn9Yb3Cd7F
h2Js5Au0EiGo8Kq7Zp8Lm2Vx7Rt4Wn9Yb6Cd1Fh5Js0AuEiGo==
-----END RSA PRIVATE KEY-----
-----BEGIN ENCRYPTED PRIVATE KEY-----
MIIFHDBOBgkqhkiG9w0BBQ0wQTApBgkqhkiG9w0BBQwwHAQIq7Xc4r1PzL0CAggA
MAwGCCqGSIb3DQIJBQAwHQYJYIZIAWUDBAEqBBDx3kP9mQ2vL7rT4wZ8nY1bBIIE
-----END ENCRYPTED PRIVATE KEY-----
EOF
```

`pem_truncated.in`:

```bash
cat > internal/config/testdata/redact/pem_truncated.in <<'EOF'
# redact-test: secrets MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VJTUt9Us8cKj MzEfYyjiWA4R4/M2bS1GB4t7NXp98C3SC6dVMvDuictGeurT8jNbvJZHtCSuYEvu
-----BEGIN CERTIFICATE-----
MIIDdzCCAl+gAwIBAgIEAgAAuTANBgkqhkiG9w0BAQUFADBaMQswCQYDVQQGEwJJ
RTESMBAGA1UEChMJQmFsdGltb3JlMRMwEQYDVQQLEwpDeWJlclRydXN0MSIwIAYD
-----END CERTIFICATE-----
-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VJTUt9Us8cKj
MzEfYyjiWA4R4/M2bS1GB4t7NXp98C3SC6dVMvDuictGeurT8jNbvJZHtCSuYEvu
EOF
```

`aws_credentials.in`:

```bash
cat > internal/config/testdata/redact/aws_credentials.in <<'EOF'
# redact-test: secrets AKIAIOSFODNN7EXAMPLE wJalrXUtnFEMIK7MDENGbPxRfiCYzq8v2Lp4Rt9 ASIAIOSFODNN7EXAMPLE Rk7XsHq9fT3eVb6Ny1u2AbWm4tQz8vLp2Cd5Fh0J IQoJb3JpZ2luX2VjEHoaCWV1LXdlc3QtMSJHMEUCIQDx3kP9mQ2vL7rT4wZ8nY1bC6dF5hJ0sA3uE9iG2oK7tQ==
[default]
aws_access_key_id = AKIAIOSFODNN7EXAMPLE
aws_secret_access_key = wJalrXUtnFEMIK7MDENGbPxRfiCYzq8v2Lp4Rt9
region = eu-west-1

[prod]
aws_access_key_id = ASIAIOSFODNN7EXAMPLE
aws_secret_access_key = Rk7XsHq9fT3eVb6Ny1u2AbWm4tQz8vLp2Cd5Fh0J
aws_session_token = IQoJb3JpZ2luX2VjEHoaCWV1LXdlc3QtMSJHMEUCIQDx3kP9mQ2vL7rT4wZ8nY1bC6dF5hJ0sA3uE9iG2oK7tQ==
region = eu-west-1
EOF
```

`npmrc.in`:

```bash
cat > internal/config/testdata/redact/npmrc.in <<'EOF'
# redact-test: secrets ghp_Z3nY8bC1dF6hJ0sA5uE9iG2oK7tWq4Xm8LpR npm_Fk3x9QvW2mLp8RtY6nBc4ZsH1jDa7eUo5Kg
registry=https://registry.npmjs.org/
@example:registry=https://npm.pkg.github.com/
//npm.pkg.github.com/:_authToken=ghp_Z3nY8bC1dF6hJ0sA5uE9iG2oK7tWq4Xm8LpR
//registry.npmjs.org/:_authToken=npm_Fk3x9QvW2mLp8RtY6nBc4ZsH1jDa7eUo5Kg
always-auth=true
EOF
```

`my_cnf.in`:

```bash
cat > internal/config/testdata/redact/my_cnf.in <<'EOF'
# redact-test: secrets Tr0ub4dor&3xQ Zx9Lm2Qv7Rt4Wp
[client]
user = backup
password = "Tr0ub4dor&3xQ"
host = 10.0.3.12
socket = /var/run/mysqld/mysqld.sock

[mysqldump]
quick
max_allowed_packet = 64M
user = backup
password=Zx9Lm2Qv7Rt4Wp
EOF
```

`nginx_conf.in`:

```bash
cat > internal/config/testdata/redact/nginx_conf.in <<'EOF'
# redact-test: secrets 7f3e9a1c5b2d8f4a6c0e9b7d5a3f1c8e
server {
    listen 443 ssl http2;
    server_name api.example.com;

    ssl_certificate     /etc/letsencrypt/live/api.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/api.example.com/privkey.pem;

    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header Authorization "Bearer 7f3e9a1c5b2d8f4a6c0e9b7d5a3f1c8e";
    }

    location /internal/ {
        auth_basic "Restricted";
        auth_basic_user_file /etc/nginx/.htpasswd;
        proxy_pass http://127.0.0.1:9090;
    }
}
EOF
```

`json_config.in`:

```bash
cat > internal/config/testdata/redact/json_config.in <<'EOF'
# redact-test: secrets Wm4tQz8vLp2Rk7Xs b7d5a3f1c8e9a7c1e5b2d8f4a6c0e9b7 Gq7Wz2Xv9Lp4Rk8Tn3Yb Hq9fT3eVb6Ny1uZk
{
  "server": {
    "host": "0.0.0.0",
    "port": 8080
  },
  "database": {
    "url": "postgres://mwtn_app:Wm4tQz8vLp2Rk7Xs@10.0.3.12:5432/mwtn?sslmode=require",
    "pool": 10
  },
  "auth": {
    "jwtSecret": "b7d5a3f1c8e9a7c1e5b2d8f4a6c0e9b7",
    "tokenTtlSeconds": 3600,
    "oauth": {
      "clientId": "mwtn-web",
      "clientSecret": "Gq7Wz2Xv9Lp4Rk8Tn3Yb"
    }
  },
  "mail": {
    "smtpPassword": "Hq9fT3eVb6Ny1uZk",
    "passwordResetUrl": "https://mwtn.example.com/reset"
  },
  "features": {
    "passwordLogin": true,
    "apiKeyHeader": "X-Api-Key"
  }
}
EOF
```

`app_log_tokens.in`:

```bash
cat > internal/config/testdata/redact/app_log_tokens.in <<'EOF'
# redact-test: secrets ghp_R8mK2vQx7LpT4nWz9YbC3dFh6JsA1uEo5GiN eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTQyIiwiZXhwIjoxNzkwMDAzNjAwfQ.c2lnbmF0dXJlLWZvci10ZXN0cy1vbmx5LTEyMzQ xoxb-289471038-771028465-Zq8Wm4tLp2Rk7XsHq9fT3eVb
2026-09-29T14:02:11.482Z INFO  deploy: cloning https://github.com/example/mwtn.git
2026-09-29T14:02:11.913Z DEBUG git: using token ghp_R8mK2vQx7LpT4nWz9YbC3dFh6JsA1uEo5GiN for github.com
2026-09-29T14:02:14.207Z INFO  deploy: checked out 3f9c2e1 (main)
2026-09-29T14:02:15.001Z DEBUG api: POST /v1/session 200 12ms session=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyLTQyIiwiZXhwIjoxNzkwMDAzNjAwfQ.c2lnbmF0dXJlLWZvci10ZXN0cy1vbmx5LTEyMzQ
2026-09-29T14:02:15.377Z WARN  worker: slack webhook failed, token xoxb-289471038-771028465-Zq8Wm4tLp2Rk7XsHq9fT3eVb
2026-09-29T14:02:16.020Z INFO  deploy: done in 4.5s
EOF
```


Negative cases (13):

`ls_la.in`:

```bash
cat > internal/config/testdata/redact/ls_la.in <<'EOF'
# redact-test: negative
total 64
drwxr-x--- 6 deploy deploy 4096 Sep 29 14:02 .
drwxr-xr-x 4 root   root   4096 Aug  3 09:11 ..
-rw------- 1 deploy deploy  812 Sep 28 22:40 .bash_history
-rw-r--r-- 1 deploy deploy  220 Aug  3 09:11 .bash_logout
-rw-r--r-- 1 deploy deploy 3771 Aug  3 09:11 .bashrc
drwx------ 3 deploy deploy 4096 Aug  3 09:15 .cache
-rw------- 1 deploy deploy  118 Sep  2 11:20 .env
-rw------- 1 deploy deploy   64 Sep  2 11:21 .pgpass
-rw-r--r-- 1 deploy deploy  807 Aug  3 09:11 .profile
drwx------ 2 deploy deploy 4096 Sep  2 11:18 .ssh
drwxr-xr-x 8 deploy deploy 4096 Sep 29 14:02 app
-rw-r--r-- 1 deploy deploy 1543 Sep 12 08:14 docker-compose.yml
-rw------- 1 deploy deploy  402 Sep 12 08:14 secrets.yaml
-rw-r--r-- 1 deploy deploy   91 Sep 20 17:03 token-rotation.log
EOF
```

`df_h.in`:

```bash
cat > internal/config/testdata/redact/df_h.in <<'EOF'
# redact-test: negative
Filesystem      Size  Used Avail Use% Mounted on
udev            3.9G     0  3.9G   0% /dev
tmpfs           796M  1.2M  795M   1% /run
/dev/sda1        78G   41G   34G  55% /
tmpfs           3.9G     0  3.9G   0% /dev/shm
tmpfs           5.0M     0  5.0M   0% /run/lock
/dev/sda15      105M  6.1M   99M   6% /boot/efi
overlay          78G   41G   34G  55% /var/lib/docker/overlay2/4f2a9c1e7b3d5f8a0c6e2b9d4f7a1c3e/merged
tmpfs           796M  4.0K  796M   1% /run/user/1000
EOF
```

`ps_aux.in`:

```bash
cat > internal/config/testdata/redact/ps_aux.in <<'EOF'
# redact-test: negative
USER         PID %CPU %MEM    VSZ   RSS TTY      STAT START   TIME COMMAND
root           1  0.0  0.1 167740 11800 ?        Ss   Sep28   0:04 /sbin/init
root         412  0.0  0.2  48232 17240 ?        S<s  Sep28   0:01 /lib/systemd/systemd-journald
root         688  0.0  0.1  15432  9120 ?        Ss   Sep28   0:00 sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups
root         702  0.1  0.6 1428776 51220 ?       Ssl  Sep28   1:12 /usr/bin/containerd
root         915  0.2  1.1 2064272 90112 ?       Ssl  Sep28   2:40 /usr/bin/dockerd -H fd:// --containerd=/run/containerd/containerd.sock
999         2231  0.0  0.3 216744 28812 ?        Ss   Sep28   0:03 postgres
999         2310  0.0  0.1 216876  8104 ?        Ss   Sep28   0:00 postgres: checkpointer
deploy      3120  1.3  2.4 1152340 198452 ?      Ssl  Sep28  18:22 node /home/deploy/app/dist/server.js --port 8080
root        4401  0.0  0.1  17180 10840 ?        Ss   14:01   0:00 sshd: deploy [priv]
deploy      4455  0.0  0.0   8968  3920 pts/0    R+   14:02   0:00 ps aux
EOF
```

`systemctl_status.in`:

```bash
cat > internal/config/testdata/redact/systemctl_status.in <<'EOF'
# redact-test: negative
● ssh.service - OpenBSD Secure Shell server
     Loaded: loaded (/lib/systemd/system/ssh.service; enabled; vendor preset: enabled)
     Active: active (running) since Sun 2026-09-28 08:02:13 UTC; 1 day 6h ago
       Docs: man:sshd(8)
             man:sshd_config(5)
    Process: 681 ExecStartPre=/usr/sbin/sshd -t (code=exited, status=0/SUCCESS)
   Main PID: 688 (sshd)
      Tasks: 1 (limit: 9387)
     Memory: 6.9M
        CPU: 1.204s
     CGroup: /system.slice/ssh.service
             └─688 "sshd: /usr/sbin/sshd -D [listener] 0 of 10-100 startups"

Sep 29 13:58:40 web1 sshd[4391]: Accepted publickey for deploy from 203.0.113.7 port 51022 ssh2: ED25519 SHA256:q8Zk2WmXv7Lp4Rt9Yb3Cd6Fh1Js5Au0EiGo8Kq7Zp8
Sep 29 13:58:40 web1 sshd[4391]: pam_unix(sshd:session): session opened for user deploy(uid=1000) by (uid=0)
EOF
```

`journalctl.in`:

```bash
cat > internal/config/testdata/redact/journalctl.in <<'EOF'
# redact-test: negative
Sep 29 03:12:07 web1 sshd[2877]: Invalid user admin from 198.51.100.23 port 40122
Sep 29 03:12:09 web1 sshd[2877]: Failed password for invalid user admin from 198.51.100.23 port 40122 ssh2
Sep 29 03:12:10 web1 sshd[2877]: Connection closed by invalid user admin 198.51.100.23 port 40122 [preauth]
Sep 29 03:14:51 web1 sshd[2903]: pam_unix(sshd:auth): authentication failure; logname= uid=0 euid=0 tty=ssh ruser= rhost=198.51.100.23  user=root
Sep 29 03:14:53 web1 sshd[2903]: Failed password for root from 198.51.100.23 port 40388 ssh2
Sep 29 09:30:02 web1 CRON[3301]: pam_unix(cron:session): session opened for user root(uid=0) by (uid=0)
Sep 29 13:58:12 web1 sudo[4370]:   deploy : TTY=pts/0 ; PWD=/home/deploy ; USER=root ; COMMAND=/usr/bin/systemctl restart mwtn
Sep 29 13:58:12 web1 sudo[4370]: pam_unix(sudo:session): session opened for user root(uid=0) by deploy(uid=1000)
Sep 29 13:58:13 web1 systemd[1]: Started mwtn.service - mwtn API.
EOF
```

`git_log.in`:

```bash
cat > internal/config/testdata/redact/git_log.in <<'EOF'
# redact-test: negative
commit 3f9c2e1a7b5d8f0c4e6a2b9d1f3e5c7a9b0d2f4e
Author: Jane Doe <jane@example.com>
Date:   Mon Sep 29 13:40:02 2026 +0200

    Rotate the session key on every deploy

    The old key lived for 90 days. Keys now come from Vault at
    start-up; see docs/secrets.md for the passthrough setup.

commit 9b1f3e5a7c9d2b4f6a8c0e1d3f5b7a9c2e4d6f8b
Author: Sam Lee <sam@example.com>
Date:   Fri Sep 26 17:05:44 2026 +0200

    Add token bucket rate limiting to the login endpoint
EOF
```

`sha256sum.in`:

```bash
cat > internal/config/testdata/redact/sha256sum.in <<'EOF'
# redact-test: negative
e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  empty.txt
9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08  deploy.tar.gz
2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae  secrets.env.gpg
EOF
```

`docker_ps.in`:

```bash
cat > internal/config/testdata/redact/docker_ps.in <<'EOF'
# redact-test: negative
CONTAINER ID   IMAGE                            COMMAND                  CREATED       STATUS                  PORTS                                       NAMES
4f2a9c1e7b3d   postgres:16                      "docker-entrypoint.s…"   2 weeks ago   Up 29 hours             5432/tcp                                    mwtn-db-1
a1c3e5b8d0f2   redis:7-alpine                   "docker-entrypoint.s…"   2 weeks ago   Up 29 hours             6379/tcp                                    mwtn-redis-1
7b9d1f3e5c7a   ghcr.io/example/mwtn-api:1.8.2   "node dist/server.js"    3 days ago    Up 29 hours (healthy)   0.0.0.0:8080->8080/tcp, :::8080->8080/tcp   mwtn-api-1
EOF
```

`uname_a.in`:

```bash
cat > internal/config/testdata/redact/uname_a.in <<'EOF'
# redact-test: negative
Linux web1 6.8.0-45-generic #45-Ubuntu SMP PREEMPT_DYNAMIC Fri Aug 30 12:02:04 UTC 2026 x86_64 x86_64 x86_64 GNU/Linux
EOF
```

`source_python.in`:

```bash
cat > internal/config/testdata/redact/source_python.in <<'EOF'
# redact-test: negative
import os

import bcrypt
from flask import Flask, request

app = Flask(__name__)
DB_PASSWORD = os.environ["DB_PASSWORD"]
API_TOKEN = os.getenv("API_TOKEN", "")


def check_password(username, password):
    hashed = get_hash(username)
    return bcrypt.checkpw(password.encode(), hashed)


@app.route("/login", methods=["POST"])
def login():
    username = request.form["username"]
    password = request.form['password']
    if not check_password(username, password):
        return "invalid credentials", 401
    return "ok"
EOF
```

`source_js.in`:

```bash
cat > internal/config/testdata/redact/source_js.in <<'EOF'
# redact-test: negative
const express = require("express");
const { Pool } = require("pg");

const pool = new Pool({
  host: process.env.DB_HOST,
  user: process.env.DB_USER,
  password: process.env.DB_PASSWORD,
  database: process.env.DB_NAME,
});

const apiToken = process.env.API_TOKEN;

const app = express();
app.post("/login", async (req, res) => {
  const { username, password } = req.body;
  const ok = await verifyPassword(username, password);
  res.status(ok ? 200 : 401).end();
});
EOF
```

`source_go.in`:

```bash
cat > internal/config/testdata/redact/source_go.in <<'EOF'
# redact-test: negative
package main

import (
	"net/http"
	"os"
)

type Config struct {
	DBPassword string `json:"db_password"`
	APIToken   string `json:"api_token"`
}

func load() Config {
	return Config{
		DBPassword: os.Getenv("DB_PASSWORD"),
		APIToken:   os.Getenv("API_TOKEN"),
	}
}

func login(w http.ResponseWriter, r *http.Request) {
	password := r.FormValue("password")
	if !checkPassword(r.FormValue("user"), password) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}
EOF
```

`helm_template.in`:

```bash
cat > internal/config/testdata/redact/helm_template.in <<'EOF'
# redact-test: negative
apiVersion: v1
kind: Secret
metadata:
  name: {{ include "mwtn.fullname" . }}-env
type: Opaque
stringData:
  DB_PASSWORD: {{ .Values.db.password | quote }}
  API_TOKEN: {{ required "apiToken is required" .Values.apiToken | quote }}
---
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: api
          env:
            - name: DB_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: {{ include "mwtn.fullname" . }}-env
                  key: DB_PASSWORD
            - name: SESSION_SECRET
              value: "{{ .Values.sessionSecret }}"
EOF
```


Check: `ls internal/config/testdata/redact/*.in | wc -l` prints `31`.

- [ ] **Step 2: Write the failing test** `internal/config/patterns_test.go`:

````go
package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
			if again, _ := RedactPatterns(got); again != got {
				t.Fatalf("not idempotent: %q", again)
			}
			if c.out == "" && counts != nil {
				t.Fatalf("unchanged but counted %v", counts)
			}
		})
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
````

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/config -run 'TestRedact' -count=1 -timeout 120s`
Expected: FAIL, build error `undefined: RedactPatterns` (and `RedactKinds`, `RedactStreams`).

- [ ] **Step 4: Implement** `internal/config/patterns.go`:

````go
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
````

- [ ] **Step 5: Run the rule tests**

Run: `go test ./internal/config -run 'TestRedactPatternsRules|TestRedactStreams' -count=1 -timeout 120s -v`
Expected: PASS (86 rule subtests plus `TestRedactStreams`).

Run: `go test ./internal/config -run TestRedactCorpus -count=1 -timeout 120s`
Expected: FAIL only on the 18 positive cases with `open testdata/redact/<name>.want: no such file or directory (run with -update to create it)`; all 13 negative cases PASS.

- [ ] **Step 6: Generate and review the goldens**

Run: `go test ./internal/config -run TestRedactCorpus -update -count=1 -timeout 120s`
Then: `for f in internal/config/testdata/redact/*.want; do echo "$(basename "$f" .want) $(head -n 1 "$f")"; done` and compare with this table line by line (sorted by file name). Any difference means the implementation differs from this plan: stop and fix it, do not accept the new golden.

| case | first line of `.want` |
|---|---|
| app_log_tokens | `# counts: token=3` |
| aws_credentials | `# counts: secret=3 token=2` |
| curl_verbose | `# counts: auth_header=3` |
| docker_inspect | `# counts: password=2 secret=1` |
| env_file | `# counts: password=2 secret=3 token=1` |
| git_config | `# counts: url_password=2` |
| json_config | `# counts: password=2 secret=2 url_password=1` |
| kubectl_secret | `# counts: password=2 secret=2` |
| my_cnf | `# counts: password=2` |
| nginx_conf | `# counts: auth_header=1` |
| npmrc | `# counts: secret=2` |
| pem_ec | `# counts: private_key=1` |
| pem_encrypted | `# counts: private_key=2` |
| pem_openssh | `# counts: private_key=1` |
| pem_pgp | `# counts: private_key=1` |
| pem_rsa | `# counts: private_key=1` |
| pem_truncated | `# counts: private_key=1` |
| printenv | `# counts: password=1 secret=2` |

Two goldens in full; the generated files must be identical to these:

`env_file.want`:
````text
# counts: password=2 secret=3 token=1
# /etc/mwtn.env - managed by ansible, do not edit
APP_ENV=production
APP_PORT=8080
DB_HOST=10.0.3.12
DB_PORT=5432
DB_NAME=mwtn
DB_USER=mwtn_app
DB_PASSWORD=[REDACTED:password]
REDIS_URL=redis://10.0.3.20:6379/0
SMTP_HOST=smtp.mailgun.org
SMTP_USER=postmaster@mg.example.com
SMTP_PASS='[REDACTED:password]'
SESSION_SECRET=[REDACTED:secret]
STRIPE_SECRET_KEY=[REDACTED:secret]
AWS_ACCESS_KEY_ID=[REDACTED:token]
AWS_SECRET_ACCESS_KEY=[REDACTED:secret]
LOG_LEVEL=info
FEATURE_PASSWORD_RESET=true
BACKUP_PASSWORD=
````

`json_config.want`:
````text
# counts: password=2 secret=2 url_password=1
{
  "server": {
    "host": "0.0.0.0",
    "port": 8080
  },
  "database": {
    "url": "postgres://mwtn_app:[REDACTED:url_password]@10.0.3.12:5432/mwtn?sslmode=require",
    "pool": 10
  },
  "auth": {
    "jwtSecret": "[REDACTED:secret]",
    "tokenTtlSeconds": 3600,
    "oauth": {
      "clientId": "mwtn-web",
      "clientSecret": "[REDACTED:secret]"
    }
  },
  "mail": {
    "smtpPassword": "[REDACTED:password]",
    "passwordResetUrl": "[REDACTED:password]"
  },
  "features": {
    "passwordLogin": true,
    "apiKeyHeader": "X-Api-Key"
  }
}
````

Also read `nginx_conf.want` (only the `Authorization "Bearer …"` value masked; both `proxy_pass` lines, `auth_basic`, `auth_basic_user_file`, `ssl_certificate_key` unchanged), `git_config.want` (`credential.helper=store` unchanged, both URL passwords masked), `printenv.want` (`PWD`, `OLDPWD`, `SSH_AUTH_SOCK` unchanged) and `pem_truncated.want` (the certificate stays; the file ends in `[REDACTED:private_key]` with no newline).

- [ ] **Step 7: Run the package green, with the race detector**

Run: `go test -race ./internal/config -count=1 -timeout 180s`
Expected: `ok`. Then `go vet ./...` (clean) and `go test -race -timeout 900s ./...` (all `ok`).

- [ ] **Step 8: Commit**

```bash
git add internal/config/patterns.go internal/config/patterns_test.go internal/config/testdata/redact
git commit -m "feat(config): pattern redaction of secret-looking output

RedactPatterns masks private keys, password/secret keys, credential
headers, URL passwords and known token prefixes as [REDACTED:<kind>]
and counts them; RedactStreams runs it after the vault Redactor.
Corpus of 18 positive and 13 negative real-shaped outputs.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 2: Hub wiring, `ExecResponse.Redacted`, audit counts

**Files:**
- Modify: `internal/broker/audit.go` (`AuditRecord`, after `StderrBytes`)
- Modify: `internal/hub/hub.go` (`ExecResponse`; `run`'s doc comment and its success tail)
- Create: `internal/hub/redact_test.go`

**Interfaces:**
- Consumes: `config.RedactStreams(r *config.Redactor, stdout, stderr string) (string, string, map[string]int)` from Task 1.
- Produces:
  - `hub.ExecResponse.Redacted map[string]int` with JSON tag `redacted,omitempty` (the MCP door's `exec`/`sudoExec` result gains `redacted`).
  - `broker.AuditRecord.Redacted map[string]int` with JSON tag `redacted,omitempty` (counts only, never a value).

`autoExec` (`internal/hub/autoallow.go`) needs no change: both paths end in `run`. Do not touch `base.Command`/`base.Description` (they stay vault-redacted only, per the spec's "Unchanged" list).

- [ ] **Step 1: Write the failing test** `internal/hub/redact_test.go`:

```go
package hub

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/lang315/sshgate/internal/sshx"
)

// Fake secrets of three kinds in one command's output: each must stay out
// of what the AI gets and out of the audit log.
const (
	fakeKeyBody = "MIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gun"
	fakeDBPass  = "Wm4tQz8vLp2Rk7Xs"
	fakeBearer  = "9f8e7d6c5b4a39281706f5e4d3c2b1a0"
)

var secretResult = sshx.ExecResult{
	Stdout: "-----BEGIN RSA PRIVATE KEY-----\n" + fakeKeyBody + "\n-----END RSA PRIVATE KEY-----\nDB_PASSWORD=" + fakeDBPass + "\n",
	Stderr: "> Authorization: Bearer " + fakeBearer + "\n",
}

// checkRedacted asserts what the AI got and the run's exec audit record.
func checkRedacted(t *testing.T, res ExecResponse, raw string, rec map[string]any) {
	t.Helper()
	if res.Stdout != "[REDACTED:private_key]\nDB_PASSWORD=[REDACTED:password]\n" || res.Stderr != "> Authorization: Bearer [REDACTED:auth_header]\n" {
		t.Errorf("AI got stdout %q stderr %q", res.Stdout, res.Stderr)
	}
	if want := map[string]int{"private_key": 1, "password": 1, "auth_header": 1}; !maps.Equal(res.Redacted, want) {
		t.Errorf("Redacted = %v, want %v", res.Redacted, want)
	}
	got, _ := rec["redacted"].(map[string]any) // JSON numbers decode as float64
	if len(got) != 3 || got["private_key"] != float64(1) || got["password"] != float64(1) || got["auth_header"] != float64(1) {
		t.Errorf("audit redacted = %v", rec["redacted"])
	}
	for _, s := range []string{fakeKeyBody, fakeDBPass, fakeBearer} {
		if strings.Contains(res.Stdout+res.Stderr, s) || strings.Contains(raw, s) {
			t.Errorf("fake secret %q reached the AI or the audit log", s)
		}
	}
}

func TestExecRedactsPatternsApproved(t *testing.T) {
	h, path := newHub(t, &fakeExec{res: secretResult})
	go allowFirst(t, h.Broker())
	res, err := h.Exec(context.Background(), ExecRequest{Client: "t", Server: "vis", Command: "cat /etc/app.env"})
	if err != nil {
		t.Fatal(err)
	}
	raw, recs := readAudit(t, path)
	checkRedacted(t, res, raw, recs[len(recs)-1])
}

// With a grant nothing decides: if the run fell through to approval it
// would expire (200 ms) and Exec would fail.
func TestExecRedactsPatternsAutoAllowed(t *testing.T) {
	h, path := newHub(t, &fakeExec{res: secretResult})
	if err := h.SetAutoAllow("vis", "15m"); err != nil {
		t.Fatal(err)
	}
	res, err := h.Exec(context.Background(), ExecRequest{Client: "t", Server: "vis", Command: "cat /etc/app.env"})
	if err != nil {
		t.Fatal(err)
	}
	raw, recs := readAudit(t, path)
	checkRedacted(t, res, raw, findAutoRecord(t, recs))
}

// A vault secret is masked first, as "***", and is not counted. Output with
// nothing else secret has no redacted field: not in the response, not in
// its JSON on the MCP door, not in the audit record.
func TestExecWithoutPatternSecretsHasNoRedacted(t *testing.T) {
	h, path, _ := newEncHub(t, &fakeExec{res: sshx.ExecResult{Stdout: "DB_PASSWORD=s3cr3t-pw\nhello\n"}})
	if err := h.Unlock("pw"); err != nil {
		t.Fatal(err)
	}
	go allowFirst(t, h.Broker())
	res, err := h.Exec(context.Background(), ExecRequest{Server: "enc", Command: "cat app.env"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "DB_PASSWORD=***\nhello\n" || res.Redacted != nil {
		t.Fatalf("got %+v", res)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "redacted") {
		t.Fatalf("MCP door JSON has redacted: %s", b)
	}
	_, recs := readAudit(t, path)
	if _, ok := recs[len(recs)-1]["redacted"]; ok {
		t.Fatalf("audit record has redacted: %v", recs[len(recs)-1])
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/hub -run 'TestExecRedactsPatterns|TestExecWithoutPatternSecrets' -count=1 -timeout 300s`
Expected: FAIL, build error `res.Redacted undefined (type ExecResponse has no field or method Redacted)`.

- [ ] **Step 3: Add the audit field.** In `internal/broker/audit.go`, `AuditRecord`, insert after the `StderrBytes` line:

```go
	Redacted    map[string]int `json:"redacted,omitempty"` // RedactPatterns counts by kind, both streams; never a value
```

(`gofmt` realigns the struct's columns; run `gofmt -w internal/broker/audit.go`.)

- [ ] **Step 4: Add the response field.** In `internal/hub/hub.go` replace `ExecResponse` with:

```go
type ExecResponse struct {
	ExitCode int            `json:"exitCode"`
	Stdout   string         `json:"stdout"`
	Stderr   string         `json:"stderr"`
	Redacted map[string]int `json:"redacted,omitempty"` // RedactPatterns counts by kind, both streams
}
```

- [ ] **Step 5: Mask before the cap in `run`.** In `internal/hub/hub.go`, change `run`'s doc comment first line from

```go
// run executes cmd on name with dc, audits base (Outcome, exit code, sizes,
// reason), and returns what the AI sees. It is the tail of both the approved
// and the auto-allowed path.
```

to

```go
// run executes cmd on name with dc, audits base (Outcome, exit code, sizes,
// reason, redacted counts), and returns what the AI sees. It is the tail of
// both the approved and the auto-allowed path.
```

and replace the success tail

```go
	code := res.ExitCode
	base.Outcome, base.ExitCode = string(broker.Allowed), &code
	h.record(base)
	return ExecResponse{
		ExitCode: res.ExitCode,
		Stdout:   config.CapOutput(red.Redact(res.Stdout), config.DefaultOutputCap),
		Stderr:   config.CapOutput(red.Redact(res.Stderr), config.DefaultOutputCap),
	}, nil
}
```

with

```go
	// Patterns run before the cap: a cap through a PEM block or a
	// password= line would leave a piece that no longer matches.
	stdout, stderr, counts := config.RedactStreams(red, res.Stdout, res.Stderr)
	code := res.ExitCode
	base.Outcome, base.ExitCode, base.Redacted = string(broker.Allowed), &code, counts
	h.record(base)
	return ExecResponse{
		ExitCode: res.ExitCode,
		Stdout:   config.CapOutput(stdout, config.DefaultOutputCap),
		Stderr:   config.CapOutput(stderr, config.DefaultOutputCap),
		Redacted: counts,
	}, nil
}
```

- [ ] **Step 6: Run the new tests and the hub package**

Run: `go test -race ./internal/hub -run 'TestExecRedactsPatterns|TestExecWithoutPatternSecrets' -count=1 -timeout 300s -v`
Expected: PASS (3 tests).
Run: `go test -race ./internal/hub ./internal/broker -count=1 -timeout 600s`
Expected: `ok` for both (existing `TestAllowedRunsAndReturnsStreams`, `TestOutputCappedPerStream`, `TestAuditOutcomes` unchanged and green).

- [ ] **Step 7: Full checks and commit**

Run: `go vet ./... && go test -race -timeout 900s ./...` — all `ok`.

```bash
git add internal/broker/audit.go internal/hub/hub.go internal/hub/redact_test.go
git commit -m "feat(hub): mask secret-looking output before the cap

run applies config.RedactStreams (vault secrets, then RedactPatterns)
before CapOutput on both the approved and the auto-allowed path, and
returns and audits the per-kind counts as redacted.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 3: Note line in the bridge and standalone mode

**Files:**
- Modify: `internal/mcpserver/tools.go` (`FormatExec`, `layoutExec`, new `redactionNote`, standalone `exec` description)
- Modify: `internal/mcpserver/bridge.go` (result decoding, `layoutExec` call, bridge `exec` description, drop the `sshx` import)
- Test: `internal/mcpserver/tools_test.go`, `internal/mcpserver/bridge_test.go`, `cmd/sshgate/e2e_test.go`

**Interfaces:**
- Consumes: `config.RedactKinds`, `config.RedactStreams` (Task 1); the MCP door's `redacted` result field (Task 2).
- Produces:
  - `func layoutExec(exitCode int, stdout, stderr string, redacted map[string]int) string` — when `redacted` has a positive count, the text ends with the note line plus `\n` (a `\n` is inserted first if the text does not already end with one).
  - `func redactionNote(counts map[string]int) string` — `""` when nothing was masked; else `note: sshgate redacted <N> values (<kind> ×<n>, …); the values are withheld from AI clients`, kinds in `config.RedactKinds` order; for N = 1: `note: sshgate redacted 1 value (<kind> ×1); the value is withheld from AI clients`. The `×` is U+00D7.

- [ ] **Step 1: Write the failing tests.** Append to `internal/mcpserver/tools_test.go`:

```go
func TestLayoutExecNote(t *testing.T) {
	got := layoutExec(0, "a\n", "b", map[string]int{"token": 1, "private_key": 1, "password": 2})
	want := "exit code: 0\nstdout:\na\nstderr:\nb\nnote: sshgate redacted 4 values (private_key ×1, password ×2, token ×1); the values are withheld from AI clients\n"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if got := layoutExec(0, "", "", map[string]int{"secret": 1}); got != "exit code: 0\nnote: sshgate redacted 1 value (secret ×1); the value is withheld from AI clients\n" {
		t.Fatalf("singular: %q", got)
	}
	for _, none := range []map[string]int{nil, {}} {
		if got := layoutExec(0, "a\n", "", none); got != "exit code: 0\nstdout:\na\n" {
			t.Fatalf("no note expected: %q", got)
		}
	}
}

// The cap keeps the first and last 32 KiB. A private key whose BEGIN line
// falls in the dropped middle while its body lands in the kept tail would
// no longer match after the cap, so FormatExec must mask first.
func TestFormatExecMasksBeforeCap(t *testing.T) {
	body := "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VJTUt9Us8cKj"
	rest := body + "\n-----END PRIVATE KEY-----\n"
	stdout := strings.Repeat("a", 40000) + "\n-----BEGIN PRIVATE KEY-----\n" + rest + strings.Repeat("b", config.DefaultOutputCap/2-len(rest))
	if capped := config.CapOutput(stdout, config.DefaultOutputCap); !strings.Contains(capped, body) || strings.Contains(capped, "BEGIN") {
		t.Fatal("test premise broken: the cap no longer splits the key from its BEGIN line")
	}
	got := FormatExec(sshx.ExecResult{Stdout: stdout}, config.NewRedactor())
	if strings.Contains(got, body) {
		t.Fatal("key body reached the AI")
	}
	if !strings.HasSuffix(got, "note: sshgate redacted 1 value (private_key ×1); the value is withheld from AI clients\n") {
		t.Fatalf("note missing: ...%q", got[len(got)-200:])
	}
}
```

Append to `internal/mcpserver/bridge_test.go`:

```go
// The hub already masked and counted; the bridge only lays the counts out
// as the final note line, and adds none when the hub sent none.
func TestBridgeExecRedactedNote(t *testing.T) {
	srv := BuildBridgeServer(fakeHub(t, map[string]any{"exitCode": 0, "stdout": "DB_PASSWORD=[REDACTED:password]\n", "stderr": "",
		"redacted": map[string]int{"password": 1, "private_key": 2}}, ""))
	res := callTool(t, srv, "exec", map[string]any{"server": "vis", "command": "cat .env"})
	want := "exit code: 0\nstdout:\nDB_PASSWORD=[REDACTED:password]\nnote: sshgate redacted 3 values (private_key ×2, password ×1); the values are withheld from AI clients\n"
	if res.IsError || text(res) != want {
		t.Fatalf("got %q", text(res))
	}
	srv = BuildBridgeServer(fakeHub(t, map[string]any{"exitCode": 0, "stdout": "hi\n", "stderr": ""}, ""))
	if res := callTool(t, srv, "exec", map[string]any{"server": "vis", "command": "echo hi"}); strings.Contains(text(res), "note:") {
		t.Fatalf("note without redacted: %q", text(res))
	}
}
```

In `cmd/sshgate/e2e_test.go`, `TestEndToEndAutoAllow`, insert after the existing `if n := len(h.Broker().Pending()); n != 0 { … }` block, before the function's closing `}`:

```go
	// Pattern redaction end to end (bridge → MCP door → hub → sshtest):
	// sshtest echoes the command, so the output is one line holding a PEM
	// block, a password= pair and a bearer header. The audit's command
	// field keeps them (it is vault-redacted only), so it is not checked.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "exec", Arguments: map[string]any{"server": "box",
		"command": "echo -----BEGIN RSA PRIVATE KEY----- MIIEowIBAAKCAQEAu1SU1LfV -----END RSA PRIVATE KEY-----; echo password=hunter2x; echo Authorization: Bearer abc123def"}})
	if err != nil || res.IsError {
		t.Fatalf("exec: %v %+v", err, res)
	}
	txt = res.Content[0].(*mcp.TextContent).Text
	for _, s := range []string{"MIIEowIBAAKCAQEAu1SU1LfV", "hunter2x", "abc123def"} {
		if strings.Contains(txt, s) {
			t.Fatalf("%q reached the AI: %q", s, txt)
		}
	}
	if !strings.HasSuffix(txt, "note: sshgate redacted 3 values (private_key ×1, password ×1, auth_header ×1); the values are withheld from AI clients\n") {
		t.Fatalf("note missing: %q", txt)
	}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/mcpserver -run 'TestLayoutExecNote|TestFormatExecMasksBeforeCap|TestBridgeExecRedactedNote' -count=1 -timeout 120s`
Expected: FAIL, build error `too many arguments in call to layoutExec`.
Run: `go test ./cmd/sshgate -run TestEndToEndAutoAllow -count=1 -timeout 300s`
Expected: FAIL with `note missing: "exit code: 0\nstdout:\necho [REDACTED:private_key]; echo password=[REDACTED:password]; …"`: since Task 2 the hub masks and sends `redacted`, but the bridge still drops it. (The binary builds: only the test files call `layoutExec` with four arguments so far.)

- [ ] **Step 3: Implement in `internal/mcpserver/tools.go`.** Replace `FormatExec` and `layoutExec` (from `// FormatExec renders` down to the end of `layoutExec`) with:

```go
// FormatExec renders an exec result for the AI: exit code first, then the
// two streams, each redacted (saved secrets, then RedactPatterns) before it
// is capped independently, then the redaction note.
func FormatExec(res sshx.ExecResult, red *config.Redactor) string {
	stdout, stderr, counts := config.RedactStreams(red, res.Stdout, res.Stderr)
	return layoutExec(res.ExitCode, config.CapOutput(stdout, config.DefaultOutputCap), config.CapOutput(stderr, config.DefaultOutputCap), counts)
}

// layoutExec lays out exit code + stdout/stderr sections, and ends with the
// redaction note when redacted counts anything. It does no redaction and no
// capping: callers that already have redacted/capped text (e.g. the bridge,
// which forwards output the hub already processed) must call this directly
// instead of FormatExec, or the text gets capped twice and an in-flight
// "[truncated N bytes]" marker gets corrupted.
func layoutExec(exitCode int, stdout, stderr string, redacted map[string]int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "exit code: %d\n", exitCode)
	if stdout != "" {
		b.WriteString("stdout:\n")
		b.WriteString(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			b.WriteString("\n")
		}
	}
	if stderr != "" {
		b.WriteString("stderr:\n")
		b.WriteString(stderr)
	}
	if note := redactionNote(redacted); note != "" {
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
		b.WriteString(note + "\n")
	}
	return b.String()
}

// redactionNote tells the AI how many values RedactPatterns masked, by kind
// in config.RedactKinds order; "" when none.
func redactionNote(counts map[string]int) string {
	var parts []string
	total := 0
	for _, k := range config.RedactKinds {
		if n := counts[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s ×%d", k, n))
			total += n
		}
	}
	switch total {
	case 0:
		return ""
	case 1:
		return "note: sshgate redacted 1 value (" + parts[0] + "); the value is withheld from AI clients"
	}
	return fmt.Sprintf("note: sshgate redacted %d values (%s); the values are withheld from AI clients", total, strings.Join(parts, ", "))
}
```

In the standalone `exec` tool description in `BuildServer`, replace

```go
"The result is `exit code:` followed by `stdout:` and `stderr:` sections; a non-zero exit code is a normal result. Each stream is capped at 64 KiB (the middle is cut) and configured secrets are masked. " +
```

with

```go
"The result is `exit code:` followed by `stdout:` and `stderr:` sections; a non-zero exit code is a normal result. Each stream is capped at 64 KiB (the middle is cut). Configured secrets are masked, and values that look like secrets (keys, passwords, tokens) are masked too, as `[REDACTED:<kind>]`, with a final `note:` line saying how many. " +
```

- [ ] **Step 4: Implement in `internal/mcpserver/bridge.go`.** Replace

```go
				var out sshx.ExecResult
				callErr := c.Call(ctx, method, params, &out)
```

with

```go
				var out struct {
					ExitCode int            `json:"exitCode"`
					Stdout   string         `json:"stdout"`
					Stderr   string         `json:"stderr"`
					Redacted map[string]int `json:"redacted"`
				}
				callErr := c.Call(ctx, method, params, &out)
```

replace

```go
				// The hub already redacted and capped each stream; lay out
				// the text as-is, no reprocessing (double-capping would
				// corrupt an in-flight "[truncated N bytes]" marker).
				return textOK(layoutExec(out.ExitCode, out.Stdout, out.Stderr)), nil
```

with

```go
				// The hub already redacted and capped each stream and
				// counted what it masked; lay out the text as-is, no
				// reprocessing (double-capping would corrupt an in-flight
				// "[truncated N bytes]" marker).
				return textOK(layoutExec(out.ExitCode, out.Stdout, out.Stderr, out.Redacted)), nil
```

remove the now-unused import line `"github.com/lang315/sshgate/internal/sshx"`, and in the bridge `exec` tool description replace

```go
"The result is `exit code:` followed by `stdout:` and `stderr:` sections; a non-zero exit code is a normal result, not a tool error. Each stream is capped at 64 KiB (the middle is cut) and saved secrets are masked. " +
```

with

```go
"The result is `exit code:` followed by `stdout:` and `stderr:` sections; a non-zero exit code is a normal result, not a tool error. Each stream is capped at 64 KiB (the middle is cut). Saved secrets are masked, and values that look like secrets (keys, passwords, tokens) are masked too, as `[REDACTED:<kind>]`, with a final `note:` line saying how many; there is no way to get them unmasked. " +
```

- [ ] **Step 5: Run the tests green**

Run: `go test -race ./internal/mcpserver -count=1 -timeout 180s -v -run 'TestLayoutExecNote|TestFormatExecMasksBeforeCap|TestBridgeExecRedactedNote|TestFormatExec|TestBridgeExec'`
Expected: PASS (new tests plus existing `TestFormatExec`, `TestBridgeExecSuccess`, `TestBridgeExecNoDoubleCap`).
Run: `go test -race ./cmd/sshgate -run 'TestEndToEnd' -count=1 -timeout 300s -v`
Expected: PASS (`TestEndToEndApprovalFlow`, `TestEndToEndAutoAllow`).

- [ ] **Step 6: Full checks and commit**

Run: `go vet ./... && go test -race -timeout 900s ./...` — all `ok`.

```bash
git add internal/mcpserver/tools.go internal/mcpserver/bridge.go internal/mcpserver/tools_test.go internal/mcpserver/bridge_test.go cmd/sshgate/e2e_test.go
git commit -m "feat(mcpserver): tell the AI what was redacted

layoutExec ends the result with one note line (kinds in fixed order)
when the hub, or FormatExec in --host mode, masked anything; FormatExec
masks patterns before the cap. Tool descriptions say so.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 4: Opt-in live check `TestLiveRedact`

**Files:**
- Modify: `internal/hub/live_test.go` (extract `openLiveVault` from `TestLiveVaultConnect`; add `TestLiveRedact`, `liveRedact`, `tripwires`, `countsText`, `TestLiveRedactTripwires`)

**Interfaces:**
- Consumes: `config.RedactStreams`, `config.RedactKinds` (Task 1); existing `redactorFor(dcs ...sshx.DialConfig) *config.Redactor`, `(*Hub).Resolve`, `(*Hub).Registry`, `startTermDoor`, `liveOpen`.
- Produces (test-only): `openLiveVault(t *testing.T) (*Hub, *rpc.Client, []string)`, `tripwires(masked string, dc sshx.DialConfig) []string`, `countsText(counts map[string]int) string`.

The live check applies `config.RedactStreams(redactorFor(dc), …)`, the exact call `run` makes, to the output of real hosts; it does not go through `Exec`, so no approval or grant is needed. It runs every command as the vault's user through the host's `Manager` (a su-password host runs them in its root shell, like the AI would).

- [ ] **Step 1: Write the failing unit test** for the tripwires (runs in every `go test`, needs no host). Append to `internal/hub/live_test.go`:

```go
func TestLiveRedactTripwires(t *testing.T) {
	dc := sshx.DialConfig{Password: "vault-pw-1"}
	cases := []struct {
		masked string
		want   []string
	}{
		{"DB_PASSWORD=[REDACTED:password]\nAuthorization: Bearer [REDACTED:auth_header]\n[REDACTED:private_key]\n", nil},
		{"-----BEGIN OPENSSH PRIVATE KEY-----\nb3Bl", []string{"unmasked private key"}},
		{"> authorization: Bearer abc123", []string{"bearer token"}},
		{"id AKIAIOSFODNN7EXAMPLE", []string{"known token prefix"}},
		{"ghp_abcdefghijklmnopqrstuvwxyz", []string{"known token prefix"}},
		{"echo vault-pw-1", []string{"vault secret"}},
	}
	for _, c := range cases {
		if got := tripwires(c.masked, dc); !slices.Equal(got, c.want) {
			t.Errorf("tripwires(%q) = %v, want %v", c.masked, got, c.want)
		}
	}
	// Real-shaped output, masked the way run masks it, trips nothing.
	out, errOut, counts := config.RedactStreams(redactorFor(dc),
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----\nPASS=vault-pw-1\nGITHUB_TOKEN=ghp_R8mK2vQx7LpT4nWz9YbC3dFh6JsA1uEo5GiN\n",
		"> Authorization: Bearer abc123\n")
	if hit := tripwires(out+errOut, dc); hit != nil {
		t.Fatalf("masked output tripped %v", hit)
	}
	if got := countsText(counts); got != "private_key=1 secret=1 auth_header=1" {
		t.Fatalf("countsText = %q", got)
	}
	if got := countsText(nil); got != "nothing" {
		t.Fatalf("countsText(nil) = %q", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/hub -run TestLiveRedactTripwires -count=1 -timeout 120s`
Expected: FAIL, build error `undefined: tripwires` / `undefined: countsText` (and `regexp`/`config`/`sshx` not yet imported).

- [ ] **Step 3: Extract `openLiveVault`.** In `internal/hub/live_test.go`, replace the whole `TestLiveVaultConnect` function (doc comment included) with:

```go
// TestLiveVaultConnect opens a terminal on every server in a copy of your real
// vault (see openLiveVault), so it uses what you saved in the app:
// passwords, passphrases, pins. It runs only when SSHGATE_LIVE_VAULT is set:
// "1" for every server, or a comma-separated list of names.
//
//	SSHGATE_LIVE_VAULT=1 go test ./internal/hub -run TestLiveVaultConnect -v -count=1
func TestLiveVaultConnect(t *testing.T) {
	only := os.Getenv("SSHGATE_LIVE_VAULT")
	if only == "" {
		t.Skip("set SSHGATE_LIVE_VAULT=1 (or a list of server names) to dial the servers in your vault")
	}
	_, c, names := openLiveVault(t)
	n := 0
	for _, name := range names {
		if only != "1" && !slices.Contains(strings.Split(only, ","), name) {
			continue
		}
		n++
		t.Run(name, func(t *testing.T) { liveOpen(t, c, name) })
	}
	if n == 0 {
		t.Fatal("no matching server in the vault")
	}
}

// openLiveVault copies your real vault (SSHGATE_STORE, else
// ~/.config/sshgate/servers.json) to a temp dir, reads its master password
// from the terminal without echo, and unlocks a hub on the copy; the real
// vault is never written. It returns the hub, a UI-door client, and every
// server name in the vault.
func openLiveVault(t *testing.T) (*Hub, *rpc.Client, []string) {
	t.Helper()
	src := os.Getenv("SSHGATE_STORE")
	if src == "" {
		home, _ := os.UserHomeDir()
		src = filepath.Join(home, ".config", "sshgate", "servers.json")
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "servers.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no terminal to read the master password from")
	}
	defer tty.Close()
	fmt.Fprintf(tty, "Master password for %s: ", src)
	pw, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty)
	if err != nil {
		t.Fatal(err)
	}

	h, err := New(Options{StorePath: path, IdleLock: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	t.Cleanup(h.Registry().CloseAll)
	c, _, _ := startTermDoor(t, h)
	ctx := context.Background()
	if err := c.Call(ctx, "unlock", map[string]string{"password": string(pw)}, nil); err != nil {
		t.Fatal(err)
	}
	var servers []struct {
		Name string `json:"name"`
	}
	if err := c.Call(ctx, "servers", nil, &servers); err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = s.Name
	}
	return h, c, names
}
```

- [ ] **Step 4: Add the live check.** Append to `internal/hub/live_test.go` (before `TestLiveRedactTripwires`):

```go
// TestLiveRedact runs a fixed list of read-only commands on every POSIX
// server in a copy of your real vault (see openLiveVault) and masks the
// output exactly as run does before it reaches the AI
// (config.RedactStreams with the host's redactorFor). It prints bytes read
// and mask counts, never output, and fails if masked output still trips a
// tripwire. It runs only when SSHGATE_LIVE_REDACT is set: "1" for every
// server, or a comma-separated list of names.
//
//	SSHGATE_LIVE_REDACT=1 go test ./internal/hub -run TestLiveRedact -v -count=1
func TestLiveRedact(t *testing.T) {
	only := os.Getenv("SSHGATE_LIVE_REDACT")
	if only == "" {
		t.Skip("set SSHGATE_LIVE_REDACT=1 (or a list of server names) to check redaction on the servers in your vault")
	}
	h, _, names := openLiveVault(t)
	n := 0
	for _, name := range names {
		if only != "1" && !slices.Contains(strings.Split(only, ","), name) {
			continue
		}
		n++
		t.Run(name, func(t *testing.T) { liveRedact(t, h, name) })
	}
	if n == 0 {
		t.Fatal("no matching server in the vault")
	}
}

// liveRedactCmds are read-only. /etc/*.env runs on its own so its counts
// show alone (the exit gate: /etc/mwtn.env on fviainboxes-db yields at
// least 2 password masks).
var liveRedactCmds = []string{
	"env",
	"cat /etc/*.env 2>/dev/null",
	"cat ~/.env ~/*/.env ~/.aws/credentials ~/.npmrc ~/.my.cnf ~/.pgpass 2>/dev/null",
	"git config --global --list 2>/dev/null",
	"command -v docker >/dev/null 2>&1 && docker inspect $(docker ps -q) 2>/dev/null",
}

// liveRedact checks one server. Everything it logs is a fixed command
// string, a byte count, or counts by kind.
func liveRedact(t *testing.T, h *Hub, name string) {
	dc, err := h.Resolve(name)
	if err != nil {
		t.Skip("cannot resolve this server; open it in the app first")
	}
	if dc.HostKey == "" {
		t.Skip("no pinned host key; open a terminal on it in the app first")
	}
	red := redactorFor(dc)
	mgr := h.Registry().Get(name, dc)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if res, err := mgr.Exec(ctx, "uname -s"); err != nil || res.ExitCode != 0 || strings.TrimSpace(res.Stdout) == "" {
		t.Skip("not a POSIX host (uname -s failed)")
	}
	total := map[string]int{}
	read := 0
	for i, cmd := range liveRedactCmds {
		res, err := mgr.Exec(ctx, cmd)
		if err != nil {
			t.Errorf("command %d (%q) failed: %s", i, cmd, red.Redact(err.Error()))
			continue
		}
		out, errOut, counts := config.RedactStreams(red, res.Stdout, res.Stderr)
		n := len(res.Stdout) + len(res.Stderr)
		read += n
		for k, c := range counts {
			total[k] += c
		}
		t.Logf("%q: %d bytes, masked %s", cmd, n, countsText(counts))
		for _, trip := range tripwires(out+errOut, dc) {
			t.Errorf("command %d (%q): tripwire %q hit in masked output", i, cmd, trip)
		}
	}
	t.Logf("%s: %d bytes read, masked %s", name, read, countsText(total))
}

var (
	pemTrip    = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY`)
	bearerTrip = regexp.MustCompile(`(?i)authorization:[ \t]*bearer[ \t]+(\S*)`)
	tokenTrip  = regexp.MustCompile(`AKIA[A-Z0-9]{16}|ghp_[A-Za-z0-9]{20,}|glpat-[A-Za-z0-9_-]{20,}|xox[abpr]-[A-Za-z0-9-]{10,}`)
)

// tripwires names what masked output still shows that masking should have
// removed. It never returns the matched text.
func tripwires(masked string, dc sshx.DialConfig) []string {
	var hit []string
	if pemTrip.MatchString(masked) {
		hit = append(hit, "unmasked private key")
	}
	for _, m := range bearerTrip.FindAllStringSubmatch(masked, -1) {
		if !strings.HasPrefix(m[1], "[REDACTED:") {
			hit = append(hit, "bearer token")
			break
		}
	}
	if tokenTrip.MatchString(masked) {
		hit = append(hit, "known token prefix")
	}
	for _, s := range []string{dc.Password, dc.SuPassword, dc.SudoPassword, dc.Passphrase} {
		if s != "" && strings.Contains(masked, s) {
			hit = append(hit, "vault secret")
			break
		}
	}
	return hit
}

// countsText renders counts in config.RedactKinds order, or "nothing".
func countsText(counts map[string]int) string {
	var parts []string
	for _, k := range config.RedactKinds {
		if n := counts[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", k, n))
		}
	}
	if parts == nil {
		return "nothing"
	}
	return strings.Join(parts, " ")
}
```

Add to the import block: `"regexp"` (standard group), and `"github.com/lang315/sshgate/internal/config"`, `"github.com/lang315/sshgate/internal/sshx"` (next to the existing `internal/rpc` import). Run `gofmt -l internal/hub` (must print nothing).

- [ ] **Step 5: Run green, and confirm the live tests skip**

Run: `go test -race ./internal/hub -run 'TestLiveRedactTripwires' -count=1 -timeout 120s -v`
Expected: PASS.
Run: `go test ./internal/hub -run 'TestLive' -count=1 -timeout 120s -v`
Expected: `TestLiveImportConnect`, `TestLiveVaultConnect`, `TestLiveRedact` each `--- SKIP` with their "set SSHGATE_LIVE_…" message; `TestLiveRedactTripwires` PASS.

Do **not** run the real live check: it needs the author's master password on `/dev/tty`. The author runs `SSHGATE_LIVE_REDACT=1 go test ./internal/hub -run TestLiveRedact -v -count=1` after merge; that is the exit gate.

- [ ] **Step 6: Full checks and commit**

Run: `go vet ./... && go test -race -timeout 900s ./...` — all `ok`.

```bash
git add internal/hub/live_test.go
git commit -m "test(hub): opt-in live redaction check on the real vault

TestLiveRedact masks the output of fixed read-only commands on each
POSIX host exactly as run does, prints counts only, and fails on a
tripwire. openLiveVault is shared with TestLiveVaultConnect.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

### Task 5: Docs (README, PRODUCT, CLAUDE.md, ROADMAP) and final checks

**Files:**
- Modify: `README.md` (How it works step 4; Safety model; Tools the AI gets)
- Modify: `PRODUCT.md` (Positioning)
- Modify: `CLAUDE.md` (Commands; `Hub.Exec` order bullet; MCP tools paragraph)
- Modify: `docs/superpowers/ROADMAP.md` (Updated line; slice 4 row split; Findings)

**Interfaces:** none (docs). Describe exactly what Tasks 1–4 built; use the names `RedactPatterns`, `RedactStreams`, `RedactKinds`, `ExecResponse.Redacted`, `layoutExec`, `TestLiveRedact`.

Match each file's voice: short declarative sentences, no marketing. Use the `old_string`s below verbatim (they are the on-disk text).

- [ ] **Step 1: README.md** — three edits.

(a) In "How it works", step 4, replace:

```text
masks every saved secret in the output, caps each stream at 64 KiB
```

with:

```text
masks every saved secret and every value that looks like a secret (private keys, passwords, tokens) in the output, caps each stream at 64 KiB
```

(b) In "Safety model", replace this bullet:

```text
- Saved secrets are masked in all command output. The audit log (`audit.jsonl`, next to the vault) records every request and decision, never command output.
```

with these two bullets:

```markdown
- Saved secrets are masked in all command output. The audit log (`audit.jsonl`, next to the vault) records every request and decision, never command output; an allowed run's record counts what was redacted, by kind.
- Redaction is a seatbelt, not a boundary. Before output reaches the AI, sshgate also masks private key blocks, values after password-, secret- and token-like keys, credential headers (`Authorization`, `Cookie`, `Set-Cookie`, `X-Api-Key`), URL passwords, and well-known tokens (AWS, GitHub, GitLab, Slack, `sk-`, JWTs), always, with no opt-out. It misses a secret with no key and no known prefix (a password alone on a line), `.pgpass` lines, secrets split across lines (except PEM blocks), and anything encoded (`base64`, `xxd`, `rev`, `gzip`). A prompt-injected AI can encode output to get it past redaction; the auto-allow warnings above still apply. To see a real value yourself, use **Send to tab**: the command then runs in your terminal and its output never reaches the AI.
```

(c) In "Tools the AI gets", right after the bullet that begins `- The result is`, add:

```markdown
- Values that look like secrets are replaced by `[REDACTED:<kind>]`, where the kind is `private_key`, `password`, `secret`, `auth_header`, `url_password` or `token`; a key and its separator stay (`DB_PASSWORD=[REDACTED:password]`). The result then ends with one line such as `note: sshgate redacted 3 values (private_key ×1, password ×2); the values are withheld from AI clients`. Saved secrets show as `***` and are not counted. The same applies in standalone `--host` mode.
```

- [ ] **Step 2: PRODUCT.md.** In "Positioning", replace:

```text
This app is both a daily SSH client and the gate.
```

with:

```text
This app is both a daily SSH client and the gate. Output that reaches the AI has saved secrets, and values that look like secrets (keys, passwords, tokens), masked, with a note saying how many; that masking is a seatbelt, not a boundary.
```

- [ ] **Step 3: CLAUDE.md** — three edits.

(a) In "Commands", after the line that starts `SSHGATE_LIVE_VAULT=1 go test`, add:

```text
SSHGATE_LIVE_REDACT=1 go test ./internal/hub -run TestLiveRedact -v -count=1   # opt-in: run fixed read-only commands (env, cat of .env/credential files, git config, docker inspect) on each POSIX server of a copy of your real vault and mask the output as the hub does; prints bytes and per-kind counts only, fails on a tripwire (unmasked PEM, bearer, AKIA/ghp_/glpat-/xox token, vault secret); asks the master password on /dev/tty, never writes the real vault
go test ./internal/config -run TestRedactCorpus -update   # rewrite the redaction goldens (internal/config/testdata/redact/*.want); review every changed "# counts:" line
```

(b) In the `Hub.Exec` order bullet, replace:

```text
→ run with a per-request timeout → redact and cap each stream → audit every branch, including denied/expired/cancelled.
```

with:

```text
→ run with a per-request timeout → redact each stream (vault secrets with `config.Redactor`, then `config.RedactPatterns`, both through `config.RedactStreams`) and only then cap it, since a cap through a PEM block would leave a piece that no longer matches → audit every branch, including denied/expired/cancelled; an allowed run's record carries `redacted`, the per-kind mask counts, never a value.
```

and in the same bullet replace:

```text
The bridge lays the already-processed result out as `exit code:`/`stdout:`/`stderr:` (`layoutExec`) without reprocessing it.
```

with:

```text
The bridge lays the already-processed result out as `exit code:`/`stdout:`/`stderr:` (`layoutExec`) without reprocessing it; `ExecResponse.Redacted` (JSON `redacted`, omitted when empty) becomes one final `note: sshgate redacted N values (kind ×n, …); the values are withheld from AI clients` line, kinds in `config.RedactKinds` order. There is no bypass: to see a real value the human uses Send to tab.
```

(c) In "MCP tools", replace:

```text
Output and errors pass through `config.Redactor`, which masks every secret in the `DialConfig`. Keep that redaction on every path that returns text.
```

with:

```text
Output and errors pass through `config.Redactor`, which masks every secret in the `DialConfig`; output then goes through `config.RedactPatterns` before `CapOutput` (`FormatExec` → `config.RedactStreams`) and ends with the same `note:` line as the bridge. Keep that redaction on every path that returns text.
```

- [ ] **Step 4: docs/superpowers/ROADMAP.md**

1. Replace the line starting `Updated: 2026-09-28 (` with the same text but date `2026-09-30` and, before its closing `)`, append `; slice 4 split into 4a output redaction and 4b audit viewer, audit rotation deferred`.
2. Replace the row starting `| 4 | Egress and audit:` with these two rows:

```markdown
| 4a | Output redaction: a fixed pattern set masks private keys, passwords, secrets, credential headers, URL passwords and known tokens in AI exec output (approved and auto-allowed, and `--host` mode), with a note to the AI and per-kind counts in the audit record | In progress | `specs/2026-09-30-slice4a-output-redaction-design.md` | 1 | Met: the audit log shows the AI asked to `cat /etc/mwtn.env` (passwords) on `fviainboxes-db`, and auto-allow can send such output unreviewed | Corpus tests pass; the author runs `TestLiveRedact` on the real hosts with no tripwire hit and plausible counts (`/etc/mwtn.env` on `fviainboxes-db` yields at least 2 `password` masks) |
| 4b | Audit viewer in the app | Specced | `specs/2026-09-30-slice4b-audit-viewer-design.md` | 4a | Slice 4a done | The Audit tab shows the day's AI requests with outcomes on the real vault; the Auto filter shows only auto-allowed runs; a new request appears without a refresh |
```

3. At the end of "Findings carried forward", add:

```markdown
- Slice 4 split (2026-09-30): audit log rotation is deferred until the log's size matters (the real log is 14 KB); its trigger is a whole-file read that shows in the audit viewer. User-defined patterns, an "Allow unredacted" bypass or per-host opt-out, and entropy-based detection are out of 4a (`specs/2026-09-30-slice4a-output-redaction-design.md`). When 4a's exit gate is met, its row becomes "Done <date of the live check>".
```

- [ ] **Step 5: Verify the docs**

Run: `grep -n "REDACTED:<kind>\|seatbelt" README.md PRODUCT.md` — both files match. `grep -n "RedactPatterns\|TestLiveRedact" CLAUDE.md` — at least 3 lines. `grep -n "^| 4a \|^| 4b " docs/superpowers/ROADMAP.md` — 2 lines, and `grep -c "^| 4 |" docs/superpowers/ROADMAP.md` prints `0`.

- [ ] **Step 6: Final checks (the spec's "Checks")**

Run and keep the output for the PR evidence:
```bash
go vet ./...
go test -race -timeout 900s ./...
(cd desktop && npm ci && npm run typecheck && npm test)
```
Expected: vet clean, every Go package `ok` (say which integration tests skipped for lack of Docker, if any), desktop typecheck and vitest green (unaffected by this slice; run as a guard).

- [ ] **Step 7: Commit**

```bash
git add README.md PRODUCT.md CLAUDE.md docs/superpowers/ROADMAP.md
git commit -m "docs: slice 4a output redaction

README, PRODUCT and CLAUDE.md describe pattern redaction, its note line
and its limits; ROADMAP splits slice 4 into 4a and 4b and defers audit
rotation.

Co-Authored-By: <model> <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01ALpoGjeMRZDpNY1DS7YHx2"
```

---

## Self-review

- **Spec coverage.** Pattern set, table and rules → Task 1 (`patterns.go`, rules table, corpus incl. every listed positive and negative shape). "Where it runs": hub `run` before the cap, both AI paths, `ExecResponse.Redacted`, audit `redacted` → Task 2; bridge note with fixed order, `FormatExec` mask-before-cap, tool descriptions, MCP door field → Task 3 (door field end to end in the e2e). Unchanged items → Global Constraints and Task 2's note. Limits → Task 5 README. Testing: corpus properties (idempotence, counts = markers added, fake secrets absent) → Task 1; hub approved/auto/none → Task 2; bridge/standalone → Task 3; live check with tripwires, counts only, no vault write → Task 4; checks incl. desktop guard → Task 5 Step 6. ROADMAP/README/PRODUCT/CLAUDE.md → Task 5. Marking 4a done waits for the author's live run (recorded in ROADMAP findings).
- **Verified, not guessed.** Before this plan was saved, every code block and heredoc in Tasks 1–4 was extracted from this file and applied to a copy of the repo at `55820ee`: `go vet ./...` is clean, and `go test -race` passes for `internal/config` (86 rule cases, `TestRedactStreams`, the corpus; `-update` reproduces exactly the goldens listed in Task 1 Step 6), `internal/broker`, `internal/hub` (the three new tests, `TestLiveRedactTripwires`; the live tests skip), `internal/mcpserver` and `cmd/sshgate` (`TestEndToEndAutoAllow` with the redaction step).
- **Push protection.** The fakes avoid shapes GitHub push protection flags: AWS ids are the documented `…IOSFODNN7EXAMPLE`, the Stripe key has a `-` inside, Slack ids use 9-digit segments, the npm token is 35 characters, and `ghp_`/`ghs_` fakes fail GitHub's checksum. If a push is still blocked, reshape the flagged fake so it still matches the same rule, update the header list, and re-run `-update` (counts must not change). Measured cost: linear, about 0.25 s per MiB of output (noted in a `ponytail:` comment).
- **Type consistency.** `RedactPatterns(string) (string, map[string]int)`, `RedactStreams(*Redactor, string, string) (string, string, map[string]int)`, `RedactKinds []string`, `ExecResponse.Redacted` / `AuditRecord.Redacted map[string]int` (`redacted,omitempty`), `layoutExec(int, string, string, map[string]int) string`, `redactionNote(map[string]int) string`, `openLiveVault(*testing.T) (*Hub, *rpc.Client, []string)`, `tripwires(string, sshx.DialConfig) []string`, `countsText(map[string]int) string` are used with these exact signatures in every task.
