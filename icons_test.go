package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rofi switcher looks chips up by recomputing this key in bash, as
// `printf '%s' "$url" | sha256sum | cut -c1-16`. These expectations are the
// output of that command, pinned here so a change to iconKey that silently
// desyncs the two fails the build instead of blanking the switcher's icons.
func TestIconKeyMatchesRofiScript(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://example.com/favicon.ico", "d3d39abc88b126ca"},
		{"https://github.com/favicon.ico", "87ab12fa0eb341a2"},
		{"", "e3b0c44298fc1c14"}, // sha256 of empty string
	}
	for _, c := range cases {
		if got := iconKey(c.in); got != c.want {
			t.Errorf("iconKey(%q) = %q, want %q (must match the rofi script)", c.in, got, c.want)
		}
	}
}

func TestIconFor(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TABSTRIP_ICON_DIR", dir)

	const favicon = "https://example.com/favicon.ico"
	cached := filepath.Join(dir, iconKey(favicon)+"-processed.png")

	// Seed a cached favicon, as EnsureChips would have left it.
	src := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize))
	src.SetRGBA(10, 10, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	f, _ := os.Create(cached)
	png.Encode(f, src)
	f.Close()

	active := iconFor(favicon, "Firefox", true)
	if active != cached {
		t.Fatalf("active icon = %q, want %q", active, cached)
	}

	inactive := iconFor(favicon, "Firefox", false)
	if !strings.HasSuffix(inactive, iconKey(favicon)+"-processed-dim.png") {
		t.Fatalf("inactive icon = %q", inactive)
	}
	// The dim variant is still generated and still selected for unfocused
	// windows; dimAlpha just sits at 255 now, so it comes out matching the
	// bright one. Assert the alpha tracks the constant rather than pinning a
	// particular look, so this keeps testing the compositing either way.
	img := decode(t, inactive)
	_, _, _, a := img.At(10, 10).RGBA()
	wantA := uint32(dimAlpha) * 0x101
	if a != wantA {
		t.Errorf("dim variant alpha = %#x, want %#x (dimAlpha=%d)", a, wantA, dimAlpha)
	}

	// With no browser chip in the cache, an uncached favicon falls back to the
	// generated blank chip, also dimmed.
	fb := iconFor("https://nosuchsite.test/favicon.ico", "Firefox", false)
	if !strings.Contains(fb, "_tabstrip-fallback") || !strings.HasSuffix(fb, "-dim.png") {
		t.Fatalf("fallback icon = %q", fb)
	}

	// A tab with no favicon at all (internal pages) takes the same path.
	if none := iconFor("", "Firefox", false); !strings.Contains(none, "_tabstrip-fallback") {
		t.Errorf("empty favicon URL = %q, want the fallback chip", none)
	}
	img = decode(t, fb)
	if _, _, _, a := img.At(iconSize/2, iconSize/2).RGBA(); a == 0 {
		t.Error("fallback center is fully transparent")
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Error("fallback corner outside margin should be transparent")
	}
}

// EnsureBrowserChip composites "_fallback-<browser>.png" into the cache;
// iconFor must prefer it over the blank square, and must strip tabctl's
// profile suffix so Chrome and Chrome2 share one chip.
func TestFallbackPrefersBrowserChip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TABSTRIP_ICON_DIR", dir)

	img := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize))
	img.SetRGBA(5, 5, color.RGBA{R: 255, A: 255})
	f, _ := os.Create(filepath.Join(dir, "_fallback-chrome.png"))
	png.Encode(f, img)
	f.Close()

	for _, browser := range []string{"Chrome", "chrome", "Chrome2"} {
		got := iconFor("", browser, true)
		if !strings.HasSuffix(got, "_fallback-chrome.png") {
			t.Errorf("iconFor(_, %q) = %q, want the chrome browser chip", browser, got)
		}
	}

	// A browser with no chip in the cache still gets the blank square.
	if got := iconFor("", "Firefox", true); !strings.Contains(got, "_tabstrip-fallback") {
		t.Errorf("iconFor(_, Firefox) = %q, want the blank chip", got)
	}
}

func decode(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}
