package main

// The fallback chip for a tab with no usable favicon: the owning browser's own
// icon, on the same geometry as a real favicon so nothing goes ragged.
//
// This used to be the rofi script's job, which meant the bar sat on blank
// squares until the switcher had been opened at least once. tabstrip is the
// always-running half, so it generates the chip and the switcher only reads.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// browserIconNames maps a tabctl browser name to its freedesktop icon name.
// An unknown browser falls through to its own lowercased name, which is right
// often enough to be worth trying before giving up.
var browserIconNames = map[string]string{
	"firefox":  "firefox",
	"zen":      "zen-browser",
	"chrome":   "google-chrome",
	"chromium": "chromium",
	"brave":    "brave-browser",
	"helium":   "helium-browser",
}

// iconSearchDirs are the freedesktop locations to try, largest first so the
// chip downsamples rather than upscales. Rasters before scalable: a themed PNG
// at 128px beats rasterising an SVG whose CSS we may not fully apply.
var iconSearchDirs = []string{
	"~/.local/share/icons/hicolor/256x256/apps",
	"~/.local/share/icons/hicolor/128x128/apps",
	"/usr/share/icons/hicolor/256x256/apps",
	"/usr/share/icons/hicolor/128x128/apps",
	"/usr/share/icons/hicolor/64x64/apps",
	"/usr/share/icons/hicolor/48x48/apps",
	"/usr/share/pixmaps",
	"/usr/share/icons/hicolor/scalable/apps",
}

// browserChipOnce keeps a browser with no installed icon from re-walking the
// search path on every snapshot.
var browserChipOnce sync.Map // base browser name -> *sync.Once

// EnsureBrowserChip generates "_fallback-<browser>.png" if it isn't cached.
// Idempotent and cheap; safe to call on every snapshot.
func EnsureBrowserChip(dir, browser string) {
	if browser == "" {
		return
	}
	base := strings.ToLower(baseBrowser(browser))
	if exists(filepath.Join(dir, "_fallback-"+base+".png")) {
		return
	}

	o, _ := browserChipOnce.LoadOrStore(base, &sync.Once{})
	o.(*sync.Once).Do(func() {
		src := resolveBrowserIcon(base)
		if src == "" {
			return // nothing installed; fallbackFor uses the blank chip
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return
		}
		img, err := decodeIcon(data)
		if err != nil {
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		_ = writePNG(filepath.Join(dir, "_fallback-"+base+".png"), composeChip(img))
	})
}

// resolveBrowserIcon finds a browser's icon file. Nothing does freedesktop
// icon lookup without pulling in a desktop toolkit, and the full spec (theme
// inheritance, index.theme parsing) buys nothing here: browsers install into
// hicolor or pixmaps under a predictable name.
func resolveBrowserIcon(base string) string {
	name, ok := browserIconNames[base]
	if !ok {
		name = base
	}
	home, _ := os.UserHomeDir()

	for _, dir := range iconSearchDirs {
		if strings.HasPrefix(dir, "~/") {
			if home == "" {
				continue
			}
			dir = filepath.Join(home, dir[2:])
		}
		for _, ext := range []string{".png", ".svg"} {
			if p := filepath.Join(dir, name+ext); exists(p) {
				return p
			}
		}
	}
	return ""
}
