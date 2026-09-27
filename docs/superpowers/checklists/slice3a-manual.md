# Slice 3a manual checklist

Items 1, 2, 4, 5 and 6 run as an opt-in e2e against a real host, from a copy of your vault (the real one is never written). You type the master password into the app when asked (twice):

    cd desktop && npm run build && SSHGATE_LIVE_HOST=buildpc [SSHGATE_LIVE_DROP=1] [SSHGATE_LIVE_BIG_MB=1024] npx playwright test e2e/live-files.spec.ts

Remote writes stay inside `~/sshgate-e2e-<ms>`, which the test creates and removes (if a run dies midway it prints the folder left behind).

1. Exit gate, on a real host (e.g. buildpc): open Files, browse to a folder, upload a folder, upload it again and choose Skip existing, download it back, rename it, delete it (type `delete`). — `exit gate`
2. Drag a folder from Finder onto a Files tab: it uploads into the folder shown. — `finder drop`, with `SSHGATE_LIVE_DROP=1`: the test reveals the folder in Finder, you drag it.
3. Windows or Linux: Upload files and Upload folder both work. — Linux: CI runs `files.spec.ts` ("Upload folder"). Windows: by hand.
4. Lock the vault during a large download: it keeps running; Cancel still works; a new transfer asks for unlock. — `lock during download` (the part file grows while locked; Cancel after unlock).
5. Save a host's port while a transfer runs: the transfer ends with "server changed"; the editor warned about it first. — `server changed`
6. A folder you cannot read shows "Permission denied". — `permission denied` (skipped when the remote user is root)
