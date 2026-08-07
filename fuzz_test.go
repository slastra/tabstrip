package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// decodeIcon parses bytes a web page controls: a site declares its own
// favIconUrl, tabstrip fetches it and hands the result to a hand-rolled ICO
// parser doing offset arithmetic. A panic there kills the daemon and takes the
// waybar strip with it, so the property under test is simply "never panics,
// whatever it is handed".

func seedPNG(size int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func seedICO(payloads [][]byte) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 1, 0})
	binary.Write(&buf, binary.LittleEndian, uint16(len(payloads)))
	offset := 6 + 16*len(payloads)
	for _, p := range payloads {
		buf.Write([]byte{0, 0, 0, 0})
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

func FuzzDecodeIcon(f *testing.F) {
	p := seedPNG(16)
	f.Add(p)
	f.Add(seedICO([][]byte{p}))
	f.Add(seedICO([][]byte{p, seedPNG(32)}))
	f.Add([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect width="16" height="16"/></svg>`))
	// Shapes that specifically exercise the offset arithmetic.
	f.Add([]byte{0, 0, 1, 0, 1, 0})                              // header claims one entry, none present
	f.Add([]byte{0, 0, 1, 0, 255, 255})                          // 65535 entries claimed
	f.Add(append([]byte{0, 0, 1, 0, 1, 0}, make([]byte, 16)...)) // entry of zeros: offset 0, size 0
	f.Add([]byte{0, 0, 1, 0})                                    // truncated header
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		img, err := decodeIcon(data)
		if err != nil {
			return
		}
		if img == nil {
			t.Fatal("decodeIcon returned a nil image with a nil error")
		}
		b := img.Bounds()
		if b.Dx() < 0 || b.Dy() < 0 {
			t.Fatalf("negative bounds %v", b)
		}
		// Anything that decodes goes straight into composeChip in production,
		// so fuzz the pipeline, not just the parser. Skip absurd dimensions:
		// they'd be a memory bound to enforce, not a crash to fix.
		if b.Dx() > 0 && b.Dy() > 0 && b.Dx() <= 4096 && b.Dy() <= 4096 {
			if chip := composeChip(img); chip.Bounds().Dx() != iconSize {
				t.Fatalf("chip is %v, want %dx%d", chip.Bounds(), iconSize, iconSize)
			}
		}
	})
}

// decodeDataURI splits and decodes page-supplied strings; it should reject
// junk rather than panic on it.
func FuzzDecodeDataURI(f *testing.F) {
	f.Add("data:image/png;base64," + base64.StdEncoding.EncodeToString(seedPNG(8)))
	f.Add("data:image/svg+xml,%3Csvg%3E%3C/svg%3E")
	f.Add("data:")
	f.Add("data:,")
	f.Add("data:;base64,")
	f.Add("data:image/png;base64,!!!!")
	f.Add("")

	f.Fuzz(func(t *testing.T, uri string) {
		_, _ = decodeDataURI(uri)
	})
}
