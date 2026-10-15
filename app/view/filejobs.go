package view

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/lang315/sshgate/app/hubapi"
	"github.com/lang315/sshgate/app/model"
)

// fileJob is one upload, download or delete (Electron's Job). folder is the
// remote folder shown when it started, which it lists again when it ends;
// dest is where it writes, for the conflict dialog. state goes planning,
// then confirm (a dialog waits) or running, then done.
type fileJob struct {
	id, tab, op, label, folder, dest string
	state                            string
	names                            []string // a delete's selected names
	hasFolder                        bool     // a delete selected a folder
	cancelReq                        bool     // Cancel pressed while planning or running
	typed                            string   // the delete dialog's typed word
	planned                          *hubapi.FilesPlanned
	progress                         *hubapi.FilesProgress
	done                             *hubapi.FilesDone
	err                              string
}

var opVerb = map[string]string{"upload": "Upload", "download": "Download", "delete": "Delete"}

func (j *fileJob) title() string { return opVerb[j.op] + " " + model.DisplayText(j.label) }

// startJob plans a job in tab id. Job ids are "f1", "f2", …: the app's own,
// never reused while it runs, so a live id is never planned twice. Only the
// protocol's keys are sent.
func (a *App) startJob(id, op string, sources []string, dest, folder, label string) *fileJob {
	v := a.files[id]
	a.jobSeq++
	j := &fileJob{id: fmt.Sprintf("f%d", a.jobSeq), tab: id, op: op, label: label, folder: folder, dest: dest, state: "planning"}
	v.jobs = append(v.jobs, j)
	a.jobs[j.id] = j
	gen, server := a.hubGen, v.server
	remoteDest := dest
	if op == "delete" {
		remoteDest = ""
	}
	go func() {
		err := a.hub().FilesPlan(context.Background(), j.id, server, op, sources, remoteDest)
		a.Update(func() {
			if err != nil && gen == a.hubGen {
				a.filesCancel(j.id) // the hub may have accepted a plan that timed out here
				a.finishJob(j, nil, err.Error())
			}
		})
	}()
	return j
}

func (a *App) filesCancel(id string) { a.Door.Notify("files.cancel", map[string]string{"id": id}) }

// fileNote carries a job on from its notifications. Only a job this app
// planned and still holds moves; files.run and files.list follow only while
// the ready screen shows, and a job planned behind the lock is cancelled
// (plan Decision 1).
func (a *App) fileNote(ev any) {
	switch e := ev.(type) {
	case hubapi.FilesPlanned:
		j := a.jobs[e.ID]
		if j == nil || j.state != "planning" {
			return
		}
		j.planned = &e
		act := model.PlannedAction(j.op, e.Conflicts.Count, j.cancelReq)
		switch {
		case act == "cancel":
			a.filesCancel(j.id) // a cancel sent while planning can be lost: files.done ends it
		case !a.ready(a.hubGen):
			a.cancelLocked(j)
		case act == "confirm":
			j.state = "confirm"
		default:
			a.runJob(j, "skip")
		}
	case hubapi.FilesProgress:
		if j := a.jobs[e.ID]; j != nil && j.state == "running" {
			j.progress = &e
		}
	case hubapi.FilesDone:
		if j := a.jobs[e.ID]; j != nil {
			a.finishJob(j, &e, "")
		}
	}
}

// runJob sends files.run once: the job leaves planning and confirm here.
func (a *App) runJob(j *fileJob, conflict string) {
	j.state = "running"
	gen := a.hubGen
	go func() {
		err := a.hub().FilesRun(context.Background(), j.id, conflict)
		a.Update(func() {
			if err != nil && gen == a.hubGen && j.state != "done" {
				a.filesCancel(j.id)
				a.finishJob(j, nil, err.Error())
			}
		})
	}()
}

// cancelJob is a row's Cancel while planning or running: files.done ends it.
func (a *App) cancelJob(j *fileJob) {
	j.cancelReq = true
	a.filesCancel(j.id)
}

// cancelConfirm is Cancel on a job waiting for its dialog: it ends here, and
// the hub's files.done finds no job.
func (a *App) cancelConfirm(j *fileJob, why string) {
	a.filesCancel(j.id)
	a.finishJob(j, nil, why)
}

func (a *App) cancelLocked(j *fileJob) { a.cancelConfirm(j, "Cancelled: vault locked") }

// finishJob ends a job once. An upload or delete that ended lists its
// folder again if it is still shown (relistAfterJob), only on the ready
// screen: behind the lock the listing stays as it was until Refresh.
func (a *App) finishJob(j *fileJob, d *hubapi.FilesDone, err string) {
	if j.state == "done" {
		return
	}
	j.state, j.done, j.err = "done", d, err
	delete(a.jobs, j.id)
	v := a.files[j.tab]
	if d == nil || j.op == "download" || v == nil || !a.ready(a.hubGen) {
		return
	}
	switch model.RelistAfterJob(j.folder, v.shown, v.requested, v.loading) {
	case "now":
		a.loadFiles(j.tab, v.shown, nil, false)
	case "after":
		v.relist = j.folder
	}
}

