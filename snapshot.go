package main

import (
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const (
	maxSlots = 10
	// Titles are capped per-tab from a shared character budget, so chips fit
	// their text: few tabs → long titles, many tabs → tighter truncation.
	charBudget = 300
	titleMin   = 16
	titleMax   = 64
)

func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return "/tmp"
}

func snapshotPath() string { return filepath.Join(runtimeDir(), "waybar-fftabs.json") }
func statePath() string    { return filepath.Join(runtimeDir(), "waybar-fftabs.window") }

// Entry is one rendered chip in the snapshot consumed by the waybar slots.
type Entry struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Active bool   `json:"active"`
}

// pickWindow resolves which Firefox window the strip should show: the focused
// one when Firefox is focused (matched by active-tab title), else the last
// remembered window, else the lowest window ID.
func pickWindow(tabs []TabInfo) (int32, bool) {
	windowSet := map[int32]bool{}
	for _, t := range tabs {
		windowSet[t.WindowID] = true
	}
	if len(windowSet) == 0 {
		return 0, false
	}

	class, title := activeWindow()
	if strings.Contains(strings.ToLower(class), "firefox") && title != "" {
		for _, t := range tabs {
			if t.Active && t.Title != "" && strings.Contains(title, t.Title) {
				os.WriteFile(statePath(), []byte(strconv.Itoa(int(t.WindowID))), 0o644)
				return t.WindowID, true
			}
		}
	}

	if b, err := os.ReadFile(statePath()); err == nil {
		if id, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && windowSet[int32(id)] {
			return int32(id), true
		}
	}

	windows := make([]int, 0, len(windowSet))
	for w := range windowSet {
		windows = append(windows, int(w))
	}
	sort.Ints(windows)
	return int32(windows[0]), true
}

func truncate(title string, cap int) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "…"
	}
	r := []rune(title)
	if len(r) <= cap {
		return title
	}
	return string(r[:cap-1]) + "…"
}

// writeSnapshot refreshes the snapshot file; reports whether it changed.
func writeSnapshot(c *TabCtl) bool {
	tabs, err := c.ListTabs()
	if err != nil {
		tabs = nil // mediator down (browser closed, boot race): render empty, keep running
	}

	var shown []TabInfo
	if windowID, ok := pickWindow(tabs); ok {
		for _, t := range tabs {
			if t.WindowID == windowID {
				shown = append(shown, t)
			}
		}
		sort.Slice(shown, func(i, j int) bool { return shown[i].Index < shown[j].Index })
		if len(shown) > maxSlots {
			shown = shown[:maxSlots]
		}
	}

	cap := charBudget / max(1, len(shown))
	cap = min(titleMax, max(titleMin, cap))
	entries := make([]Entry, 0, len(shown))
	for i, t := range shown {
		entries = append(entries, Entry{
			ID:     t.ID(c.browser),
			Label:  html.EscapeString(strconv.Itoa(i+1) + " " + truncate(t.Title, cap)),
			Active: t.Active,
		})
	}
	payload, _ := json.Marshal(map[string][]Entry{"tabs": entries})

	if old, err := os.ReadFile(snapshotPath()); err == nil && string(old) == string(payload) {
		return false
	}
	tmp := snapshotPath() + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return false
	}
	return os.Rename(tmp, snapshotPath()) == nil
}

func readSnapshot() []Entry {
	var doc struct {
		Tabs []Entry `json:"tabs"`
	}
	b, err := os.ReadFile(snapshotPath())
	if err != nil || json.Unmarshal(b, &doc) != nil {
		return nil
	}
	return doc.Tabs
}

// pokeSlots signals every waybar process so the slot modules re-render.
// Equivalent to `pkill -RTMIN+8 -x waybar` (SIGRTMIN=34 on linux/glibc).
func pokeSlots() {
	const sigRTMin8 = syscall.Signal(34 + 8)
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, p := range procs {
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		comm, err := os.ReadFile("/proc/" + p.Name() + "/comm")
		if err != nil || strings.TrimSpace(string(comm)) != "waybar" {
			continue
		}
		syscall.Kill(pid, sigRTMin8)
	}
}
