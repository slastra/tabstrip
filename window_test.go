package main

import "testing"

func TestWindowForTitle(t *testing.T) {
	tabs := []Tab{
		{TabInfo: TabInfo{WindowID: 10, Title: "RELO", Active: true}, Browser: "Firefox"},
		{TabInfo: TabInfo{WindowID: 10, Title: "background tab"}, Browser: "Firefox"},
		{TabInfo: TabInfo{WindowID: 20, Title: "chat jimmy", Active: true}, Browser: "Firefox"},
		{TabInfo: TabInfo{WindowID: 30, Title: "Online Converter - Convert Image, Video, Audio", Active: true}, Browser: "Firefox"},
	}

	cases := []struct {
		name   string
		title  string
		want   int32
		wantOK bool
	}{
		{"exact page part", "RELO — Mozilla Firefox", 10, true},
		{"private window suffix falls back to containment",
			"chat jimmy — Mozilla Firefox (Private Browsing)", 20, true},
		{"long title matched by containment",
			"Online Converter - Convert Image, Video, Audio — Mozilla Firefox", 30, true},
		{"no match renders nothing rather than guessing",
			"Some Other Page — Mozilla Firefox", 0, false},
		// An inactive tab's title must never identify a window: only the
		// active tab is what the window manager shows.
		{"inactive tab title does not match", "background tab — Mozilla Firefox", 0, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := windowForTitle(tabs, c.title)
			if ok != c.wantOK || (ok && got != c.want) {
				t.Errorf("windowForTitle(%q) = (%d, %v), want (%d, %v)",
					c.title, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// Two windows whose active tabs share a title prefix must not collapse onto
// one another: the longer containment match wins.
func TestWindowForTitlePrefersLongestMatch(t *testing.T) {
	tabs := []Tab{
		{TabInfo: TabInfo{WindowID: 1, Title: "Inbox", Active: true}, Browser: "Firefox"},
		{TabInfo: TabInfo{WindowID: 2, Title: "Inbox (3) — Work", Active: true}, Browser: "Firefox"},
	}
	got, ok := windowForTitle(tabs, "Inbox (3) — Work — Mozilla Firefox")
	if !ok || got != 2 {
		t.Errorf("got (%d, %v), want window 2", got, ok)
	}
}

// With more than one browser connected, the window class must decide which
// browser a window belongs to before the title is consulted. Firefox and
// Chrome number their windows independently, so window 1 exists in both.
func TestBrowsersInClass(t *testing.T) {
	tabs := []Tab{
		{TabInfo: TabInfo{WindowID: 1, Title: "Inbox", Active: true}, Browser: "Firefox"},
		{TabInfo: TabInfo{WindowID: 1, Title: "Inbox", Active: true}, Browser: "Chrome"},
		{TabInfo: TabInfo{WindowID: 2, Title: "Docs", Active: true}, Browser: "Chrome2"},
	}

	cases := []struct {
		class string
		want  []string
	}{
		{"firefox", []string{"Firefox"}},
		// Both Chrome profiles share one window class; the title picks between them.
		{"google-chrome", []string{"Chrome", "Chrome2"}},
		// "chromium" must not match "chrome": different browsers.
		{"chromium", nil},
		{"kitty", nil},
	}

	for _, c := range cases {
		got := browsersInClass(tabs, c.class)
		if len(got) != len(c.want) {
			t.Errorf("browsersInClass(%q) = %v, want %v", c.class, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("browsersInClass(%q) = %v, want %v", c.class, got, c.want)
				break
			}
		}
	}
}

// Each browser appends its own name to the window title.
func TestWindowForTitleStripsBrowserSuffix(t *testing.T) {
	tabs := []Tab{
		{TabInfo: TabInfo{WindowID: 5, Title: "Inbox", Active: true}, Browser: "Chrome"},
	}
	for _, title := range []string{
		"Inbox - Google Chrome",
		"Inbox - Chromium",
		"Inbox — Mozilla Firefox",
	} {
		if got, ok := windowForTitle(tabs, title); !ok || got != 5 {
			t.Errorf("windowForTitle(%q) = (%d, %v), want (5, true)", title, got, ok)
		}
	}
}

func TestMaxTabs(t *testing.T) {
	if got := maxTabs(); got != defaultMaxTabs {
		t.Errorf("unset = %d, want the default %d", got, defaultMaxTabs)
	}
	t.Setenv("TABSTRIP_MAX_TABS", "24")
	if got := maxTabs(); got != 24 {
		t.Errorf("override = %d, want 24", got)
	}
	// Junk must not blank the strip.
	for _, bad := range []string{"0", "-3", "lots", ""} {
		t.Setenv("TABSTRIP_MAX_TABS", bad)
		if got := maxTabs(); got != defaultMaxTabs {
			t.Errorf("%q = %d, want the default %d", bad, got, defaultMaxTabs)
		}
	}
}