// dropConfirms cancels every job waiting for its dialog when the ready
// screen goes: files.run is refused while locked, and the dialog must not
// come back after an unlock (plan Decision 6).
func (a *App) dropConfirms() {
	for _, j := range a.jobs {
		if j.state == "confirm" {
			a.cancelLocked(j)
		}
	}
}

// transfers counts server's live jobs, for the host editor's warning.
func (a *App) transfers(server string) int {
	n := 0
	for _, j := range a.jobs {
		if v := a.files[j.tab]; v != nil && v.server == server {
			n++
		}
	}
	return n
}

// pickUpload asks for files (or folders) to upload into the folder shown
// when the picker opened, which its title names. The answer is dropped if
// the hub stopped, the vault locked, or the tab closed meanwhile.
func (a *App) pickUpload(id string, dirs bool) {
	v := a.files[id]
	gen, folder, sh := a.hubGen, v.shown, a.shell()
	title := model.UploadTitle(v.server, folder)
	go func() {
		paths, err := sh.PickPaths(title, dirs)
		a.Update(func() {
			if !a.ready(gen) || a.files[id] != v {
				return
			}
			if err != nil {
				v.err = err.Error()
				return
			}
			if len(paths) > 0 {
				a.startJob(id, "upload", paths, folder, folder, model.JobLabel(baseNames(paths)))
			}
		})
	}()
}

// pickDownload asks for a local folder, then downloads what was selected
// when Download was pressed.
func (a *App) pickDownload(id string) {
	v := a.files[id]
	sel := v.selected()
	if len(sel) == 0 {
		return
	}
	var sources, names []string
	for _, x := range sel {
		sources, names = append(sources, model.JoinPath(v.shown, x.Name)), append(names, x.Name)
	}
	gen, folder, sh := a.hubGen, v.shown, a.shell()
	title := model.DownloadTitle(v.server)
	go func() {
		dest, err := sh.PickFolder(title)
		a.Update(func() {
			if !a.ready(gen) || a.files[id] != v {
				return
			}
			if err != nil {
				v.err = err.Error()
				return
			}
			if dest != "" {
				a.startJob(id, "download", sources, dest, folder, model.JobLabel(names))
			}
		})
	}()
}

// removeFiles plans a delete of the selection; its dialog shows once
// files.planned has counted what it reaches.
func (a *App) removeFiles(id string) {
	v := a.files[id]
	sel := v.selected()
	if len(sel) == 0 {
		return
	}
	var paths, names []string
	hasFolder := false
	for _, x := range sel {
		paths, names = append(paths, model.JoinPath(v.shown, x.Name)), append(names, x.Name)
		hasFolder = hasFolder || x.Kind == "dir"
	}
	j := a.startJob(id, "delete", paths, "", v.shown, model.JobLabel(names))
	j.names, j.hasFolder = names, hasFolder
}

// dropFiles uploads what was dropped on tab id into the folder shown. The
// paths come from the OS, so they are checked here: at most MaxDrop, each
// absolute and present (Electron's grantDropped).
func (a *App) dropFiles(id string, paths []string) {
	v := a.files[id]
	if v == nil || len(paths) == 0 || !a.ready(a.hubGen) || !model.ActionsReady(v.listing, v.loading) {
		return
	}
	if len(paths) > model.MaxDrop {
		v.err = fmt.Sprintf("Drop at most %d items at a time.", model.MaxDrop)
		return
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			v.err = "cannot upload " + p + ": not an absolute path"
			return
		}
		if _, err := os.Lstat(p); err != nil {
			v.err = err.Error()
			return
		}
	}
	a.startJob(id, "upload", paths, v.shown, v.shown, model.JobLabel(baseNames(paths)))
}

func baseNames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

// buildTransfers lists tab v's jobs in start order (Electron's Transfers).
func (a *App) buildTransfers(c *ui.Context, v *filesTab) {
	th := c.Theme()
	var click func()
	ui.Column(c).Gap(6).Label("Transfers").Children(func() {
		for _, j := range v.jobs {
			title := j.title()
			ui.Column(c.Key("job-" + j.id)).Gap(2).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, title).SingleLine()
					switch j.state {
					case "planning":
						ui.Text(c, "Checking…").TextColor(th.TextMuted)
					case "confirm":
						ui.Text(c, "Waiting for you").TextColor(th.TextMuted)
					case "running":
						frac := -1.0 // a delete reports no progress
						if j.op != "delete" {
							total, done := int64(1), int64(0)
							if j.planned != nil && j.planned.Bytes > 0 {
								total = j.planned.Bytes
							}
							if j.progress != nil {
								done = j.progress.Done
								if j.progress.Total > 0 {
									total = j.progress.Total
								}
							}
							frac = float64(done) / float64(total)
						}
						ui.Progress(c, frac).Label(title).Width(160)
						if j.progress != nil {
							ui.Text(c, model.ProgressText(j.progress.Done, j.progress.Total, j.progress.File)).Font("monospace").TextColor(th.TextMuted).SingleLine()
						}
					default:
						ui.Text(c, model.DisplayText(model.DoneSummary(j.done, j.err)))
					}
					if j.state != "done" {
						if ui.Button(c, "Cancel").Label("Cancel " + title).Clicked() {
							click = func() {
								if j.state == "confirm" {
									a.cancelConfirm(j, "Cancelled")
								} else {
									a.cancelJob(j)
								}
							}
						}
					} else if ui.Button(c, "×").Label("Dismiss " + title).Tooltip("Dismiss").Clicked() {
						click = func() { v.jobs = deleteJob(v.jobs, j) }
					}
				})
				if j.done != nil && len(j.done.Errors) > 0 {
					for _, m := range j.done.Errors {
						ui.Text(c, model.DisplayText(m)).Font("monospace").TextColor(th.Danger)
					}
					if more := j.done.ErrorCount - len(j.done.Errors); more > 0 {
						ui.Text(c, fmt.Sprintf("and %d more", more)).TextColor(th.TextMuted)
					}
				}
			})
		}
	})
	if click != nil {
		click()
	}
}

