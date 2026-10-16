package model

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lang315/sshgate/app/hubapi"
)

// DeleteWord is what a recursive delete asks the person to type.
const DeleteWord = "delete"

// MaxDrop is how many paths one drop may hold (Electron's MAX_DROP; the hub
// takes 1 to 1000 sources).
const MaxDrop = 1000

// SortEntries returns es sorted for the file list (desktop/src/renderer/
// files.ts): folders first, then the rest, each by key ("name", "size",
// "mtime" or "mode"), descending when desc; ties by name, ascending.
func SortEntries(es []hubapi.FileEntry, key string, desc bool) []hubapi.FileEntry {
	out := slices.Clone(es)
	slices.SortStableFunc(out, func(a, b hubapi.FileEntry) int {
		if ad, bd := a.Kind == "dir", b.Kind == "dir"; ad != bd {
			if ad {
				return -1
			}
			return 1
		}
		var c int
		switch key {
		case "size":
			c = cmp.Compare(a.Size, b.Size)
		case "mtime":
			c = cmp.Compare(a.Mtime, b.Mtime)
		case "mode":
			c = cmp.Compare(a.Mode, b.Mode)
		default:
			c = strings.Compare(a.Name, b.Name)
		}
		if desc {
			c = -c
		}
		if c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// VisibleEntries drops dot names unless showHidden.
func VisibleEntries(es []hubapi.FileEntry, showHidden bool) []hubapi.FileEntry {
	if showHidden {
		return es
	}
	return slices.DeleteFunc(slices.Clone(es), func(x hubapi.FileEntry) bool { return strings.HasPrefix(x.Name, ".") })
}

func FormatSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	unit := ""
	for _, u := range []string{"KB", "MB", "GB", "TB", "PB"} {
		v, unit = v/1024, u
		if v < 1024 {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", v, unit)
}

// FormatMode is ls's mode column: d, l or -, then the rwx of the low 9 bits.
func FormatMode(kind string, mode uint32) string {
	b := []byte("-")
	switch kind {
	case "dir":
		b[0] = 'd'
	case "link":
		b[0] = 'l'
	}
	for i := 8; i >= 0; i-- {
		if mode&(1<<i) != 0 {
			b = append(b, "rwx"[(8-i)%3])
		} else {
			b = append(b, '-')
		}
	}
	return string(b)
}

// FormatTime shows Unix seconds as "YYYY-MM-DD HH:MM" in loc.
func FormatTime(sec int64, loc *time.Location) string {
	return time.Unix(sec, 0).In(loc).Format("2006-01-02 15:04")
}

func JoinPath(dir, name string) string {
	if strings.HasSuffix(dir, "/") {
		return dir + name
	}
	return dir + "/" + name
}

// ParentPath is p's folder; "" and "/" have none.
func ParentPath(p string) (string, bool) {
	if p == "" || p == "/" {
		return "", false
	}
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/", true
	}
	return p[:i], true
}

// NameError is why name cannot be a new folder or rename target, or "".
func NameError(name string) string {
	switch {
	case name == "" || name == "." || name == "..":
		return "Enter a name."
	case strings.ContainsAny(name, "/\\\x00"):
		return `A name cannot contain /, \, or NUL.`
	}
	return ""
}

// DeleteNeedsTyping: a delete that reaches past what was selected (a
// folder with contents, or one the walk could not read) needs DeleteWord.
func DeleteNeedsTyping(hasFolder bool, selected int, p hubapi.FilesPlanned) bool {
	return hasFolder && p.Files+p.Dirs+p.Links+p.ErrorCount > selected
}

// PruneSelection keeps the selected names still listed.
func PruneSelection(sel []string, entries []hubapi.FileEntry) []string {
	var out []string
	for _, n := range sel {
		if slices.ContainsFunc(entries, func(x hubapi.FileEntry) bool { return x.Name == n }) {
			out = append(out, n)
		}
	}
	return out
}

// ActionsReady: the folder actions need a listing and no load in flight.
func ActionsReady(l *hubapi.FileListing, loading bool) bool { return l != nil && !loading }

// RelistAfterJob says whether an upload or delete that ended in jobFolder
// lists the folder shown again: "now", "after" the load in flight, or "no"
// (the person moved on).
func RelistAfterJob(jobFolder, shown, requested string, loading bool) string {
	switch {
	case !loading && jobFolder == shown && requested == shown:
		return "now"
	case loading && requested == jobFolder:
		return "after"
	}
	return "no"
}

// PlannedAction is what files.planned does with a job: "cancel" (a Cancel
// was pressed while planning), "confirm" (a delete, or conflicts: the
// person chooses) or "skip" (run at once; nothing to overwrite).
func PlannedAction(op string, conflicts int, cancelRequested bool) string {
	switch {
	case cancelRequested:
		return "cancel"
	case op == "delete" || conflicts > 0:
		return "confirm"
	}
	return "skip"
}

// DoneSummary is a finished job's line: the hub's counts, or err for a job
// that ended without files.done (a refused plan, a hub restart).
func DoneSummary(d *hubapi.FilesDone, err string) string {
	if d == nil {
		return err
	}
	verb, n := "Uploaded", d.Copied
	switch d.Op {
	case "download":
		verb = "Downloaded"
	case "delete":
		verb, n = "Deleted", d.Deleted
	}
	parts := []string{fmt.Sprintf("%s %d", verb, n)}
	if d.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("skipped %d", d.Skipped))
	}
	if d.ErrorCount > 0 {
		parts = append(parts, fmt.Sprintf("%d error%s", d.ErrorCount, plural(d.ErrorCount)))
	}
	head := ""
	switch {
	case d.Cancelled && (d.Reason == "" || d.Reason == "cancelled"):
		head = "Cancelled"
	case d.Cancelled:
		head = "Cancelled: " + d.Reason
	case d.Reason != "":
		head = "Failed: " + d.Reason
	default:
		return strings.Join(parts, " · ")
	}
	parts[0] = strings.ToLower(parts[0][:1]) + parts[0][1:]
	return head + " · " + strings.Join(parts, " · ")
}

// JobLabel names a job's items: the one name, or "N items".
func JobLabel(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	return fmt.Sprintf("%d items", len(names))
}

func ConflictTitle(n int) string {
	if n == 1 {
		return "1 file already exists"
	}
	return fmt.Sprintf("%d files already exist", n)
}

// DeleteSummary is the delete dialog's count line.
func DeleteSummary(p hubapi.FilesPlanned) string {
	return fmt.Sprintf("%d file%s, %d folder%s, %d link%s · %s. There is no undo.",
		p.Files, plural(p.Files), p.Dirs, plural(p.Dirs), p.Links, plural(p.Links), FormatSize(p.Bytes))
}

// BadNote says how many names files.list left out.
func BadNote(n int) string {
	if n == 1 {
		return "1 name is not shown: not safe to display or copy."
	}
	return fmt.Sprintf("%d names are not shown: not safe to display or copy.", n)
}

// UploadTitle and DownloadTitle title the native pickers.
func UploadTitle(server, folder string) string {
	return "Upload to " + DisplayText(server) + ":" + DisplayText(folder)
}

func DownloadTitle(server string) string { return "Download from " + DisplayText(server) + " to\u2026" }

// ProgressText is a running job's "done of total · file".
func ProgressText(done, total int64, file string) string {
	return FormatSize(done) + " of " + FormatSize(total) + " · " + DisplayText(file)
}
