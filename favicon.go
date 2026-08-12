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
// PNG/JPEG/GIF and x/image/webp the fourth raster format sites actually serve;
// ICO is parsed here (see decodeICO) and SVG is rasterised with oksvg.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
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
	_ "golang.org/x/image/webp"
)

const (
	// chipInset leaves a little air around the icon inside the chip box.
	chipInset = 4
	// chipRadius rounds the icon's own corners. Shared with the rofi
	// switcher's rows, which render these same files, so changing it changes
	// both surfaces at once.
	chipRadius = 10

	// svgRaster is the square an SVG is rasterised into before downsampling.
	// Rendering at the viewBox size and scaling up is what made small SVG
	// favicons look mushy; oversample and come down instead.
	svgRaster = 256

	fetchTimeout = 5 * time.Second
	maxIconBytes = 2 << 20 // 2 MiB; favicons are kilobytes, this is a sanity bound

	// failTTL suppresses retries of an icon that is genuinely broken — it
	// didn't decode, or the server says it isn't there. Without it every tab
	// event re-attempts every broken favicon.
	failTTL = 6 * time.Hour

	// transientFailTTL is the same suppression for a failure the network is
	// expected to recover from. The daemon starts with the session, so it
	// routinely races DNS, a VPN, or wifi association; at the flat failTTL a
	// single blip at login blanked every http-hosted favicon in the strip
	// until the afternoon. Short enough to heal unnoticed, long enough that a
	// genuinely unreachable host isn't retried every snapshot.
	transientFailTTL = 5 * time.Minute

	// fetchUserAgent identifies as Firefox ESR rather than as tabstrip.
	//
	// This asks for nothing the browser hasn't already asked for — we are
	// re-fetching an icon the tab whose favicon this is has loaded already —
	// but Go's default "Go-http-client/1.1" is on enough WAF blocklists to
	// draw a 403, and a 403 used to cost the icon six hours. ESR is chosen
	// over a mainline release so the version stays plausible for longer.
	fetchUserAgent = "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"

	// fetchAccept advertises exactly the formats decodeIcon can read, so a
	// server that content-negotiates doesn't hand back an AVIF we'd only
	// throw away.
	fetchAccept = "image/png,image/webp,image/svg+xml,image/x-icon,image/vnd.microsoft.icon,image/jpeg,image/gif,*/*;q=0.5"
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
				markFailed(dir, key, failTTLFor(err))
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
	chip := composeChip(img)
	if !visible(chip) {
		return fmt.Errorf("icon rendered blank")
	}
	return writePNG(filepath.Join(dir, key+"-processed.png"), chip)
}

// visible reports whether an image has any pixel at all that would be drawn.
//
// A decode that succeeds and produces nothing is worse than one that fails.
// A failure falls back to the browser chip; a blank chip gets written, cached,
// and leaves a hole in the strip that reads as a bug in the bar rather than as
// an icon we couldn't render.
//
// This is not hypothetical. oksvg draws neither <image> elements nor CSS from
// a <style> block, and Gemini's favicon is a blurred raster clipped to a
// vector path whose own fill is set to none by exactly such a block — so every
// element in it is one oksvg skips, and it rasterises to 4096 transparent
// pixels without ever erroring.
func visible(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0 {
				return true
			}
		}
	}
	return false
}

// --- fetch -----------------------------------------------------------------

// transientErr marks a failure as worth retrying soon: the network was
// unreachable or the server was briefly unwell, as opposed to the icon being
// broken. Only the negative cache reads it, to pick a TTL — see failTTLFor.
type transientErr struct{ err error }

func (e transientErr) Error() string { return e.err.Error() }
func (e transientErr) Unwrap() error { return e.err }

// failTTLFor decides how long a failure suppresses retries of that icon.
// Anything not explicitly marked transient is treated as durable, so a new
// error path errs towards being quiet rather than towards hammering.
func failTTLFor(err error) time.Duration {
	var t transientErr
	if errors.As(err, &t) {
		return transientFailTTL
	}
	return failTTL
}

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
	req.Header.Set("User-Agent", fetchUserAgent)
	req.Header.Set("Accept", fetchAccept)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, transientErr{err} // DNS, refused, timed out: try again soon
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("favicon fetch: HTTP %d", resp.StatusCode)
		// A server that answered has said something durable: a 404 means the
		// icon the page declared isn't there and won't be in five minutes.
		// 429 and 5xx are the server having a moment, so those come back.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, transientErr{err}
		}
		return nil, err
	}
	// Read one byte past the cap so an oversized icon can be told apart from
	// one that merely fills it. Truncating at the cap instead would hand the
	// decoder half a file, and the resulting decode error would blame the
	// icon for being corrupt when it is only too big.
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(resp.Body, maxIconBytes+1)); err != nil {
		return nil, transientErr{err} // connection died mid-body
	}
	if buf.Len() > maxIconBytes {
		return nil, fmt.Errorf("favicon larger than %d bytes", maxIconBytes)
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

// markFailed records a failed icon so it isn't retried for ttl.
//
// The marker carries its own TTL as its contents rather than the TTL being a
// constant read at check time: the two differ by two orders of magnitude, and
// which one applies is known only here, at the point the error was classified.
func markFailed(dir, key string, ttl time.Duration) {
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(failMarker(dir, key), []byte(ttl.String()), 0o644)
}

func failedRecently(dir, key string) bool {
	fi, err := os.Stat(failMarker(dir, key))
	if err != nil {
		return false
	}
	if time.Since(fi.ModTime()) < markedTTL(dir, key) {
		return true
	}
	os.Remove(failMarker(dir, key)) // expired; allow one more attempt
	return false
}

// markedTTL reads back the TTL markFailed stored. An empty or unparseable
// marker is one written before markers carried a TTL, and takes the long one —
// that was the only behaviour those markers ever had.
func markedTTL(dir, key string) time.Duration {
	b, err := os.ReadFile(failMarker(dir, key))
	if err != nil {
		return failTTL
	}
	d, err := time.ParseDuration(strings.TrimSpace(string(b)))
	if err != nil || d <= 0 {
		return failTTL
	}
	return d
}
