package main

// tabstrip owns the favicon cache.
//
// It is the always-running half of the pair: subscribed to tabctl's
// TabsUpdated, it learns about a new tab the moment it opens, so a chip is
// ready before anything asks for it. The rofi switcher runs occasionally and
// only reads what it finds here, which is the opposite of how this started
// out — the bar used to sit on fallback chips until the user happened to open
// the switcher.
//
// Everything is in-process: no ImageMagick, no exec. Go's stdlib covers
// PNG/JPEG/GIF; ICO is parsed here (see decodeICO) and SVG is rasterised with
// oksvg.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	xdraw "golang.org/x/image/draw"
)

const (
	// chipInset leaves a little air around the icon inside the chip box.
	chipInset = 4
	// chipRadius rounds the icon's own corners. Must match what the rofi
	// script produced, so a cache written by either looks the same.
	chipRadius = 10

	// svgRaster is the square an SVG is rasterised into before downsampling.
	// Rendering at the viewBox size and scaling up is what made small SVG
	// favicons look mushy; oversample and come down instead.
	svgRaster = 256

	fetchTimeout = 5 * time.Second
	maxIconBytes = 2 << 20 // 2 MiB; favicons are kilobytes, this is a sanity bound

	// failTTL suppresses retries of an icon that couldn't be fetched or
	// decoded. Without it every tab event re-attempts every broken favicon.
	failTTL = 6 * time.Hour
)

// inFlight collapses concurrent work on the same icon: a burst of tab events
// referencing one uncached favicon should fetch it once.
var inFlight sync.Map // key string -> struct{}

// EnsureChips makes sure every tab's chip exists, fetching and converting the
// missing ones in the background. Returns immediately; callers render whatever
// is on disk now and pick up the rest on the next snapshot.
func EnsureChips(dir string, tabs []TabInfo) {
	seen := make(map[string]bool, len(tabs))
	for _, t := range tabs {
		if t.FavIconURL == "" {
			continue
		}
		key := iconKey(t.FavIconURL)
		if seen[key] {
			continue
		}
		seen[key] = true

		if exists(filepath.Join(dir, key+"-processed.png")) || failedRecently(dir, key) {
			continue
		}
		if _, busy := inFlight.LoadOrStore(key, struct{}{}); busy {
			continue
		}
		go func(favicon, key string) {
			defer inFlight.Delete(key)
			if err := ensureChip(dir, favicon, key); err != nil {
				markFailed(dir, key)
			}
		}(t.FavIconURL, key)
	}
}

func ensureChip(dir, favIconURL, key string) error {
	data, err := fetchIcon(favIconURL)
	if err != nil {
		return err
	}
	img, err := decodeIcon(data)
	if err != nil {
		return err
	}
	return writePNG(filepath.Join(dir, key+"-processed.png"), composeChip(img))
}

// --- fetch -----------------------------------------------------------------

