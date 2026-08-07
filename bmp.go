package main

import (
	"bytes"
	"image"

	"golang.org/x/image/bmp"
)

// decodeBMP is split out so favicon.go doesn't import x/image/bmp directly;
// ICO's bare-DIB entries are the only thing that needs it.
func decodeBMP(data []byte) (image.Image, error) {
	return bmp.Decode(bytes.NewReader(data))
}
