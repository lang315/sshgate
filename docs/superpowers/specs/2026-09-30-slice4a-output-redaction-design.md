# sshgate: Output Redaction — Design (Slice 4, part a)

Date: 2026-09-30
Status: Approved in chat 2026-09-30; not yet implemented.
Depends on: `2026-09-24-desktop-app-design.md` (slice 1), `2026-09-28-auto-allow-design.md` (auto-allow and its 2026-09-29 amendment). Everything there still holds unless this document says otherwise.

## Goal

Mask secrets in command output before it reaches an AI client, even when sshgate has never seen those secrets. Today only the secrets stored in the vault for that server are masked (`config.Redactor`). Anything else the command prints goes to the AI verbatim: a `.env` file, a private key, a bearer token in `curl -v`, a database URL with a password.

Entry gate, met: the real audit log shows the AI asked to `cat /etc/mwtn.env`, a file of passwords, on `fviainboxes-db`. The human chose Send to tab that time. With auto-allow and the root-host opt-in (2026-09-29), the same output can reach the AI with no human looking at it.

Exit gate:
- the redaction corpus tests pass;
- the author runs the opt-in live check on their real hosts, with no tripwire hit and plausible counts (on `fviainboxes-db`, `/etc/mwtn.env` yields at least 2 `password` masks).

## Scope

**In:**
- a fixed, built-in pattern set applied to the stdout and stderr of every AI `exec` and `sudo-exec`, whether approved by hand or auto-allowed;
- a note to the AI saying how many values were masked, and of which kinds;
- per-kind counts in the exec audit record;
- the same masking in standalone `--host` mode;
- a corpus of realistic outputs with golden results;
- an opt-in live check.

**Out, recorded in ROADMAP:**
- User-defined patterns, per host or global. There is no real need yet.
- An "Allow unredacted" button, or a per-host opt-out. The decision in chat was to always mask. To see a real value, use Send to tab: the command then runs in the human's own terminal and never reaches the AI.
- Entropy-based detection. Hashes, UUIDs and checksums would be masked and hurt debugging.
- Audit log rotation. The real log is 14 KB, so this waits until size matters.
- The audit viewer. That is slice 4b, in its own spec.

## Decisions made in chat

- **When output matches:** mask the value and tell the AI. Chosen over masking silently, and over withholding the whole output when there is a private key.
- **No bypass:** always mask. There is no per-request or per-host way out.
- **Corpus:** synthesized testdata that is realistic in shape, plus an opt-in live check against a copy of the real vault that prints counts only.
- **Approach:** a fixed Go `regexp` set, with no new dependency. Chosen over user-defined patterns and over entropy detection.

## Where it runs

The output path, before and after:

```
before: sshx → Redactor.Redact (vault secrets) → CapOutput (64 KiB, keeps head and tail) → AI
after:  sshx → Redactor.Redact (vault secrets) → RedactPatterns → CapOutput → AI
```

`RedactPatterns` runs before the cap. The cap can cut a PEM block or a `password=` line in half, and the remaining piece would no longer match.

- **Hub** (`internal/hub/hub.go`, `run`). Both AI paths go through `run`: `Exec` after an approval, and `autoExec`. `run` applies `RedactPatterns` to stdout and stderr, merges the two streams' counts, and returns them in `ExecResponse.Redacted` (`map[string]int`, JSON `redacted`, omitted when empty). The exec audit record gets the same map as `redacted`. It holds counts only, never a value.
- **Bridge** (`internal/mcpserver/bridge.go`) and **standalone** (`FormatExec` in `tools.go`). `layoutExec` gains a `redacted` argument. When it is non-empty, the text ends with one line:

  ```
  note: sshgate redacted 3 values (password ×2, private_key ×1); the values are withheld from AI clients
  ```

  Kinds are listed in a fixed order (the table order below), so the line is deterministic. The tool descriptions, which already say "saved secrets are masked", add: "values that look like secrets (keys, passwords, tokens) are masked too".
