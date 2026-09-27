package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"

	"github.com/lang315/sshgate/internal/broker"
	"github.com/lang315/sshgate/internal/files"
	"github.com/lang315/sshgate/internal/rpc"
	"github.com/lang315/sshgate/internal/sshx"
)

// Test knobs.
var (
	planTTL       = 10 * time.Minute       // a planned job not run by then is dropped
	progressEvery = 250 * time.Millisecond // files.progress per job at most this often
	// cancelGrace: then a stuck job's SFTP channel is closed. Kept below the
	// door's 5s close wait so a stuck job's end audit is still written inside it.
	cancelGrace = 2 * time.Second
	// afterExpiryDecided runs once plan expiry has decided to end a job, outside
	// j.mu: a test drives a files.run landing right there.
	afterExpiryDecided func()
)

// jobSet is every job in the hub, so a server change can end its jobs.
type jobSet struct {
	mu  sync.Mutex
	all map[*fileJob]struct{}
}

func newJobSet() *jobSet { return &jobSet{all: map[*fileJob]struct{}{}} }

func (s *jobSet) add(j *fileJob)    { s.mu.Lock(); s.all[j] = struct{}{}; s.mu.Unlock() }
func (s *jobSet) remove(j *fileJob) { s.mu.Lock(); delete(s.all, j); s.mu.Unlock() }

// endServer ends name's jobs. Callers run it before closing the server's
// connection, so "server changed" is the reason reported, not a lost connection.
func (s *jobSet) endServer(name, reason string) {
	s.mu.Lock()
	var js []*fileJob
	for j := range s.all {
		if j.server == name {
			js = append(js, j)
		}
	}
	s.mu.Unlock()
	for _, j := range js {
		j.end(reason)
	}
}

// fileJob is one plan → run → done. state moves planning → planned →
// running → done under mu; reason, once set, is why it ended early.
type fileJob struct {
	id, server string
	op         files.Op
	dc         sshx.DialConfig
	h          *Hub
	door       *fileDoor
	c          *sftp.Client
	ctx        context.Context
	cancel     context.CancelFunc

	mu       sync.Mutex
	state    string
	reason   string
	plan     *files.Plan
	conflict string
	expire   *time.Timer
	once     sync.Once
	done     chan struct{}
}

// fileDoor is one UI door's jobs, by the client's id.
type fileDoor struct {
	h    *Hub
	s    *rpc.Server
	mu   sync.Mutex
	jobs map[string]*fileJob
}

func (d *fileDoor) get(id string) *fileJob {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.jobs[id]
}

// end stops the job for reason. It never blocks on the network: a planned
// job finishes on another goroutine; a planning or running one is cancelled
// (ctx alone does not stop pkg/sftp's calls) and, if it has not finished
// within cancelGrace, its SFTP channel is closed to force it. j.c may still
// be nil while planning (files.plan assigns it, under j.mu, only once
// mgr.NewSFTP returns), so the grace closure reads it under j.mu and
// tolerates nil.
func (j *fileJob) end(reason string) { j.endIf(reason, "") }

// endIf is end, but only while the job's state is onlyIn ("" for any). The
// state check and setting reason share one j.mu section, so a files.run
// cannot slip in between: it either ran first (and endIf does nothing) or
// sees reason set and refuses. It reports whether it ended the job.
func (j *fileJob) endIf(reason, onlyIn string) bool {
	j.mu.Lock()
	if onlyIn != "" && j.state != onlyIn {
		j.mu.Unlock()
		return false
	}
	if j.reason == "" {
		j.reason = reason
	}
	st := j.state
	j.mu.Unlock()
	j.cancel()
	switch st {
	case "planned":
		go j.finish(files.Result{Cancelled: true}, nil)
	case "planning", "running":
		time.AfterFunc(cancelGrace, func() {
			select {
			case <-j.done:
				return
			default:
			}
			j.mu.Lock()
			c := j.c
			j.mu.Unlock()
			if c != nil {
				c.Close()
			}
		})
	}
	return true
}

