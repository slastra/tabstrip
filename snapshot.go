package main

import (
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"regexp"
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
	titleMax   = 40
)

func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return "/tmp"
}

func snapshotPath() string { return filepath.Join(runtimeDir(), "waybar-fftabs.json") }

// Entry is one rendered chip in the snapshot consumed by the waybar slots.
type Entry struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Icon   string `json:"icon,omitempty"`
	Active bool   `json:"active"`
}

// pickWindows resolves which browser windows the strip should show: every one
// on the focused workspace, in on-screen order.
//
// Workspace rather than focused-window scope means a chip is always something
// you can currently see, which is the property worth keeping — clicking one
// can't drag you to a tab on a workspace you've left. It also keeps the strip
// populated while a terminal or editor has focus, which the focused-window
// version could not.
//
// tabctl exposes no window-identity signal, so Hyprland windows are mapped to
// tabctl window IDs by title: exact against the page part of the window title
// first, then the longest active-tab title contained in it. A window that
// matches nothing (title lag mid-navigation) is skipped rather than guessed at.
// windowTitleSuffixes are the browser-name tails appended to window titles.
// Longest first, so " — Mozilla Firefox" is not shadowed by a shorter match.
var windowTitleSuffixes = []string{
	" — Mozilla Firefox",
	" - Google Chrome",
	" - Chromium",
	" - Brave",
	" — Zen Browser",
	" - Helium",
}

// winKey identifies a browser window. The window ID alone is not enough once
// more than one browser is connected: Firefox and Chrome number their windows
// independently and will collide.
type winKey struct {
	Browser  string
	WindowID int32
}

func pickWindows(tabs []Tab) []winKey {
	var keys []winKey
	seen := map[winKey]bool{}

	for _, c := range clientsOnActiveWorkspace() {
		if c.Title == "" {
			continue
		}
		class := strings.ToLower(c.Class)

		// Narrow to the browsers whose name appears in this window's class,
		// then identify the window among that browser's tabs. Matching the
		// class first keeps a Chrome window from being resolved against a
		// Firefox tab that happens to share a title.
		for _, browser := range browsersInClass(tabs, class) {
			var owned []Tab
			for _, t := range tabs {
				if t.Browser == browser {
					owned = append(owned, t)
				}
			}
			if id, ok := windowForTitle(owned, c.Title); ok {
				k := winKey{Browser: browser, WindowID: id}
				if !seen[k] {
					seen[k] = true
					keys = append(keys, k)
				}
				break
			}
		}
	}
	return keys
}

// browsersInClass returns the connected browsers whose base name appears in a
// window class, e.g. "google-chrome" matches Chrome and Chrome2. Profiles of
// one browser share a class, so both are returned and the title decides.
func browsersInClass(tabs []Tab, class string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range tabs {
		if seen[t.Browser] {
			continue
		}
		base := strings.ToLower(baseBrowser(t.Browser))
		if base != "" && strings.Contains(class, base) {
			seen[t.Browser] = true
			out = append(out, t.Browser)
		}
	}
	sort.Strings(out)
	return out
}

// windowForTitle maps one Hyprland window title back to a tabctl window ID.
func windowForTitle(tabs []Tab, title string) (int32, bool) {
	// Browsers append their own name to the window title: Firefox uses
	// " — Mozilla Firefox" (plus " (Private Browsing)"), Chrome and its
	// relatives " - Google Chrome". Strip whichever suffix is present so the
	// remainder can be compared to a tab title exactly.
	page := title
	for _, suffix := range windowTitleSuffixes {
		if i := strings.LastIndex(page, suffix); i >= 0 {
			page = page[:i]
			break
		}
	}

	var best int32
	bestLen := 0
	for _, t := range tabs {
		if !t.Active || t.Title == "" {
			continue
		}
		if t.Title == page {
			return t.WindowID, true
		}
		if len(t.Title) > bestLen && strings.Contains(title, t.Title) {
			best, bestLen = t.WindowID, len(t.Title)
		}
	}
	return best, bestLen > 0
}

// counterPrefix matches unread-count prefixes like "(741) ".
var counterPrefix = regexp.MustCompile(`^\(\d+\)\s*`)

// titleSeps are the separators sites put before their name in page titles.
var titleSeps = []string{" — ", " – ", " - ", " | ", " · "}

// cleanTitle drops chip noise from a tab title: a leading unread counter and
// a trailing site-name segment ("… - YouTube"). The site segment is only cut
// when what remains is long enough to stay recognizable and the tail is short
// enough to look like a site name rather than content.
func cleanTitle(title string) string {
	title = strings.TrimSpace(counterPrefix.ReplaceAllString(title, ""))
	cut, sepLen := -1, 0
	for _, s := range titleSeps {
		if i := strings.LastIndex(title, s); i > cut {
			cut, sepLen = i, len(s)
		}
	}
	if cut >= 8 && len(title)-cut-sepLen <= 30 {
		title = strings.TrimSpace(title[:cut])
	}
	return title
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
func writeSnapshot(f *Fleet) bool {
	tabs := f.ListAll()

	// Cache chips for every tab of every browser, not just the windows being
	// rendered: switching windows or workspaces should find its icons already
	// there. Runs in the background, so this snapshot uses whatever is on disk
	// now and the next one picks up the rest.
	infos := make([]TabInfo, len(tabs))
	browsers := map[string]bool{}
	for i, t := range tabs {
		infos[i] = t.TabInfo
		browsers[t.Browser] = true
	}
	EnsureChips(iconDir(), infos)
	for b := range browsers {
		EnsureBrowserChip(iconDir(), b)
	}

	// Group by window in on-screen order, tabs within a window in tab-bar
	// order, so the strip reads the way the screen looks.
	var shown []Tab
	byWindow := map[winKey][]Tab{}
	for _, t := range tabs {
		k := winKey{Browser: t.Browser, WindowID: t.WindowID}
		byWindow[k] = append(byWindow[k], t)
	}
	for _, k := range pickWindows(tabs) {
		w := byWindow[k]
		sort.Slice(w, func(i, j int) bool { return w[i].Index < w[j].Index })
		shown = append(shown, w...)
	}
	if len(shown) > maxSlots {
		shown = shown[:maxSlots]
	}

	cap := charBudget / max(1, len(shown))
	cap = min(titleMax, max(titleMin, cap))
	entries := make([]Entry, 0, len(shown))
	for i, t := range shown {
		entries = append(entries, Entry{
			ID:     t.ID(t.Browser),
			Label:  html.EscapeString(strconv.Itoa(i+1) + " " + truncate(cleanTitle(t.Title), cap)),
			Icon:   iconFor(t.FavIconURL, t.Browser, t.Active),
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
