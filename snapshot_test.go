package main

import "testing"

func TestCleanTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"(741) Analog vTX have a BIG problem. Divimath just solved it. - YouTube",
			"Analog vTX have a BIG problem. Divimath just solved it."},
		{"gromeck/BLE-Scanner - Kagi Search", "gromeck/BLE-Scanner"},
		{"ESP32 Bluetooth foot traffic detection - Claude", "ESP32 Bluetooth foot traffic detection"},
		{"slastra/tabstrip: waybar tab strip · GitHub", "slastra/tabstrip: waybar tab strip"},
		// Page part too short to survive a cut: keep the whole title.
		{"Home - X", "Home - X"},
		{"New Tab", "New Tab"},
		// Tail too long to be a site name: it's content, keep it.
		{"How to fix X - a very long explanation of the whole approach",
			"How to fix X - a very long explanation of the whole approach"},
		// Only the last segment is cut, not every separator.
		{"Song Title - Artist Name - YouTube", "Song Title - Artist Name"},
		{"(2) Inbox | Fastmail", "Inbox | Fastmail"},
	}
	for _, c := range cases {
		if got := cleanTitle(c.in); got != c.want {
			t.Errorf("cleanTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