func (j *fileJob) planWalk(sources []string, dest string) {
	var p *files.Plan
	var err error
	switch j.op {
	case files.OpUpload:
		p, err = files.PlanUpload(j.ctx, j.c, sources, dest)
	case files.OpDownload:
		p, err = files.PlanDownload(j.ctx, j.c, sources, dest)
	default:
		p, err = files.PlanDelete(j.ctx, j.c, sources)
	}
	if err != nil {
		j.finish(files.Result{Cancelled: j.ctx.Err() != nil}, err)
		return
	}
	j.mu.Lock()
	if j.reason != "" { // ended while planning
		j.mu.Unlock()
		p.Close()
		j.finish(files.Result{Cancelled: true}, nil)
		return
	}
	j.plan, j.state = p, "planned"
	j.expire = time.AfterFunc(planTTL, j.expireIfPlanned)
	// files.planned is sent while still holding j.mu: finish (from end or a
	// run failure) and run both take j.mu first, so neither files.done nor a
	// run can happen until this notification has gone out. Notify only takes
	// the rpc write lock, never j.mu, so this cannot deadlock against them.
	j.door.s.Notify("files.planned", map[string]any{
		"id": j.id, "files": p.Files, "dirs": p.Dirs, "links": p.Links, "bytes": p.Bytes,
		"conflicts":  map[string]any{"count": p.Conflicts, "sample": nonNil(p.Sample)},
		"errorCount": p.Errors.Count, "errors": nonNil(p.Errors.List),
	})
	j.mu.Unlock()
}

