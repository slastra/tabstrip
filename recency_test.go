package main

import (
	"testing"
	"time"
)

func tab(browser string, window, id int32, active bool) Tab {
	return Tab{TabInfo: TabInfo{WindowID: window, TabID: id, Active: active}, Browser: browser}
}

func order(tabs []Tab) []string {
	var ids []string
	for _, t := range tabs {
		ids = append(ids, t.ID(t.Browser))
	}
	return ids
}

func TestRecencyOrdersByLastTouch(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t0 := time.UnixMilli(1_000_000)
	tabs := []Tab{
		tab("Firefox", 1, 5, true), tab("Firefox", 1, 9, false),
		tab("Chrome", 7, 3, true),
	}

	r := loadRecency()
	r.Observe(tabs, "", t0)
	// Everything seen at once: newest tab ID first.
	SortByRecency(tabs, r.last)
	if got, want := order(tabs), []string{"firefox.1.9", "firefox.1.5", "chrome.7.3"}; !equal(got, want) {
		t.Fatalf("initial order %v, want %v", got, want)
	}

	// Focus Chrome, then the Firefox tab with the lowest ID.
	r.Observe(tabs, "chrome.7.3", t0.Add(time.Second))
	r.Observe(tabs, "firefox.1.5", t0.Add(2*time.Second))
	SortByRecency(tabs, r.last)
	if got, want := order(tabs), []string{"firefox.1.5", "chrome.7.3", "firefox.1.9"}; !equal(got, want) {
		t.Fatalf("after focus %v, want %v", got, want)
	}

	// Staying on a tab doesn't restamp it; the log persists for `list`.
	r.Observe(tabs, "firefox.1.5", t0.Add(time.Hour))
	if got := loadRecency().last["firefox.1.5"]; got != t0.Add(2*time.Second).UnixMilli() {
		t.Fatalf("held focus restamped to %d", got)
	}

	// A closed tab drops out of the log.
	r.Observe(tabs[:2], "", t0.Add(3*time.Second))
	if _, ok := loadRecency().last["firefox.1.9"]; ok {
		t.Fatal("closed tab still in the log")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