- **MCP door protocol.** `exec` and `sudoExec` results gain the optional `redacted` field. The bridge and hub ship together, and an older bridge ignores the unknown field. The UI-door protocol does not change.
- **Unchanged:**
  - the command and description shown to the human in the approval panel, since the human must see the real command to decide;
  - the command and description in the audit log, which still mask only vault secrets;
  - terminals, Send to tab, and Files;
  - the auto-allow feed, which shows the command and not output;
  - error texts. Errors reach the AI only as generic messages today.

## Pattern set (`internal/config/patterns.go`)

`func RedactPatterns(s string) (string, map[string]int)`: pure and stateless. The patterns are compiled once with `regexp.MustCompile`. A match is replaced by `[REDACTED:<kind>]`. Where the secret sits after a key, only the value is replaced, and the key and separator stay, so the AI still sees the structure.

| Kind | Matches | Result |
|---|---|---|
| `private_key` | A whole `-----BEGIN [A-Z ]*PRIVATE KEY( BLOCK)?-----` … `-----END …-----` block: RSA, EC, DSA, OPENSSH, ENCRYPTED, PGP. With no END, it matches from BEGIN to the end of the stream. | `[REDACTED:private_key]` |
| `password` | A key whose name, split on `_`, `-`, `.`, camelCase and case, contains the word `password`, `passwd`, `pwd` or `pass`, followed by `=`, `:` or `: ` and a value. Covers `.env`, `export X=`, YAML, INI, TOML, JSON `"k": "v"`, `k v` in conf files, and `--password=v` / `-p v` style flags in printed command lines. | `DB_PASSWORD=[REDACTED:password]` |
| `secret` | The same key rule, for the words `secret`, `token`, `apikey`/`api_key`, `access_key`, `private_key`, `client_secret`, `auth` (as a whole word, e.g. `_authToken`, `auth:`), and `credential(s)`. | `API_TOKEN=[REDACTED:secret]` |
| `auth_header` | Values of `Authorization:` (`Bearer`, `Basic`, `Token`, anything else), `Proxy-Authorization:`, `Cookie:`, `Set-Cookie:` (the whole value) and `X-Api-Key:`. The scheme word stays. | `Authorization: Bearer [REDACTED:auth_header]` |
| `url_password` | The password in `scheme://user:password@host`. | `postgres://app:[REDACTED:url_password]@db:5432/x` |
| `token` | Bare tokens with a recognizable prefix: AWS `AKIA`/`ASIA` + 16; GitHub `ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_`; GitLab `glpat-`; Slack `xox[abpr]-`; `sk-` / `sk_live_` / `rk_live_` (OpenAI/Stripe style, ≥ 20 chars); a JWT (`eyJ` + `.` + `eyJ` + `.` + signature). | `[REDACTED:token]` |

A key-value match wins over a `token` match inside the same value, so each secret counts once. Several rules keep normal output intact:

- **Whole words.** Key names match on whole words (split as above), case-insensitively. `password`, `passwd` and `pwd` match anywhere in the key. `pass` matches only as the key's last word, so `DB_PASS=` and `pass:` match, but `passFile`, `passive`, `compass` and `bypass` do not. `password_hint` does match: that false positive is accepted.
- **Empty values and placeholders stay:** `***`, `<password>`, `<redacted>`, `changeme`, `xxx…`, `REPLACE_ME`, `null`, `none`, `true`, `false`. The AI can then still tell that a setting is unset.
- **Expressions stay.** A value that looks like code or a template is kept: it contains `(`, `[` or `{`, or starts with `$`, `{{`, `%` or `process.env`. Examples are `os.getenv("X")`, `request.form['password']`, `${DB_PASS}`, `{{ .Values.pass }}`, `%s`. The AI can then read source code. A string literal in code, such as `password = "hunter2"`, is still masked.
- **No entropy rule.** Hashes, UUIDs, commit SHAs, checksums and base64 blobs without a key or prefix stay.
- **Idempotent.** `[REDACTED:…]` is itself a placeholder, so `RedactPatterns(RedactPatterns(x))` equals `RedactPatterns(x)`.

