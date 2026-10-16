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
