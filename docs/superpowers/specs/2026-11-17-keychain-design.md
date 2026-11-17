# Keychain: Secure Enclave SSH keys (mygo app + hub)

Date: 2026-11-17. Status: draft for the author's review.

## Intent

The author wants a Keychain like Termius's: a sidebar entry, a "New key" button and a card per key. The aim is the most secure place for SSH keys that macOS offers. On 2026-11-17 the author chose:

- Keys are generated inside the Mac's Secure Enclave.
- Touch ID is used **only to unlock the app**, never per signature.
- The Secure Enclave key handle is stored **encrypted in the vault**, so a locked app cannot sign.

Today a host with key auth names a private-key file on disk (`Server.KeyPath`); only its passphrase is in the vault (`EncKeyPassphrase`). Anyone who copies such a file (and its passphrase, if any) can use the key anywhere. A Secure Enclave key can only be used by this Mac's chip, and this design also makes it usable only while the vault is unlocked.

## Goals

- In the app's Keychain view, create named Secure Enclave keys (ECDSA P-256), see them as cards, copy a key's OpenSSH public key, and delete a key that no host uses.
- In the host editor, choose a Keychain key as the host's authentication.
- The hub signs SSH authentication with that key through the existing Swift helper (`sshgate-touchid`). The signing works:
  - with no Touch ID prompt;
  - while the vault is unlocked;
  - for hosts with a live grant under soft lock (auto-allow);
  - nothing new resolves under hard lock (see Anti-goals for work already started).

## Non-goals (ruled out on 2026-11-17)

- Importing existing keys (RSA, ed25519) into the vault. They keep working through `KeyPath`.
- Touch ID per signature or per connection.
- Generating RSA or ed25519 keys; exporting a private key; iCloud sync; renaming a key; using one key on several Macs.
- Keychain keys in `sshgate --host` mode, in `hub --cli` without the app, or on Windows and Linux.
- Any change to the MCP door: AI clients cannot list, create or delete keys.

## Anti-goals

The feature fails, however well it works, if any of these happens:

- The key blob reaches a UI-door reply, an audit record, a log line, an error string, a command-line argument or the clipboard. It travels only from the vault to the hub's memory to the helper's stdin.
- After a hard lock, a new resolve of a Keychain host succeeds. Work resolved before the lock (an approved or auto run in flight, a transfer opening its channel, a `term.open` between resolve and dial) may still dial and sign with the config it already holds. That is the same as decrypted passwords today, and the author chose it on 2026-11-17 over a sign-time lock check (a hook from sshx to the hub) and over closing Keychain connections at lock (which would end terminals and tunnels). The blob is useless off this Mac.
- A key is deleted while a host still uses it, or deleted without a deliberate, delayed click.
- An existing vault, host or ciphertext stops working because of the format change.

## Constraints

- **The vault's `Version` cannot be bumped.** It is part of every secret's AAD (`aadFor`, `internal/config/store.go:44`), so a bump would invalidate every ciphertext.
  - The new data is an additive field.
  - Downgrade: once the file has keys, an older hub that loads it re-marshals the struct without them. Its MAC check then fails, and it refuses the vault (fails closed). An older hub never writes it, since `config.Update` refuses before writing. Write this in the release notes, and say that the older hub's message, "config MAC mismatch — file tampered or wrong master password" (`store.go:69`), is not a wrong password.
- **The app is signed ad hoc** (no Apple Team ID). The helper must not use keychain items. CryptoKit's `SecureEnclave.P256.Signing.PrivateKey` and its `dataRepresentation` blob need no entitlement; the existing `enroll`/`unlock` commands already use the same family (`KeyAgreement`).
- **One signature per new connection.** The hub's `Registry` keeps connections open, so signing happens at dial, not per command. One helper exec per dial is acceptable; measure it in the spike.
- **Hub rules stay:** fail closed, generic errors to the AI, redaction, nothing secret outside the encrypted store.

## Approaches

| | Where the Secure Enclave blob lives | Trade-off |
|---|---|---|
| **1** | **Encrypted in the vault, like other secrets** | **Locked app means no signing; uses the existing AES-GCM per-field crypto; the vault gains a key list.** |
| 2 | A 0600 file beside the vault (like `touchid.json`) | Simpler, but any process of the user can sign while the app is locked: weaker than today's key file with a passphrase. |
| 3 | The macOS Keychain (as TablePro does) | Ad-hoc signing makes it ask "Allow access" after each rebuild, and it does not follow the vault lock. |

