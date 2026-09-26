# Slice 3a manual checklist

1. Exit gate, on a real host (e.g. buildpc): open Files, browse to a folder, upload a folder, upload it again and choose Skip existing, download it back, rename it, delete it (type `delete`).
2. Drag a folder from Finder onto a Files tab: it uploads into the folder shown. (Playwright cannot produce a real dropped file.)
3. Windows or Linux: Upload files and Upload folder both work.
4. Lock the vault during a large download: it keeps running; Cancel still works; a new transfer asks for unlock.
5. Save a host's port while a transfer runs: the transfer ends with "server changed"; the editor warned about it first.
6. A folder you cannot read shows "Permission denied".
