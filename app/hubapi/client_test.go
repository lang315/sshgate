package hubapi

import (
	"context"
	"encoding/json"
	"testing"
)

// rec records one call and answers it with a canned JSON result.
type rec struct {
	method string
	params string
	result string
}

func (r *rec) Call(_ context.Context, method string, params, result any) error {
	r.method = method
	b, _ := json.Marshal(params)
	r.params = string(b)
	if result != nil && r.result != "" {
		return json.Unmarshal([]byte(r.result), result)
	}
	return nil
}

func TestCallShapes(t *testing.T) {
	ctx := context.Background()
	pw1, empty := "pw1", ""
	for _, tc := range []struct {
		name, method, params string
		call                 func(*Client) error
	}{
		{"lock", "lock", `{}`, func(c *Client) error { return c.Lock(ctx, false) }},
		{"stop and lock", "lock", `{"stopAuto":true}`, func(c *Client) error { return c.Lock(ctx, true) }},
		{"unlock", "unlock", `{"password":"pw"}`, func(c *Client) error { _, err := c.Unlock(ctx, "pw"); return err }},
		{"decide", "decide", `{"id":"abc","outcome":"denied","reason":"no"}`, func(c *Client) error { return c.Decide(ctx, "abc", Denied, "no") }},
		{"deny all", "denyAll", `{"reason":"denied all by user"}`, func(c *Client) error { return c.DenyAll(ctx, "denied all by user") }},
		{"create", "vault.create", `{"password":"password1"}`, func(c *Client) error { return c.CreateVault(ctx, "password1") }},
		{"auto off", "servers.setAutoAllow", `{"mode":"off","server":"box"}`, func(c *Client) error { return c.SetAutoAllow(ctx, "box", "off") }},
		{"forget key", "servers.forgetHostKey", `{"name":"box"}`, func(c *Client) error { return c.ForgetHostKey(ctx, "box") }},
		{"save new", "servers.save", `{"server":{"name":"web","host":"10.0.0.5","port":22,"user":"u","auth":"password","keyPath":"","aiVisible":false,"autoAllowRoot":false,"autoAllowSudo":false,"password":"pw1"},"autoAllow":"off"}`,
			func(c *Client) error {
				return c.SaveServer(ctx, "", ServerInput{Name: "web", Host: "10.0.0.5", Port: 22, User: "u", Auth: "password", Password: &pw1}, "off")
			}},
		{"save edit", "servers.save", `{"original":"box","server":{"name":"box","host":"h","port":2222,"user":"u","auth":"key","keyPath":"~/.ssh/id","aiVisible":true,"autoAllowRoot":true,"autoAllowSudo":false,"keyPassphrase":""},"autoAllow":"forever"}`,
			func(c *Client) error {
				return c.SaveServer(ctx, "box", ServerInput{Name: "box", Host: "h", Port: 2222, User: "u", Auth: "key", KeyPath: "~/.ssh/id", AIVisible: true, AutoAllowRoot: true, KeyPassphrase: &empty}, "forever")
			}},
		{"delete", "servers.delete", `{"name":"box"}`, func(c *Client) error { return c.DeleteServer(ctx, "box") }},
		{"import scan", "import.scan", `null`, func(c *Client) error { _, err := c.ImportScan(ctx); return err }},
		{"import apply", "import.apply", `{"aliases":["work","db"]}`, func(c *Client) error { _, err := c.ImportApply(ctx, []string{"work", "db"}); return err }},
		{"auto check", "servers.autoAllowCheck", `{"server":"box"}`, func(c *Client) error { _, err := c.AutoAllowCheck(ctx, "box"); return err }},
		{"soft-lock limit", "settings.setSoftLockLimit", `{"limit":"none"}`, func(c *Client) error { return c.SetSoftLockLimit(ctx, "none") }},
		{"files list", "files.list", `{"server":"box","path":""}`, func(c *Client) error { _, err := c.FilesList(ctx, "box", "", nil); return err }},
		{"files list trusting", "files.list", `{"server":"box","path":"/home","trustHostKey":{"fingerprint":"SHA256:x","keyType":"ssh-ed25519"}}`,
			func(c *Client) error {
				_, err := c.FilesList(ctx, "box", "/home", &Trust{Fingerprint: "SHA256:x", KeyType: "ssh-ed25519"})
				return err
			}},
		{"audit read", "audit.read", `{}`, func(c *Client) error { _, err := c.AuditRead(ctx, AuditQuery{}); return err }},
		{"audit read filtered", "audit.read", `{"before":40,"server":"box","kinds":["config","file"],"outcomes":["auto"],"text":"tail"}`,
			func(c *Client) error {
				_, err := c.AuditRead(ctx, AuditQuery{Before: 40, Server: "box", Kinds: []string{"config", "file"}, Outcomes: []string{"auto"}, Text: "tail"})
				return err
			}},
		{"mkdir", "files.mkdir", `{"path":"/home/a","server":"box"}`, func(c *Client) error { return c.FilesMkdir(ctx, "box", "/home/a") }},
		{"rename", "files.rename", `{"from":"/home/a","server":"box","to":"/home/b"}`, func(c *Client) error { return c.FilesRename(ctx, "box", "/home/a", "/home/b") }},
		{"plan upload", "files.plan", `{"id":"f1","server":"box","op":"upload","sources":["/l/a"],"dest":"/home"}`,
			func(c *Client) error { return c.FilesPlan(ctx, "f1", "box", "upload", []string{"/l/a"}, "/home") }},
		{"plan delete", "files.plan", `{"id":"f2","server":"box","op":"delete","sources":["/home/a"]}`,
			func(c *Client) error { return c.FilesPlan(ctx, "f2", "box", "delete", []string{"/home/a"}, "") }},
		{"run", "files.run", `{"conflict":"skip","id":"f1"}`, func(c *Client) error { return c.FilesRun(ctx, "f1", "skip") }},
		{"tunnels list", "tunnels.list", `{}`, func(c *Client) error { _, err := c.TunnelsList(ctx); return err }},
		{"tunnel save dynamic", "tunnels.save", `{"server":"box","tunnel":{"id":"","kind":"dynamic","listenPort":1080,"targetHost":"","targetPort":0,"label":""}}`,
			func(c *Client) error {
				_, err := c.TunnelsSave(ctx, "box", Tunnel{Kind: "dynamic", ListenPort: 1080})
				return err
			}},
		{"tunnel save local", "tunnels.save", `{"server":"box","tunnel":{"id":"a1","kind":"local","listenPort":5433,"targetHost":"db","targetPort":5432,"label":"pg"}}`,
			func(c *Client) error {
				_, err := c.TunnelsSave(ctx, "box", Tunnel{ID: "a1", Kind: "local", ListenPort: 5433, TargetHost: "db", TargetPort: 5432, Label: "pg"})
				return err
			}},
		{"tunnel delete", "tunnels.delete", `{"id":"a1","server":"box"}`, func(c *Client) error { return c.TunnelsDelete(ctx, "box", "a1") }},
		{"tunnel start", "tunnels.start", `{"id":"a1","server":"box"}`, func(c *Client) error { return c.TunnelsStart(ctx, "box", "a1") }},
	} {
		r := &rec{}
		if err := tc.call(New(r)); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if r.method != tc.method || r.params != tc.params {
			t.Errorf("%s: sent %s %s, want %s %s", tc.name, r.method, r.params, tc.method, tc.params)
		}
	}
}

