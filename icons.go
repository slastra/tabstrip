package main

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

// tabstrip writes the favicon cache (see favicon.go) and derives what the bar
// needs beside each chip: a dimmed copy, so the windows you are not in recede
// while the focused window stays at full brightness, and a blank rounded
// square for icons that never resolved. Waybar's image module has no CSS class
// states, so brightness is baked into the image rather than styled.
//
// The rofi tab switcher reads this cache but never writes it. Both used to
// write, which is why the key format is pinned by a test (see iconKey).

const (
	// Out of 255. 255 is fully opaque, i.e. no dimming: every chip renders at
	// full brightness and the strip distinguishes the focused window by its
	// border alone. The machinery is deliberately left intact rather than torn
	// out — the dim variant is still written and still selected — so restoring
	// the effect is this one number (100 was the previous value).
	dimAlpha = 255

	// Fallback geometry matches a real chip's, so a tab that lands on the
	// blank square doesn't visibly change size in the strip.
	iconSize   = 64
	iconMargin = 4
	iconRadius = 8
)

// iconDir is the chip cache. tabstrip writes it; the rofi tab switcher
// (~/.config/rofi/scripts/tabs.sh) only reads it. Named for tabctl rather than
// for either program, since it predates both owning it.
func iconDir() string {
	if d := os.Getenv("TABSTRIP_ICON_DIR"); d != "" {
		return d
	}
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "tabctl", "favicons")
}

// iconKey names a chip: the first 16 hex chars of the favicon URL's SHA-256.
//
// KEEP IN SYNC with chip_for() in ~/.config/rofi/scripts/tabs.sh, which
// recomputes the same key in bash to find the chips tabstrip left. A
// divergence here doesn't error — the switcher's every lookup just misses and
// its rows silently fall back to browser icons.
func iconKey(favIconURL string) string {
	sum := sha256.Sum256([]byte(favIconURL))
	return hex.EncodeToString(sum[:])[:16]
}

// iconFor returns the chip image for a tab: the cached favicon when one has
// been fetched for that favicon URL, the generated fallback otherwise; dimmed
// unless bright. Empty string (no icon field in the snapshot) only when
// generation fails.
//
// bright is decided by the caller rather than read off the tab. The strip
// lights the focused window, not each window's active tab. See writeSnapshot.
//
// favIconURL is empty for tabs with no icon and on browser-internal pages,
// which lands on the fallback exactly as an uncached icon does.
func iconFor(favIconURL, browser string, bright bool) string {
	dir := iconDir()

	// An empty favIconURL is never cached under any key, so don't bother
	// stat'ing for it — fall straight through to the fallback.
	base := ""
	if favIconURL != "" {
		if p := filepath.Join(dir, iconKey(favIconURL)+"-processed.png"); exists(p) {
			base = p
		}
	}
	if base == "" {
		if base = fallbackFor(dir, browser); base == "" {
			return ""
		}
	}
	if bright {
		return base
	}
	if dim, err := dimVariant(base); err == nil {
		return dim
	}
	return base
}

// dimVariant returns "<base>-dim.png", regenerating it whenever the source
// icon is newer — a chip is rewritten after the cache is cleared, or after a
// failure marker expires and the icon fetches successfully on the retry.
func dimVariant(base string) (string, error) {
	dim := strings.TrimSuffix(base, ".png") + "-dim.png"
	bi, err := os.Stat(base)
	if err != nil {
		return "", err
	}
	if di, err := os.Stat(dim); err == nil && !di.ModTime().Before(bi.ModTime()) {
		return dim, nil
	}

	f, err := os.Open(base)
	if err != nil {
		return "", err
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		return "", err
	}

	out := image.NewRGBA(src.Bounds())
	draw.DrawMask(out, out.Bounds(), src, src.Bounds().Min,
		image.NewUniform(color.Alpha{A: dimAlpha}), image.Point{}, draw.Over)
	return dim, writePNG(dim, out)
}

// fallbackFor picks the chip for a tab with no usable favicon: the owning
// browser's own icon when one has been composited into the cache, else the
// blank square generated here.
//
// EnsureBrowserChip (browsericon.go) is what composites it, on every snapshot,
// so by the time a tab needs the fallback it is normally already there. The
// blank square covers a browser with no icon installed under any name the
// search path knows.
func fallbackFor(dir, browser string) string {
	if browser != "" {
		p := filepath.Join(dir, "_fallback-"+strings.ToLower(baseBrowser(browser))+".png")
		if exists(p) {
			return p
		}
	}
	return fallbackIcon(dir)
}

// baseBrowser strips tabctl's profile-instance suffix ("Chrome2" -> "Chrome"),
// so every profile of a browser shares one fallback chip.
func baseBrowser(name string) string {
	base := strings.TrimRight(name, "0123456789")
	if base == "" {
		return name
	}
	return base
}

// fallbackIcon generates (once) a blank white rounded square matching the
// cached icons' geometry, for tabs whose favicon is missing or unresolvable.
func fallbackIcon(dir string) string {
	p := filepath.Join(dir, "_tabstrip-fallback.png")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}

	img := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize))
	for y := 0; y < iconSize; y++ {
		for x := 0; x < iconSize; x++ {
			if inRoundedSquare(x, y) {
				img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}
	if writePNG(p, img) != nil {
		return ""
	}
	return p
}

func inRoundedSquare(x, y int) bool {
	lo, hi := iconMargin, iconSize-1-iconMargin
	if x < lo || x > hi || y < lo || y > hi {
		return false
	}
	// Inside the box; corners are rounded with radius iconRadius.
	cx, cy := x, y
	if x < lo+iconRadius {
		cx = lo + iconRadius
	} else if x > hi-iconRadius {
		cx = hi - iconRadius
	}
	if y < lo+iconRadius {
		cy = lo + iconRadius
	} else if y > hi-iconRadius {
		cy = hi - iconRadius
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= iconRadius*iconRadius
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func writePNG(path string, img image.Image) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