func deleteJob(js []*fileJob, j *fileJob) []*fileJob {
	out := js[:0:0]
	for _, x := range js {
		if x != j {
			out = append(out, x)
		}
	}
	return out
}

// buildFileConfirm shows the oldest job of tab v waiting for the person,
// unless a name dialog is open (it waits its turn): the conflict dialog for
// an upload or download, the delete dialog for a delete. The decisive
// buttons are mouse-only and wait AllowDelay after the dialog shows a new
// job; Cancel has the keyboard, Enter and Esc cancel.
func (a *App) buildFileConfirm(c *ui.Context, v *filesTab) {
	var j *fileJob
	for _, x := range v.jobs {
		if x.state == "confirm" && x.planned != nil {
			j = x
			break
		}
	}
	if j == nil || v.name != nil {
		v.confirm = nil // only the active tab builds, so a tab switch keeps the old delay; a dialog dropped here starts a fresh one
		return
	}
	th := c.Theme()
	ok := guard(c, &v.confirm, j.id, a.Now())
	p := *j.planned
	choice := ""
	open := true
	var m ui.Element
	if j.op == "delete" {
		needs := model.DeleteNeedsTyping(j.hasFolder, len(j.names), p)
		m = ui.Modal(c.Key("confirm-"+j.id), &open, func() {
			ui.Column(c).Width(440).Gap(10).Padding(20).Children(func() {
				ui.Text(c, "Delete for good?").Bold().FontSize(16).TextColor(th.Danger)
				for i, n := range j.names {
					if i == 10 {
						ui.Text(c, fmt.Sprintf("and %d more", len(j.names)-10)).TextColor(th.TextMuted)
						break
					}
					ui.Text(c, model.DisplayText(n)).Font("monospace").SingleLine()
				}
				ui.Text(c, model.DeleteSummary(p))
				if needs {
					var in ui.Element
					ui.Field(c, "Type "+model.DeleteWord+" to confirm", func() { in = ui.TextInput(c.Key("typed"), &j.typed).AutoFocus() })
					if in.Submitted() {
						choice = "cancel"
					}
				}
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					if mouseButtonNamed(c, "Delete", "Delete for good", ok && (!needs || j.typed == model.DeleteWord)) {
						choice = "skip"
					}
					cancel := ui.Button(c, "Cancel")
					if !needs {
						cancel = cancel.AutoFocus()
					}
					if cancel.Clicked() {
						choice = "cancel"
					}
				})
			})
		}).Label("Delete files")
	} else {
		n := p.Conflicts.Count
		m = ui.Modal(c.Key("confirm-"+j.id), &open, func() {
			ui.Column(c).Width(440).Gap(10).Padding(20).Children(func() {
				ui.Text(c, model.ConflictTitle(n)).Bold().FontSize(16)
				where := j.dest // a download's local folder
				if j.op == "upload" {
					where = v.server + ":" + j.dest
				}
				ui.Text(c, "in "+model.DisplayText(where)).Font("monospace").TextColor(th.TextMuted)
				for _, s := range p.Conflicts.Sample {
					ui.Text(c, model.DisplayText(s)).Font("monospace").SingleLine()
				}
				if more := n - len(p.Conflicts.Sample); more > 0 {
					ui.Text(c, fmt.Sprintf("and %d more", more)).TextColor(th.TextMuted)
				}
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					if MouseButton(c, "Overwrite all", ok) {
						choice = "overwrite"
					}
					if MouseButton(c, "Skip existing", ok) {
						choice = "skip"
					}
					if ui.Button(c, "Cancel").AutoFocus().Clicked() {
						choice = "cancel"
					}
				})
			})
		}).Label("Files already exist")
	}
	if m.Dismissed() {
		choice = "cancel"
	}
	switch choice {
	case "":
	case "cancel":
		a.cancelConfirm(j, "Cancelled")
		v.ls.Handle.Focus()
	default:
		if model.TrustEnabled(v.confirm.At, a.Now()) {
			a.runJob(j, choice)
			v.ls.Handle.Focus()
		}
	}
}
