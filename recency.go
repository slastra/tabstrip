package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Recency records when each tab was last touched: first seen, then restamped
// every time it becomes the active tab of the focused window. The switcher
// sorts by it, most recent first. Browsers do keep a lastAccessed per tab,
// but tabctl's wire struct doesn't carry it, and the daemon already watches
// focus, so it keeps its own log rather than widening the protocol.
//
// Tab IDs only mean something within one browser session (both restart
// their counters), so the log lives in the runtime dir and a browser's
// entries are dropped whenever its tabs vanish from a listing.
//
// Only the daemon writes; `list` reads.
type Recency struct {
	last    map[string]int64 // tab ID → unix ms
	focused string           // tab focused at the previous observation
}

func recencyPath() string { return filepath.Join(runtimeDir(), "tabstrip-recency.json") }

func loadRecency() *Recency {
	r := &Recency{last: map[string]int64{}}
	if b, err := os.ReadFile(recencyPath()); err == nil {
		json.Unmarshal(b, &r.last)
	}
	return r
}

// Observe folds one listing into the log: new tabs are stamped as seen,
// a change of focused tab restamps it, and tabs that are gone are dropped.
// focused is "" when no browser window has focus. Writes only on change.
func (r *Recency) Observe(tabs []Tab, focused string, now time.Time) {
	ms := now.UnixMilli()
	changed := false
	present := make(map[string]bool, len(tabs))
	for _, t := range tabs {
		id := t.ID(t.Browser)
		present[id] = true
		if _, ok := r.last[id]; !ok {
			r.last[id] = ms
			changed = true
		}
	}
	if focused != "" && focused != r.focused && present[focused] {
		r.last[focused] = ms
		changed = true
	}
	r.focused = focused
	for id := range r.last {
		if !present[id] {
			delete(r.last, id)
			changed = true
		}
	}
	if !changed {
		return
	}
	b, _ := json.Marshal(r.last)
	tmp := recencyPath() + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, recencyPath())
	}
}

// SortByRecency orders tabs most recently touched first. Ties (every tab
// present when the log was first created shares one stamp) fall back to
// the newest tab ID, a stand-in for opening order within a browser.
func SortByRecency(tabs []Tab, last map[string]int64) {
	sort.SliceStable(tabs, func(i, j int) bool {
		a, b := last[tabs[i].ID(tabs[i].Browser)], last[tabs[j].ID(tabs[j].Browser)]
		if a != b {
			return a > b
		}
		return tabs[i].TabID > tabs[j].TabID
	})
}
