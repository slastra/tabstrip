package main

import "testing"

func TestParseTabID(t *testing.T) {
	cases := []struct {
		id      string
		browser string
		tab     int32
		ok      bool
	}{
		{"firefox.127.9", "firefox", 9, true},
		{"chrome2.1845.2101", "chrome2", 2101, true},
		{"firefox.127", "", 0, false},
		{"firefox.127.9.1", "", 0, false},
		{"firefox.127.x", "", 0, false},
		{".127.9", "", 0, false},
		{"firefox.127.99999999999", "", 0, false},
		{"", "", 0, false},
	}
	for _, c := range cases {
		browser, tab, err := parseTabID(c.id)
		if (err == nil) != c.ok {
			t.Errorf("parseTabID(%q) error = %v, want ok=%v", c.id, err, c.ok)
			continue
		}
		if c.ok && (browser != c.browser || tab != c.tab) {
			t.Errorf("parseTabID(%q) = %q, %d; want %q, %d", c.id, browser, tab, c.browser, c.tab)
		}
	}
}