## Limits (stated in README and PRODUCT)

Redaction is a seatbelt, not a boundary. It misses:
- a secret with no key and no known prefix, such as a password alone on a line;
- `.pgpass` lines (`host:port:db:user:pass`);
- secrets split across lines, except PEM blocks;
- anything encoded (`base64`, `xxd`, `rev`, `gzip`).

A prompt-injected AI can encode output to get it past redaction. The auto-allow warnings, and the rule that a grant is not a privilege boundary, stand unchanged.

## Testing

- **Corpus** (`internal/config/testdata/redact/`). Each case is a pair: `<name>.in` and `<name>.want` (golden). `go test ./internal/config -run TestRedactCorpus -update` rewrites the golden files. Every secret in the corpus is fake.
  - **Positive cases:**
    - a `.env` shaped like `/etc/mwtn.env`;
    - `printenv`;
    - `docker inspect`, with its `Env` array;
    - `kubectl get secret -o yaml`, with base64 `data:` values under secret-named keys;
    - `git config --list`, with a credential URL;
    - `curl -v`, with `Authorization` and `Set-Cookie`;
    - PEM keys: RSA, OPENSSH, EC, PGP, encrypted, and one cut off with no END;
    - `~/.aws/credentials`, `.npmrc` `_authToken`, `my.cnf`, and an nginx conf with `proxy_set_header Authorization`;
    - a JSON config;
    - a GitHub token and a JWT in logs.
  - **Negative cases, which must come back byte for byte:**
    - `ls -la`, `df -h`, `ps aux`, `systemctl status`, a `journalctl` excerpt;
    - `git log`, `sha256sum`, `docker ps`, `uname -a`;
    - Python, JS and Go source that reads passwords from the environment or a form;
    - a Helm template.
  - **Properties checked on every case:**
    - idempotence;
    - the counts equal the number of `[REDACTED:` markers added;
    - no `.want` for a positive case still contains the fake secret string listed in the case's header comment.
- **Hub.** Using `sshtest`, a command whose fake output contains a PEM key, a `password=` line and a bearer header returns a masked `ExecResponse` with `redacted` counts. The audit record has the counts and does not contain any of the values. This is tested on both the approved path and the auto-allow path. Output with no secret leaves `redacted` absent.
- **Bridge and standalone.** The `note:` line appears with its fixed kind order, and is absent when nothing was masked. `FormatExec` masks before it caps: a key placed where the cap would cut it is still masked.
- **Live check (opt-in).** `SSHGATE_LIVE_REDACT=1 go test ./internal/hub -run TestLiveRedact -v -count=1` works like `TestLiveVaultConnect`:
  - It opens a copy of the real vault (`SSHGATE_STORE`, else `~/.config/sshgate/servers.json`) and asks for the master password on `/dev/tty`. It never writes the real vault.
  - On each POSIX host (Windows hosts are skipped by `uname`), it runs a fixed list of read-only commands: `env`; `cat` of `/etc/*.env`, `~/.env`, `~/*/.env`, `~/.aws/credentials`, `~/.npmrc`, `~/.my.cnf` and `~/.pgpass`, with errors suppressed; `git config --global --list`; and `docker inspect $(docker ps -q)` when Docker is present.
  - For each host it prints bytes read and the counts by kind. It never prints output.
  - It fails if the masked output still trips a tripwire:
    - an unmasked `BEGIN … PRIVATE KEY` line;
    - `Authorization: Bearer ` followed by anything but a marker;
    - an `AKIA`/`ghp_`/`glpat-`/`xox` token;
    - a vault-stored secret of that host.
- **Checks:** `go vet ./...`; `go test -race ./...`; and the desktop `npm run typecheck && npm test`, which is unaffected but run as a guard.

## ROADMAP, README and PRODUCT changes

- **ROADMAP:**
  - Slice 4 splits into 4a (this spec) and 4b (the audit viewer, its own spec). Rotation is noted as deferred.
  - When the exit gate is met, 4a is marked done with the live check's date.
