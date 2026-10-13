// Package qrsvg renders a QR code as an SVG image, built here from the
// code's modules and nothing else: the encoded text never appears in the
// image, and the image holds three elements — svg, rect and path — with no
// script, no event attribute, no link and no external reference. It is a
// leaf of the payments port, so the Vipps adapter (an on-site payment's QR)
// and Invoices (a pay link's QR) render with the one renderer.
//
// Show it as an image — <img src="data:image/svg+xml;base64,…"> — never
// inlined into a page's markup.
package qrsvg

import (
	"errors"
	"fmt"
	"image/color"
	"strings"

	"github.com/boombuler/barcode/qr"
)

// quietZone is the light margin, in modules, a reader needs around a code.
const quietZone = 4

// Render is text as a QR code (error correction M) in an SVG whose user
// units are modules, so it scales without blurring.
func Render(text string) (string, error) {
	if text == "" {
		return "", errors.New("qrsvg: nothing to encode")
	}
	code, err := qr.Encode(text, qr.M, qr.Auto)
	if err != nil {
		return "", fmt.Errorf("qrsvg: encode: %w", err)
	}
	b := code.Bounds()
	size := b.Dx() + 2*quietZone
	var d strings.Builder
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if dark(code.At(x, y)) {
				fmt.Fprintf(&d, "M%d %dh1v1h-1z", x-b.Min.X+quietZone, y-b.Min.Y+quietZone)
			}
		}
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`+
		`<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="%s"/></svg>`, size, size, size, size, d.String()), nil
}

func dark(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r+g+b < 3*0x8000
}
