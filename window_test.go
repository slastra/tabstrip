package main

import "testing"

func TestWindowForTitle(t *testing.T) {
	tabs := []TabInfo{
		{WindowID: 10, Title: "RELO", Active: true},
		{WindowID: 10, Title: "background tab"},
		{WindowID: 20, Title: "chat jimmy", Active: true},
		{WindowID: 30, Title: "Online Converter - Convert Image, Video, Audio", Active: true},
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
	tabs := []TabInfo{
		{WindowID: 1, Title: "Inbox", Active: true},
		{WindowID: 2, Title: "Inbox (3) — Work", Active: true},
	}
	got, ok := windowForTitle(tabs, "Inbox (3) — Work — Mozilla Firefox")
	if !ok || got != 2 {
		t.Errorf("got (%d, %v), want window 2", got, ok)
	}
}
