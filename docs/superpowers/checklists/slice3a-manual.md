# Slice 3a manual checklist

Items 1, 2, 4, 5 and 6 run as an opt-in e2e against a real host, from a copy of your vault (the real one is never written). Nothing is typed into a terminal — every remote step goes through the Files tab (SFTP) — so this works against OpenSSH on Windows hosts too. You type the master password into the app when asked (up to three times: start, once more if the lock test's download finishes before you unlock, and once more if the port-change test ran, to reach the scratch folder again for cleanup):

    cd desktop && npm run build && SSHGATE_LIVE_HOST=buildpc [SSHGATE_LIVE_DROP=1] [SSHGATE_LIVE_BIG_MB=128] [SSHGATE_LIVE_DENIED=/some/unreadable/folder] npx playwright test e2e/live-files.spec.ts

Remote writes stay inside `<home>/sshgate-e2e-<ms>`, which the test creates and removes (if a run dies midway it prints the folder left behind).

1. Exit gate, on a real host (e.g. buildpc): open Files, browse to a folder, upload a folder, upload it again and choose Skip existing, download it back, rename it, delete it (type `delete`). — `exit gate`
2. Drag a folder from Finder onto a Files tab: it uploads into the folder shown. — `finder drop`, with `SSHGATE_LIVE_DROP=1`: the test reveals the folder in Finder, you drag it.
3. Windows or Linux: Upload files and Upload folder both work. — Linux: CI runs `files.spec.ts` ("Upload folder"). Windows: by hand.
4. Lock the vault during a large download: it keeps running; Cancel still works; a new transfer asks for unlock. — `lock during download` (the part file grows while locked; Cancel after unlock).
5. Save a host's port while a transfer runs: the transfer ends with "server changed"; the editor warned about it first. — `server changed`
6. A folder you cannot read shows "Permission denied". — `permission denied` (skipped when the remote user is root; on a Windows host, skipped unless `SSHGATE_LIVE_DENIED` names a folder you cannot read there)