func TestStatusDecodesOptionalFields(t *testing.T) {
	r := &rec{result: `{"locked":true,"hasStore":true,"hasVault":true,"storePath":"/v.json","pending":2,"autoHosts":["box"],"storeError":"bad"}`}
	s, err := New(r).Status(context.Background())
	if err != nil || r.method != "status" {
		t.Fatal(err, r.method)
	}
	if !s.Locked || !s.HasVault || s.StorePath != "/v.json" || len(s.AutoHosts) != 1 || s.StoreError != "bad" || s.SoftLockLimit != "" {
		t.Fatalf("%+v", s)
	}
}

func TestPendingAndServersNeverNil(t *testing.T) {
	r := &rec{result: `[]`}
	p, err := New(r).Pending(context.Background())
	if err != nil || p == nil {
		t.Fatalf("pending %v %v", p, err)
	}
	s, err := New(&rec{result: `null`}).Servers(context.Background())
	if err != nil || s == nil {
		t.Fatalf("servers %v %v", s, err)
	}
}

func TestServerDecodesSecretFlagsAndOptIns(t *testing.T) {
	r := &rec{result: `[{"name":"box","hasPassword":true,"hasSuPassword":false,"hasSudoPassword":true,"hasKeyPassphrase":true,"autoAllowRoot":true,"autoAllowSudo":true}]`}
	s, err := New(r).Servers(context.Background())
	if err != nil || len(s) != 1 {
		t.Fatal(s, err)
	}
	if b := s[0]; !b.HasPassword || b.HasSuPassword || !b.HasSudoPassword || !b.HasKeyPassphrase || !b.AutoAllowRoot || !b.AutoAllowSudo {
		t.Fatalf("%+v", b)
	}
}