The author picked **1**.

## Design

### Vault (`internal/config`)

- New, in `File`: `Keys []Key \`json:"keys,omitempty"\``. Each `Key` has:
  - `Name string`: unique; letters, digits, `.`, `_` and `-`; 1–64 bytes;
  - `Type string`: always `"se-p256"`;
  - `PublicKey string`: an OpenSSH `authorized_keys` line, `ecdsa-sha2-nistp256 AAAA… <name>`;
  - `Created string`: RFC 3339;
  - `EncBlob string`: the CryptoKit `dataRepresentation`, encrypted with `Encrypt(masterKey, "keychain/"+Name+"/blob", aad, blob)`, where `aad = Version|"keychain"|Name|PublicKey`. A blob copied onto another entry, or a public key swapped, fails to decrypt.
- `Server` gains `KeyName string \`json:"keyName,omitempty"\``. There is a new auth value `"keychain"`.
  - `ServerInput` gains `KeyName`. `Validate`: `keychain` needs a `KeyName` that names an existing key. (Validate needs the file, or the check moves into `ApplyServer`; that choice is the builder's.)
  - `ApplyServer`: any auth other than `keychain` clears `KeyName`, as `key` clears `KeyPath`.
  - `Auth` is already in the AAD, so switching auth behaves as it does today.
- Key writes go through `config.Update` only: `AddKey`, `DeleteKey` (refused with `ErrKeyInUse` while any server has that `KeyName`).

### Helper (`app/native/touchid/main.swift`)

Two new commands. Exit codes: 0 ok, 1 other, 3 the key no longer works on this Mac, and a new **5, Secure Enclave unavailable**. Today 4 means "Touch ID not usable now" (`main.swift:5,32`), and `available()` requires biometrics (`main.swift:22-25`). The key commands must not call it, because signing must work on a Mac without Touch ID or with the lid closed. (Builder's call, recorded 2026-11-17.)

- `key-create`: no stdin. It creates `SecureEnclave.P256.Signing.PrivateKey(accessControl:)` with `SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly, [.privateKeyUsage], nil)`, so no biometry. It prints `{"v":1,"blob":<base64 dataRepresentation>,"pub":<base64 x9.63 public key>}`.
- `key-sign`: stdin is `{"blob":…,"data":<base64>}`. It signs `data` (CryptoKit hashes it with SHA-256, as `ecdsa-sha2-nistp256` requires) and prints `{"sig":<base64 raw r||s>}`. A blob from another Mac, or from an erased Secure Enclave, exits 3.

### Hub

- **The helper path:**
  - `hub.Options.KeyHelper string`, set by a new `sshgate hub --keyHelper=<path>` flag.
  - The app passes it in `hubproc.LaunchFor` with the same path `touchid.HelperPath` resolves. Packaged, that is `Resources/sshgate-touchid`.
  - Without it, `keychain` hosts fail with "Secure Enclave keys need the sshgate app on macOS", and `keys.create` fails the same way.
- **`internal/mcpserver/resolve.go`:**
  - `Resolve` for a `keychain` server finds the `Key` by `KeyName`, decrypts `EncBlob` with the deps' key, and sets `DialConfig.SEKeyBlob` and `DialConfig.PublicKey`. With no key it returns the existing locked error.
  - `IsLocked` treats a `keychain` server as encrypted.
  - `resolveAutoLocked` already resolves with `autoKey`, so auto-allow under soft lock works unchanged.
- **`internal/sshx`:**
  - `DialConfig` gains `SEKeyBlob`, `PublicKey` and `KeyHelper`.
  - `ConfigHash` includes `SEKeyBlob`.
  - `authMethods` for `keychain` uses `ssh.PublicKeys` with a signer:
    - `PublicKey()` is parsed from `PublicKey`;
    - `Sign` runs the helper's `key-sign` with a 10 s timeout, the blob on stdin and never in argv, and converts r||s into the SSH ecdsa signature blob;
    - helper exit 3 becomes "this Secure Enclave key does not work on this Mac".
  - The `Redactor` treats `SEKeyBlob` as a secret.
- **Auto-allow:** `snapOf` works from `DialConfig` (`internal/hub/autoallow.go:43`), so it includes `DialConfig.PublicKey`, the key's identity. Changing a host's key then ends its grant ("server changed"). `dialChanged` includes `KeyName`.
- **New UI-door methods** (protocol 13 → 14). Params are strict, and all of them are refused while locked (`writeKeyLocked` for writes):
  - `keys.list` returns `[{name, type, publicKey, created, usedBy: [server]}]`. No blob.
  - `keys.create {name}` runs the helper's `key-create`, then `AddKey` in one `config.Update`. It writes a `kind: "config"` audit record, `action: "keyCreate"`, `key: name`.
  - `keys.delete {name}` returns `ErrKeyInUse` while any server uses the key; otherwise `DeleteKey`. It writes a config record, `action: "keyDelete"`.
  - `servers.save` takes `keyName` in `ServerInput`.
  - `servers` entries carry `keyName` and, for display only, the key's `publicKey`.
- **Unchanged:** the MCP door, `listServers`, import (`import.apply` never sets `keychain`), and `vault.create` (a KDF-less file cannot hold keys; `vault.create` clears `Keys` of a kept file, if any).

### App

- **Sidebar:** a third item, "Keychain", with a drawn key icon, after Hosts and Audit. Selecting it shows the Keychain view, a new `model` tab constant alongside Audit.
- **Keychain view.** It follows the slice-4 look; use the existing card, chip, `termBlock` and `decisionButton` pieces.
  - Header: "Keychain", with **New key** as the primary button.
  - One card per key:
    - the tile shows the key icon;
    - name;
    - "ECDSA P-256 · Secure Enclave";
    - created date;
    - "Used by N hosts" as a chip;
    - the public key in a `termBlock`, selectable;
    - **Copy public key**, using mygo's clipboard write if it has one, otherwise the selectable text alone;
    - **Delete**: `decisionButton(toneDangerFill)` in a confirm dialog that states the key cannot be recovered. It has the 500 ms delay and Cancel as the primary, and is disabled with a tooltip while the key is in use.
  - Empty state: one sentence plus New key.
- **New key dialog:**
  - a name field, validated as in config;
  - Create runs `keys.create`;
  - the reply's public key is shown with Copy, so the author can install it on servers at once.
- **Editor:**
  - the Auth radio gains "Keychain" after "Key file" (today's "Key" option keeps its meaning);
  - Keychain shows a select of key names from `keys.list`, and the chosen key's public key below it;
  - no passphrase field for it.
- **`hubapi`:** the new methods, `KeyName` on `Server` and `ServerInput`, and `ProtocolVersion` 14.

## Order of work

1. **Spike, which gates everything:**
   - add `key-create` and `key-sign` to the helper;
   - on this Mac, create a key, sign while the screen is locked (checking `AfterFirstUnlock`), and time 20 signatures;
   - verify the signature with `x/crypto/ssh` against the public key.
   
   If signing under screen lock fails, stop and bring it back to the author.
   If the median signature (helper exec included) is over 500 ms, the design changes to a long-lived signing helper; bring that back too. (Builder's call: one signature per dial, and the dial holds the connection's lock.)
   Also check that a blob created by one build of the helper still signs after the helper is rebuilt (ad-hoc signature changes).
2. Vault and config with tests: encrypt and decrypt; AAD binding (a swapped blob or public key fails); in-use delete refused; `ApplyServer` clears `KeyName`; an old file without `keys` loads unchanged.
3. sshx signer and resolve, with a fake helper (`testdata`, using a software P-256 key behind the same stdin/stdout contract). Run `sshtestd` with public-key auth (add an option if it lacks one) to prove a full dial. Then the locked, hard-lock and soft-lock-grant cases.
4. Hub methods, protocol 14, audit records, `--keyHelper`, the `hubproc` launch arg.
5. App: sidebar, Keychain view, dialogs, editor, `hubapi`; view tests with the fake door; shots in both themes.
6. Docs: CLAUDE.md (architecture paragraphs, UI-door method list, protocol 14), PRODUCT.md (Keychain moves from planned to built).

## Testing

- **Go:** the cases in the order above, and the existing suites unchanged.
- **The real Secure Enclave:** by hand only, in the spike and once at the end with the packaged app. Create, connect, lock and confirm it no longer connects, soft-lock with a grant and confirm the auto exec connects, delete.
- **The safety tests:** the blob never appears in `servers`, `keys.list`, audit lines or errors. Assert by searching for the blob's base64 in every captured reply and record in a hub test.

## Left to the builder

- Where the `KeyName` existence check lives (`Validate` or `ApplyServer`).
- The exact error strings.
- The icon drawing.
- Whether `keys.list` is called on view entry only, or also after `keys.create` and `servers.save` replies. It must never be on a timer: the idle-lock rule.
- Card layout details within the slice-4 components.
- The fake helper's implementation.
