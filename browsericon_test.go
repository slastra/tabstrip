package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Brave's channels and editions install side by side, and tabctl keys them
// separately ("bravebeta", "braveorigin"). A key with no entry falls through
// to its own name, which never matches the hyphenated file Brave ships, so the
// chip silently degrades to the blank square.
func TestBraveChannelsHaveIconCandidates(t *testing.T) {
	// Every key tabctl 2.3.0 generates for the Brave matrix.
	keys := []string{
		"brave", "bravebeta", "bravenightly", "bravedevelopment",
		"braveorigin", "braveoriginbeta", "braveoriginnightly", "braveorigindevelopment",
	}
	for _, k := range keys {
		names, ok := browserIconNames[k]
		if !ok {
			t.Errorf("%s has no icon candidates; it would fall through to %q and miss", k, k)
			continue
		}
		for _, n := range names {
			if !strings.HasPrefix(n, "brave") {
				t.Errorf("%s: candidate %q is not a Brave icon name", k, n)
			}
		}
	}
}

// The lookup must prefer an earlier directory over an earlier candidate name,
// so a large icon for the second choice still beats a small one for the first.
func TestResolveBrowserIconPrefersSizeOverCandidateOrder(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big")
	small := filepath.Join(dir, "small")
	for _, d := range []string{big, small} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(big, "second.png"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(small, "first.png"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	origDirs, origNames := iconSearchDirs, browserIconNames["testbrowser"]
	iconSearchDirs = []string{big, small}
	browserIconNames["testbrowser"] = []string{"first", "second"}
	defer func() {
		iconSearchDirs = origDirs
		if origNames == nil {
			delete(browserIconNames, "testbrowser")
		} else {
			browserIconNames["testbrowser"] = origNames
		}
	}()

	if got, want := resolveBrowserIcon("testbrowser"), filepath.Join(big, "second.png"); got != want {
		t.Errorf("resolveBrowserIcon = %q, want %q", got, want)
	}
}
