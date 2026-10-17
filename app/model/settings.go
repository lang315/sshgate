package model

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Limit is one soft-lock limit: the hub's value and the Settings label.
type Limit struct{ Value, Label string }

// Limits are the Settings choices, shortest first (Electron's LIMITS).
var Limits = []Limit{{"24h", "24 hours (default)"}, {"3d", "3 days"}, {"7d", "7 days"}, {"none", "No limit"}}

// limitWords is how a fixed limit reads in a sentence: "up to 3 days",
// "the 3-day limit". No limit has none.
var limitWords = map[string][2]string{"24h": {"24 hours", "24-hour"}, "3d": {"3 days", "3-day"}, "7d": {"7 days", "7-day"}}

func limitRank(l string) int {
	return slices.IndexFunc(Limits, func(x Limit) bool { return x.Value == l })
}

// LimitChange is "same", "none" (No limit chosen), "higher" or "lower".
func LimitChange(cur, chosen string) string {
	switch {
	case chosen == cur:
		return "same"
	case chosen == "none":
		return "none"
	case limitRank(chosen) > limitRank(cur):
		return "higher"
	}
	return "lower"
}

func LimitHelp(l string) string {
	if l == "none" {
		return "Auto-allow stops only with Stop auto-allow and lock, a restart or a crash."
	}
	return "Then the app locks fully and auto-allow stops. Forever grants wait for Resume."
}

func LimitNotice(l string) string {
	span := "for as long as the app runs"
	if l != "none" {
		span = "for up to " + limitWords[l][0]
	}
	return "While the app is locked, the AI keeps running on hosts with auto-allow " + span + ". Any program running as your user can use that access too."
}

// SettingsChangeKey is what restarts Save's delay: the choice (it swaps the
// notice), the hosts the notice lists, an error line, and whether the master
// password field is showing (Touch ID being turned on).
func SettingsChangeKey(chosen string, hosts []string, err string, touchOn bool) string {
	return fmt.Sprintf("%s|%s|%q|%t", chosen, strings.Join(hosts, "\n"), err, touchOn)
}

// SaveAllowed is Settings' Save: something changed (the limit, per change,
// or Touch ID, per touch: "same", "on" or "off"), Touch ID being turned on
// has a master password, nothing is in flight, and AllowDelay has passed
// since the dialog opened or its content last changed.
func SaveAllowed(change, touch, password string, busy bool, changedAt, now time.Time) bool {
	if busy || (change == "same" && touch == "same") || (touch == "on" && password == "") {
		return false
	}
	return TrustEnabled(changedAt, now)
}

// LimitText is the Auto-allow dialog's paragraph for a forever grant, from
// the vault's soft-lock limit ("" reads as the 24-hour default).
func LimitText(limit string) string {
	keep := "the AI keeps running on this host while the app is locked, with no time limit"
	after := "After a restart or Stop auto-allow and lock"
	if limit != "none" {
		w, ok := limitWords[limit]
		if !ok {
			w = limitWords["24h"] // absent: the hub's default
		}
		keep = "the AI keeps running on this host for up to " + w[0]
		after = "After a restart, the " + w[1] + " limit, or Stop auto-allow and lock"
	}
	return "Stays set after restart. Locking the app, by hand or from inactivity, does not stop it: " + keep +
		". To stop it, use Stop auto-allow and lock on the unlock screen, Stop on the host card, or Stop all auto-allow. " + after + ", it waits for you to click Resume."
}
