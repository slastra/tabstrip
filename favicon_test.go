package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes builds a solid square PNG of the given size.
func pngBytes(t *testing.T, size int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// buildICO assembles an ICO whose directory entries deliberately LIE about
// their dimensions, declaring 256x256 (encoded as 0) for every entry. This is
// what a real favicon in the wild did, and it made stricter decoders reject
// the file outright.
func buildICO(t *testing.T, payloads [][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 1, 0})
	binary.Write(&buf, binary.LittleEndian, uint16(len(payloads)))

	offset := 6 + 16*len(payloads)
	for _, p := range payloads {
		buf.Write([]byte{0, 0, 0, 0}) // width=0, height=0 (=256), colors, reserved
		binary.Write(&buf, binary.LittleEndian, uint16(1))
		binary.Write(&buf, binary.LittleEndian, uint16(32))
		binary.Write(&buf, binary.LittleEndian, uint32(len(p)))
		binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(p)
	}
	for _, p := range payloads {
		buf.Write(p)
	}
	return buf.Bytes()
}

func TestDecodeICOIgnoresLyingHeaders(t *testing.T) {
	ico := buildICO(t, [][]byte{
		pngBytes(t, 16, color.RGBA{R: 255, A: 255}),
		pngBytes(t, 48, color.RGBA{G: 255, A: 255}),
		pngBytes(t, 32, color.RGBA{B: 255, A: 255}),
	})

	img, err := decodeIcon(ico)
	if err != nil {
		t.Fatalf("decodeIcon(ico) failed: %v", err)
	}
	// Largest by ACTUAL decoded area, not by the 256 every entry claimed.
	if got := img.Bounds().Dx(); got != 48 {
		t.Errorf("picked a %dpx entry, want the 48px one", got)
	}
}

func TestDecodeICOSurvivesOneBadEntry(t *testing.T) {
	ico := buildICO(t, [][]byte{
		[]byte("not an image at all"),
		pngBytes(t, 32, color.RGBA{B: 255, A: 255}),
	})
	img, err := decodeIcon(ico)
	if err != nil {
		t.Fatalf("a single bad entry should not condemn the file: %v", err)
	}
	if got := img.Bounds().Dx(); got != 32 {
		t.Errorf("got %dpx, want the decodable 32px entry", got)
	}
}

func TestDecodeIconFormats(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">` +
		`<rect x="0" y="0" width="32" height="32" fill="#ff0000"/></svg>`)

	cases := []struct {
		name string
		data []byte
	}{
		{"png", pngBytes(t, 32, color.RGBA{R: 255, A: 255})},
		{"ico", buildICO(t, [][]byte{pngBytes(t, 32, color.RGBA{R: 255, A: 255})})},
		{"svg", svg},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			img, err := decodeIcon(c.data)
			if err != nil {
				t.Fatalf("decodeIcon: %v", err)
			}
			if img.Bounds().Empty() {
				t.Error("decoded to an empty image")
			}
		})
	}
}

// A small SVG must be rasterised well above the chip size, not rendered at its
// viewBox and upscaled — that upscaling is what made these look mushy.
func TestSVGRastersAboveChipSize(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16">` +
		`<rect width="16" height="16" fill="#000"/></svg>`)
	img, err := decodeIcon(svg)
	if err != nil {
		t.Fatalf("decodeIcon: %v", err)
	}
	if got := img.Bounds().Dx(); got <= iconSize {
		t.Errorf("rasterised at %dpx, want > %dpx so downsampling is possible", got, iconSize)
	}
}

func TestDecodeDataURI(t *testing.T) {
	raw := pngBytes(t, 16, color.RGBA{R: 255, A: 255})
	b64 := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
	if got, err := decodeDataURI(b64); err != nil || !bytes.Equal(got, raw) {
		t.Errorf("base64 data URI round-trip failed: err=%v", err)
	}

	// Percent-encoded, the form inline SVG usually arrives in.
	pct := "data:image/svg+xml,%3Csvg%3E%3C/svg%3E"
	got, err := decodeDataURI(pct)
	if err != nil || string(got) != "<svg></svg>" {
		t.Errorf("percent-encoded data URI = %q, err=%v", got, err)
	}
}