// expireIfPlanned ends the job for "plan expired", but only if it is still
// planned: run's expire.Stop() can race a timer already firing, and that
// firing callback must not cancel a transfer that has since started.
func (j *fileJob) expireIfPlanned() {
	hook := afterExpiryDecided // read before endIf starts finish, so a test's reset cannot race it
	if j.endIf("plan expired", "planned") && hook != nil {
		hook()
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// run starts a planned job; conflict is "overwrite" or "skip".
func (j *fileJob) run(conflict string) error {
	j.mu.Lock()
	if j.state != "planned" || j.reason != "" {
		j.mu.Unlock()
		return fmt.Errorf("job %q is not ready to run", j.id)
	}
	j.state, j.conflict = "running", conflict
	j.expire.Stop()
	p := j.plan
	j.mu.Unlock()
	j.h.auditFile(broker.FileRecord{Phase: "start", Action: string(j.op), Server: j.server, Host: j.dc.Host, Port: j.dc.Port,
		Remote: p.Remote, Local: p.Local, Conflict: conflict, Files: p.Files, Bytes: p.Bytes})
	go func() {
		// last is a unix-nanos timestamp: pkg/sftp's concurrent upload writer
		// may call progress from another goroutine than Run's caller.
		var last atomic.Int64
		r := p.Run(j.ctx, j.c, conflict == "overwrite", func(file string, done, total int64) {
			now := time.Now().UnixNano()
			if now-last.Load() < int64(progressEvery) {
				return
			}
			last.Store(now)
			j.door.s.Notify("files.progress", map[string]any{"id": j.id, "file": file, "done": done, "total": total})
		})
		j.finish(r, nil)
	}()
	return nil
}

// finish runs once: it releases the job, writes the end audit record for a
// job that ran, and sends files.done.
func (j *fileJob) finish(r files.Result, planErr error) {
	j.once.Do(func() {
		j.mu.Lock()
		ran, p, reason := j.state == "running", j.plan, j.reason
		if j.expire != nil {
			j.expire.Stop()
		}
		j.state = "done"
		j.mu.Unlock()
		j.cancel()
		if p != nil {
			p.Close()
		}
		j.c.Close()
		j.h.files.remove(j)
		j.door.mu.Lock()
		if j.door.jobs[j.id] == j {
			delete(j.door.jobs, j.id)
		}
		j.door.mu.Unlock()
		r.Cancelled = r.Cancelled || reason != "" // ended early by end()
		switch {
		case reason != "":
		case planErr != nil:
			reason = planErr.Error()
		case r.Fatal != nil:
			reason = r.Fatal.Error()
		}
		if ran {
			j.h.auditFile(broker.FileRecord{Phase: "end", Action: string(j.op), Server: j.server, Host: j.dc.Host, Port: j.dc.Port,
				Remote: p.Remote, Local: p.Local, Conflict: j.conflict, Files: r.Copied + r.Deleted, Bytes: r.Bytes,
				Skipped: r.Skipped, ErrorCount: r.Errors.Count, Cancelled: r.Cancelled, Reason: reason})
		}
		j.door.s.Notify("files.done", map[string]any{
			"id": j.id, "op": j.op, "copied": r.Copied, "skipped": r.Skipped, "deleted": r.Deleted, "bytes": r.Bytes,
			"errorCount": r.Errors.Count, "errors": nonNil(r.Errors.List), "cancelled": r.Cancelled, "reason": reason,
		})
		close(j.done)
	})
}

// registerJobMethods adds files.plan/run/cancel/cancelAll for one door and
// returns its close func: cancel every job, then wait up to 5 s.
func registerJobMethods(s *rpc.Server, h *Hub) (closeAll func()) {
	d := &fileDoor{h: h, s: s, jobs: map[string]*fileJob{}}
	s.HandleRequest("files.plan", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			ID      string   `json:"id"`
			Server  string   `json:"server"`
			Op      files.Op `json:"op"`
			Sources []string `json:"sources"`
			Dest    string   `json:"dest"`
		}
		if err := strictParams(raw, &p, "id", "server", "op", "sources", "dest"); err != nil {
			return nil, err
		}
		switch {
		case !termIDRe.MatchString(p.ID):
			return nil, &rpc.Error{Code: -32602, Message: "id must be 1-64 characters of A-Z a-z 0-9 _ -"}
		case p.Op != files.OpUpload && p.Op != files.OpDownload && p.Op != files.OpDelete:
			return nil, &rpc.Error{Code: -32602, Message: "op must be upload, download, or delete"}
		case len(p.Sources) == 0 || len(p.Sources) > 1000:
			return nil, &rpc.Error{Code: -32602, Message: "sources: 1 to 1000 paths"}
		case (p.Op == files.OpDelete) != (p.Dest == ""):
			return nil, &rpc.Error{Code: -32602, Message: "dest is required for upload and download, and absent for delete"}
		}
		mgr, dc, err := h.pinnedClient(p.Server)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		j := &fileJob{id: p.ID, server: p.Server, op: p.Op, dc: dc, h: h, door: d, ctx: ctx, cancel: cancel, state: "planning", done: make(chan struct{})}
		d.mu.Lock()
		_, dup := d.jobs[p.ID]
		if !dup {
			d.jobs[p.ID] = j
		}
		d.mu.Unlock()
		if dup {
			cancel()
			return nil, fmt.Errorf("job %q already exists", p.ID)
		}
		// Added before NewSFTP (which can itself block on a stalled server):
		// a server change arriving during setup must still end this job, not
		// just ones that reached "planned" or "running".
		h.files.add(j)
		c, err := mgr.NewSFTP()
		if err != nil {
			cancel()
			d.mu.Lock()
			delete(d.jobs, p.ID)
			d.mu.Unlock()
			h.files.remove(j)
			close(j.done) // a door closing meanwhile must not wait for it
			return nil, err
		}
		j.mu.Lock()
		j.c = c
		j.mu.Unlock()
		go j.planWalk(p.Sources, p.Dest)
		return map[string]any{}, nil
	})
	s.HandleRequest("files.run", func(_ context.Context, raw json.RawMessage) (any, error) {
		h.touch()
		var p struct {
			ID       string `json:"id"`
			Conflict string `json:"conflict"`
		}
		if err := strictParams(raw, &p, "id", "conflict"); err != nil {
			return nil, err
		}
		if p.Conflict != "overwrite" && p.Conflict != "skip" {
			return nil, &rpc.Error{Code: -32602, Message: "conflict must be overwrite or skip"}
		}
		if h.Locked() {
			return nil, ErrLocked
		}
		j := d.get(p.ID)
		if j == nil {
			return nil, fmt.Errorf("no job %q", p.ID)
		}
		return map[string]any{}, j.run(p.Conflict)
	})
	// A notification, on the read loop: it only cancels. Allowed while locked.
	s.Handle("files.cancel", func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			ID string `json:"id"`
		}
		if err := strictParams(raw, &p, "id"); err != nil {
			fmt.Fprintf(os.Stderr, "files.cancel: malformed notification: %v\n", err)
			return nil, err
		}
		if j := d.get(p.ID); j != nil {
			j.end("cancelled")
		}
		return map[string]any{}, nil
	})
	cancelAll := func(reason string) []*fileJob {
		d.mu.Lock()
		js := make([]*fileJob, 0, len(d.jobs))
		for _, j := range d.jobs {
			js = append(js, j)
		}
		d.mu.Unlock()
		for _, j := range js {
			j.end(reason)
		}
		return js
	}
	// For Electron main after a renderer crash; not in the renderer whitelist.
	s.HandleRequest("files.cancelAll", func(context.Context, json.RawMessage) (any, error) {
		return map[string]int{"cancelled": len(cancelAll("renderer reloaded"))}, nil
	})
	return func() {
		js := cancelAll("app closed")
		deadline := time.After(5 * time.Second)
		for _, j := range js {
			select {
			case <-j.done:
			case <-deadline:
				return
			}
		}
	}
}