- **README:**
  - "Tools the AI gets" says that values that look like secrets are masked, and describes the note line.
  - The safety model gains a "Redaction is a seatbelt, not a boundary" bullet with the limits above.
- **PRODUCT.md:** the Positioning paragraph mentions output redaction to the AI.
- **CLAUDE.md:** the `Hub.Exec` order bullet and the "MCP tools" paragraph mention `RedactPatterns` before the cap, and the `redacted` field and note.

## Amendment 2026-09-30 (planning)

The plan (`plans/2026-09-30-slice4a-output-redaction.md`) settled these points. Where this section differs from the sections above, it wins.

- **Note line order.** Kinds in the note line follow the table order: `private_key, password, secret, auth_header, url_password, token`. The earlier example therefore reads `(private_key ×1, password ×2)`. A single match reads "1 value".
- **Key words:**
  - `secret` matches anywhere in the key.
  - `pass`, `token`, `auth`, `apikey`, `credential(s)` and `*_key` match only as the key's last word, so `token_url` and `auth_method` stay.
  - `PWD`, the shell's working directory, is excluded.
  - nginx's `*_pass` directives (`proxy_pass`, `fastcgi_pass` …), which take upstream URLs, are excluded.
- **Conf lines.** The `key value` form, a space separator with no `=` or `:`, applies to password keys only. A bare `-p value` flag is not covered, only `--password=value`.
- **`token` kind.** A token must contain a digit, so words that merely start with a prefix, such as `sk-learn`, stay.
- **Corpus.** Each `.in` starts with a `# redact-test:` header line: the fake secrets for a positive case, or `negative`. Negative cases have no `.want`; they must come back byte for byte. A positive case's `.want` starts with a `# counts:` line, which the test checks.
- **Hub tests.** They use the hub's fake exec seam rather than `sshtest`, because `sshtest` cannot return a multi-line PEM. A full-stack `sshtest` step in `TestEndToEndAutoAllow` checks the note line end to end.
- **Fake secrets.** They are shaped so that GitHub push protection does not flag them, for example AWS's documented `EXAMPLE` keys.

## Amendment 2026-09-30 (Task 1 review)

This section wins over the sections above.

- **Separators.** `=>` is a separator (PHP, Ruby, Perl). An unquoted value never starts with `>`.
- **Values that are kept.** A value is kept only if it is one of the following:
  - a code expression: an identifier or dotted path followed by `(` or `[`;
  - a value starting with `${`, `{{`, `$` + an identifier, `%` + a printf verb (`s`, `v`, `d`, `q`), or `process.env.`;
  - an empty value or a placeholder. The placeholders now also include `yes`, `no`, `on` and `off`, so `PasswordAuthentication no` stays;
  - an absolute or home path: it starts with `/` or `~/`, has another `/`, and has no whitespace.

  Other values that contain brackets or start with `$` or `%` are masked, because strong passwords contain them.
- **Nested pairs.** When a key is rejected, or its value is kept, its value is scanned again for key-value pairs. `sort_key=name&api_key=…` and `"authDb": "Server=db;Password=…"` are therefore masked.
- **Unterminated quotes.** A quoted value with no closing quote is masked to the end of its line.
- **Orphan END.** An `-----END … PRIVATE KEY-----` line with no BEGIN of that kind before it is masked from the start of the text. This covers a window that starts inside a key.
- **Added to the password and token rules:** `requirepass` and `masterauth` (redis) as password keys; `sk_test_` and `rk_test_` as token prefixes.
- **Cost.** Masking is linear in the output size. Only windows of the raw output around what the cap keeps are masked: the head and tail, each widened by 64 KiB. The truncation marker still counts the bytes dropped from the raw output.
- **Still not covered (Limits):** WordPress `define('DB_PASSWORD', …)`, XML `<password>`, `curl -u user:pw`, `mysql -pPW`, a one-line `.netrc`, `/etc/shadow` hashes, and an unencoded `@` inside a URL password.