func TestComposeChipGeometry(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	chip := composeChip(src)

	if chip.Bounds().Dx() != iconSize || chip.Bounds().Dy() != iconSize {
		t.Fatalf("chip is %v, want %dx%d", chip.Bounds(), iconSize, iconSize)
	}
	// Corners rounded away, centre intact.
	if _, _, _, a := chip.At(chipInset, chipInset).RGBA(); a != 0 {
		t.Error("chip corner is opaque, want rounded off")
	}
	if _, _, _, a := chip.At(iconSize/2, iconSize/2).RGBA(); a == 0 {
		t.Error("chip centre is transparent")
	}
}

// A wide source must keep its aspect rather than being squashed to square.
func TestComposeChipPreservesAspect(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 128, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 128; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	chip := composeChip(src)
	// Top edge inside the inset should be empty: a 2:1 source letterboxes.
	if _, _, _, a := chip.At(iconSize/2, chipInset+1).RGBA(); a != 0 {
		t.Error("wide source filled the full height, want letterboxed")
	}
	if _, _, _, a := chip.At(iconSize/2, iconSize/2).RGBA(); a == 0 {
		t.Error("centre band should carry the image")
	}
}

func TestEnsureChipsWritesAndNegativeCaches(t *testing.T) {
	dir := t.TempDir()
	raw := pngBytes(t, 32, color.RGBA{R: 200, G: 40, B: 40, A: 255})
	good := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
	bad := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("garbage"))

	if err := ensureChip(dir, good, iconKey(good)); err != nil {
		t.Fatalf("ensureChip(good): %v", err)
	}
	if !exists(filepath.Join(dir, iconKey(good)+"-processed.png")) {
		t.Error("chip was not written")
	}

	if err := ensureChip(dir, bad, iconKey(bad)); err == nil {
		t.Error("undecodable icon should error")
	}
	markFailed(dir, iconKey(bad))
	if !failedRecently(dir, iconKey(bad)) {
		t.Error("failure marker should suppress retries")
	}
	if failedRecently(dir, iconKey(good)) {
		t.Error("a healthy icon must not be marked failed")
	}
}

func TestFetchIconHTTP(t *testing.T) {
	raw := pngBytes(t, 16, color.RGBA{B: 255, A: 255})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/missing.ico") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write(raw)
	}))
	defer srv.Close()

	got, err := fetchIcon(srv.URL + "/favicon.png")
	if err != nil || !bytes.Equal(got, raw) {
		t.Errorf("fetchIcon over http failed: err=%v", err)
	}
	if _, err := fetchIcon(srv.URL + "/missing.ico"); err == nil {
		t.Error("404 should be an error, not an empty icon")
	}
	if _, err := fetchIcon("ftp://example.com/favicon.ico"); err == nil {
		t.Error("unsupported scheme should error")
	}
}

func TestEnsureChipsSkipsCachedAndEmpty(t *testing.T) {
	dir := t.TempDir()
	raw := pngBytes(t, 16, color.RGBA{A: 255})
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)

	// Pre-seed the chip; EnsureChips must not overwrite it.
	chip := filepath.Join(dir, iconKey(url)+"-processed.png")
	if err := os.WriteFile(chip, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	EnsureChips(dir, []TabInfo{{FavIconURL: url}, {FavIconURL: ""}})

	got, _ := os.ReadFile(chip)
	if string(got) != "sentinel" {
		t.Error("EnsureChips overwrote an already-cached chip")
	}
	if exists(filepath.Join(dir, iconKey("")+"-processed.png")) {
		t.Error("empty favicon URL should not produce a chip")
	}
}