func TestImportRepliesDecode(t *testing.T) {
	s, err := New(&rec{result: `{"candidates":null,"note":"No ~/.ssh/config"}`}).ImportScan(context.Background())
	if err != nil || s.Candidates == nil || len(s.Candidates) != 0 || s.Note != "No ~/.ssh/config" {
		t.Fatalf("%+v %v", s, err)
	}
	s, err = New(&rec{result: `{"candidates":[{"alias":"web","host":"10.0.0.5","port":2200,"user":"deploy","auth":"key","keyPath":"~/.ssh/id_web","needsPassphrase":true,"hostKey":"SHA256:abc","hostKeyAlgo":"ssh-ed25519","status":"ready"}]}`}).ImportScan(context.Background())
	if c := s.Candidates; err != nil || len(c) != 1 || c[0].Port != 2200 || !c[0].NeedsPassphrase || c[0].HostKeyAlgo != "ssh-ed25519" || c[0].Status != "ready" {
		t.Fatalf("%+v %v", s, err)
	}
	res, err := New(&rec{result: `{"imported":["web"],"skipped":[{"alias":"jump","reason":"needs ProxyJump"}]}`}).ImportApply(context.Background(), []string{"web", "jump"})
	if err != nil || len(res.Imported) != 1 || res.Skipped[0] != (ImportSkip{"jump", "needs ProxyJump"}) {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestAutoCheckDecodes(t *testing.T) {
	c, err := New(&rec{result: `{"uid":1000,"passwordlessSudo":true}`}).AutoAllowCheck(context.Background(), "box")
	if err != nil || c != (AutoCheck{UID: 1000, PasswordlessSudo: true}) {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestFilesListDecodes(t *testing.T) {
	l, err := New(&rec{result: `{"status":"listed","path":"/home","entries":null,"truncated":true,"bad":2}`}).FilesList(context.Background(), "box", "", nil)
	if err != nil || l.Status != "listed" || l.Path != "/home" || l.Entries == nil || !l.Truncated || l.Bad != 2 {
		t.Fatalf("%+v %v", l, err)
	}
	l, err = New(&rec{result: `{"status":"hostKeyUnknown","server":"box","host":"h","port":22,"user":"u","fingerprint":"SHA256:x","keyType":"ssh-ed25519","knownHosts":"absent"}`}).FilesList(context.Background(), "box", "", nil)
	if err != nil || l.Status != "hostKeyUnknown" || l.Fingerprint != "SHA256:x" || l.KeyType != "ssh-ed25519" || l.Port != 22 {
		t.Fatalf("%+v %v", l, err)
	}
}

func TestTunnelsDecode(t *testing.T) {
	l, err := New(&rec{result: `null`}).TunnelsList(context.Background())
	if err != nil || l == nil || len(l) != 0 {
		t.Fatalf("%#v %v", l, err)
	}
	l, err = New(&rec{result: `[{"server":"box","id":"a1","kind":"local","listenPort":5433,"targetHost":"db","targetPort":5432,"label":"pg","status":"error","error":"connection failed","conns":0},{"server":"box","id":"b2","kind":"dynamic","listenPort":1080,"status":"running","conns":3}]`}).TunnelsList(context.Background())
	if err != nil || len(l) != 2 || l[0].Server != "box" || l[0].ID != "a1" || l[0].TargetHost != "db" || l[0].Label != "pg" || l[0].Error != "connection failed" ||
		l[1].Kind != "dynamic" || l[1].TargetHost != "" || l[1].Status != "running" || l[1].Conns != 3 {
		t.Fatalf("%+v %v", l, err)
	}
	saved, err := New(&rec{result: `{"id":"c3","kind":"dynamic","listenPort":1080}`}).TunnelsSave(context.Background(), "box", Tunnel{Kind: "dynamic", ListenPort: 1080})
	if err != nil || saved.ID != "c3" {
		t.Fatalf("%+v %v", saved, err)
	}
}

func TestAuditReadDecodes(t *testing.T) {
	p, err := New(&rec{result: `{"records":null,"skipped":0,"path":"/a/audit.jsonl"}`}).AuditRead(context.Background(), AuditQuery{})
	if err != nil || p.Records == nil || len(p.Records) != 0 || p.Path != "/a/audit.jsonl" {
		t.Fatalf("%#v %v", p, err)
	}
	p, err = New(&rec{result: `{"records":[{"seq":9,"record":{"kind":"config","action":"save"}},{"seq":8,"record":{"command":"ls"}}],"next":8,"skipped":2,"path":"/p"}`}).AuditRead(context.Background(), AuditQuery{})
	if err != nil || len(p.Records) != 2 || p.Records[0].Seq != 9 || string(p.Records[1].Record) != `{"command":"ls"}` || p.Next != 8 || p.Skipped != 2 {
		t.Fatalf("%+v %v", p, err)
	}
}
