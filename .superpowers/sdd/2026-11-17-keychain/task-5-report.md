# Task 5 report: App Keychain view, editor, sidebar

## Implemented
- hubapi: `Key`, `KeysList/KeysCreate/KeysDelete`, `KeyName`/`KeyPublicKey` on Server, `KeyName` on ServerInput.
- model: `KeychainTab`, `ShowKeychain`, `HostDraft.KeyName`, `ToInput` sends keyName only for keychain auth, `KeyNameError`.
- view/keychain.go: key list state, Keychain view (cards, chip, termBlock, Copy, guarded Delete), New key dialog, delete confirm (Cancel primary+AutoFocus, 500 ms decisionButton), editor Keychain auth (select + public key, no passphrase).
- frame/home/icons/editor/app wired. keys.list is called on entering the view (sidebar click), after create/delete, and after servers.save replies (only if already loaded), plus once when the editor needs it with nothing loaded. Never from a timer or redraw.
- Shots: keychain, keychain-empty, keychain-new, editor-keychain (light+dark), viewed.

## TDD
RED (before keychain.go): all 6 tests failed with `ui: no element shows "Keychain"`.
GREEN: the 6 tests pass; full run `go test -race -timeout 20m ./...` in app/: all ok (view 736s), exit=0. go vet and gofmt clean.

## Concerns
- Editor Keychain radio with no keys: no select shown, Save disabled until a key exists.
- keys.list is also called by the editor on first use if the list is not loaded (a user action, not a timer).
- Used the key list "Not used" chip for zero usage.