// fetchIcon resolves a favicon URL to bytes. Firefox reports most favicons as
// inline data: URIs, so the common case never touches the network.
func fetchIcon(favIconURL string) ([]byte, error) {
	if strings.HasPrefix(favIconURL, "data:") {
		return decodeDataURI(favIconURL)
	}
	if !strings.HasPrefix(favIconURL, "http://") && !strings.HasPrefix(favIconURL, "https://") {
		return nil, fmt.Errorf("unsupported favicon scheme")
	}

	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, favIconURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("favicon fetch: HTTP %d", resp.StatusCode)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(resp.Body, maxIconBytes)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeDataURI handles both encodings a page can declare: base64, and the
// percent-encoded form used for inline SVG.
func decodeDataURI(uri string) ([]byte, error) {
	comma := strings.IndexByte(uri, ',')
	if comma < 0 {
		return nil, fmt.Errorf("malformed data URI")
	}
	meta, payload := uri[:comma], uri[comma+1:]
	if strings.Contains(meta, ";base64") {
		return base64.StdEncoding.DecodeString(payload)
	}
	unescaped, err := url.PathUnescape(payload)
	if err != nil {
		return nil, err
	}
	return []byte(unescaped), nil
}

// --- decode ----------------------------------------------------------------

// decodeIcon sniffs the real format rather than trusting any declared type.
// The cache names every file .png whatever it holds, and a mis-sniff is how
// ICO favicons silently failed before.
func decodeIcon(data []byte) (image.Image, error) {
	switch {
	case len(data) >= 4 && string(data[:4]) == "\x00\x00\x01\x00":
		return decodeICO(data)
	case looksLikeSVG(data):
		return decodeSVG(data)
	default:
		img, _, err := image.Decode(bytes.NewReader(data)) // png / jpeg / gif
		return img, err
	}
}

func looksLikeSVG(data []byte) bool {
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	lower := strings.ToLower(string(head))
	return strings.Contains(lower, "<svg") || strings.Contains(lower, "<?xml")
}

// decodeICO parses the ICO container directly instead of taking a dependency.
//
// The format is simple, and doing it here fixes a real bug: entry headers are
// routinely wrong. A favicon in the wild declared 256x256 entries whose
// payloads were 16x16 and 32x32 PNGs, which made stricter decoders reject the
// whole file. So the declared dimensions are ignored entirely — every entry is
// decoded and the largest result by actual pixel area wins.
func decodeICO(data []byte) (image.Image, error) {
	if len(data) < 6 {
		return nil, fmt.Errorf("ico: truncated header")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 {
		return nil, fmt.Errorf("ico: no entries")
	}

	var best image.Image
	var bestArea int
	for i := 0; i < count; i++ {
		off := 6 + i*16
		if off+16 > len(data) {
			break
		}
		size := int(binary.LittleEndian.Uint32(data[off+8 : off+12]))
		start := int(binary.LittleEndian.Uint32(data[off+12 : off+16]))
		if start <= 0 || size <= 0 || start+size > len(data) {
			continue
		}

		img, err := decodeICOEntry(data[start : start+size])
		if err != nil {
			continue // a bad entry shouldn't condemn the file
		}
		if a := img.Bounds().Dx() * img.Bounds().Dy(); a > bestArea {
			best, bestArea = img, a
		}
	}
	if best == nil {
		return nil, fmt.Errorf("ico: no decodable entry")
	}
	return best, nil
}

// decodeICOEntry decodes one ICO entry. Modern favicons embed a PNG; older
// ones hold a bare DIB, which needs a synthesised BMP file header since the
// container strips it.
func decodeICOEntry(payload []byte) (image.Image, error) {
	if len(payload) >= 8 && string(payload[1:4]) == "PNG" {
		return png.Decode(bytes.NewReader(payload))
	}
	return decodeDIB(payload)
}

// decodeDIB wraps a bare device-independent bitmap in the 14-byte BMP file
// header that x/image/bmp expects.
//
// An ICO's DIB doubles its declared height to cover the AND mask that follows
// the colour data; halve it back or every icon decodes stretched with a band
// of mask data at the bottom.
func decodeDIB(payload []byte) (image.Image, error) {
	const fileHeaderLen = 14
	if len(payload) < 40 {
		return nil, fmt.Errorf("ico: truncated DIB")
	}
	fixed := append([]byte(nil), payload...)
	height := int32(binary.LittleEndian.Uint32(fixed[8:12]))
	binary.LittleEndian.PutUint32(fixed[8:12], uint32(height/2))

	bpp := int(binary.LittleEndian.Uint16(fixed[14:16]))
	headerSize := binary.LittleEndian.Uint32(fixed[0:4])
	paletteLen := 0
	if bpp <= 8 {
		paletteLen = (1 << bpp) * 4
	}
	dataOffset := fileHeaderLen + int(headerSize) + paletteLen

	hdr := make([]byte, fileHeaderLen)
	hdr[0], hdr[1] = 'B', 'M'
	binary.LittleEndian.PutUint32(hdr[2:6], uint32(fileHeaderLen+len(fixed)))
	binary.LittleEndian.PutUint32(hdr[10:14], uint32(dataOffset))

	return decodeBMP(append(hdr, fixed...))
}

// decodeSVG rasterises a vector favicon well above the chip size and lets
// composeChip downsample, so small viewBoxes stay crisp.
//
// oksvg reads presentation attributes and inline style="" but does not apply
// CSS from a <style> block, so an icon that themes itself that way renders
// with default fills rather than its declared ones.
func decodeSVG(data []byte) (image.Image, error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data), oksvg.WarnErrorMode)
	if err != nil {
		return nil, err
	}
	icon.SetTarget(0, 0, float64(svgRaster), float64(svgRaster))

	img := image.NewRGBA(image.Rect(0, 0, svgRaster, svgRaster))
	scanner := rasterx.NewScannerGV(svgRaster, svgRaster, img, img.Bounds())
	icon.Draw(rasterx.NewDasher(svgRaster, svgRaster, scanner), 1.0)
	return img, nil
}

// --- compose ---------------------------------------------------------------

// composeChip fits the icon into the chip box, rounds its corners, and centres
// it on a transparent canvas. The icon is squared first so the rounding lands
// on its real edges rather than on whatever aspect it happened to have.
func composeChip(src image.Image) image.Image {
	inner := iconSize - chipInset*2

	square := image.NewRGBA(image.Rect(0, 0, inner, inner))
	xdraw.CatmullRom.Scale(square, fit(src.Bounds(), inner), src, src.Bounds(), draw.Over, nil)

	rounded := image.NewRGBA(square.Bounds())
	draw.DrawMask(rounded, square.Bounds(), square, image.Point{}, roundedMask(inner, chipRadius), image.Point{}, draw.Over)

	chip := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize))
	draw.Draw(chip, image.Rect(chipInset, chipInset, chipInset+inner, chipInset+inner), rounded, image.Point{}, draw.Over)
	return chip
}

// fit returns the largest centred box of the given side that preserves aspect.
func fit(b image.Rectangle, side int) image.Rectangle {
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return image.Rect(0, 0, side, side)
	}
	if w > h {
		nh := side * h / w
		return image.Rect(0, (side-nh)/2, side, (side-nh)/2+nh)
	}
	nw := side * w / h
	return image.Rect((side-nw)/2, 0, (side-nw)/2+nw, side)
}

func roundedMask(side, radius int) *image.Alpha {
	m := image.NewAlpha(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			if inRounded(x, y, side, radius) {
				m.SetAlpha(x, y, color.Alpha{A: 255})
			}
		}
	}
	return m
}

func inRounded(x, y, side, radius int) bool {
	hi := side - 1
	cx, cy := x, y
	if x < radius {
		cx = radius
	} else if x > hi-radius {
		cx = hi - radius
	}
	if y < radius {
		cy = radius
	} else if y > hi-radius {
		cy = hi - radius
	}
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= radius*radius
}

// --- negative cache --------------------------------------------------------

func failMarker(dir, key string) string { return filepath.Join(dir, key+".fail") }

func markFailed(dir, key string) {
	_ = os.MkdirAll(dir, 0o755)
	if f, err := os.Create(failMarker(dir, key)); err == nil {
		f.Close()
	}
}

func failedRecently(dir, key string) bool {
	fi, err := os.Stat(failMarker(dir, key))
	if err != nil {
		return false
	}
	if time.Since(fi.ModTime()) < failTTL {
		return true
	}
	os.Remove(failMarker(dir, key)) // expired; allow one more attempt
	return false
}
