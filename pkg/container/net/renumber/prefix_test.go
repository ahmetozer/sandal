//go:build linux

package renumber

import (
	"net"
	"testing"
	"time"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSelectIgnoresLinkLocalAndULA(t *testing.T) {
	now := time.Now()
	cands := []Candidate{
		{Prefix: mustCIDR(t, "fe80::/64"), ValidUntil: now.Add(time.Hour)},
		{Prefix: mustCIDR(t, "fd00::/64"), ValidUntil: now.Add(time.Hour)},
		{Prefix: mustCIDR(t, "2001:db8::/64"), ValidUntil: now.Add(time.Hour)},
	}
	got := Select(cands, nil)
	if got == nil {
		t.Fatal("expected a global prefix to be selected")
	}
	if got.String() != "2001:db8::/64" {
		t.Errorf("got %s want 2001:db8::/64", got)
	}
}

func TestSelectLongestValidLifetime(t *testing.T) {
	now := time.Now()
	cands := []Candidate{
		{Prefix: mustCIDR(t, "2001:db8:1::/64"), ValidUntil: now.Add(time.Hour)},
		{Prefix: mustCIDR(t, "2001:db8:2::/64"), ValidUntil: now.Add(2 * time.Hour)},
	}
	got := Select(cands, nil)
	if got == nil || got.String() != "2001:db8:2::/64" {
		t.Errorf("got %v want 2001:db8:2::/64", got)
	}
}

func TestSelectStickyToCurrent(t *testing.T) {
	now := time.Now()
	current := mustCIDR(t, "2001:db8:1::/64")
	cands := []Candidate{
		{Prefix: mustCIDR(t, "2001:db8:1::/64"), ValidUntil: now.Add(time.Hour)},
		{Prefix: mustCIDR(t, "2001:db8:2::/64"), ValidUntil: now.Add(time.Hour)},
	}
	got := Select(cands, current)
	if got == nil || got.String() != "2001:db8:1::/64" {
		t.Errorf("got %v want 2001:db8:1::/64 (sticky)", got)
	}
}

func TestSelectSwitchesWhenCurrentDisappears(t *testing.T) {
	now := time.Now()
	current := mustCIDR(t, "2001:db8:1::/64")
	cands := []Candidate{
		{Prefix: mustCIDR(t, "2001:db8:2::/64"), ValidUntil: now.Add(time.Hour)},
	}
	got := Select(cands, current)
	if got == nil || got.String() != "2001:db8:2::/64" {
		t.Errorf("got %v want 2001:db8:2::/64", got)
	}
}

func TestSelectEmpty(t *testing.T) {
	if got := Select(nil, nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}
