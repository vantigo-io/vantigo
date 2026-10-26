package entra

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// The photo's conversion (staff photo sync, spec §4 "Staff → Entra"): any
// JPEG or PNG Vantigo holds becomes a JPEG under 4 MB whose longer side is
// at most 1024 px, over white where the PNG was transparent; what cannot be
// decoded is an error.

// encodePNG is img as a PNG.
func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// encodeJPEG is img as a JPEG at quality.
func encodeJPEG(t *testing.T, img image.Image, quality int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// filled is a w×h picture of c.
func filled(w, h int, c color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	return img
}

// noise is a w×h picture of random pixels: the worst a JPEG compresses.
func noise(w, h int) *image.RGBA {
	r := rand.New(rand.NewPCG(1, 2))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.UintN(256))
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff
	}
	return img
}

// convertedJPEG converts p, which must succeed, and decodes the answer as a
// JPEG under photoMaxBytes.
func convertedJPEG(t *testing.T, p contracts.Photo) image.Image {
	t.Helper()
	out, err := convertPhoto(p)
	if err != nil {
		t.Fatalf("convertPhoto: %v", err)
	}
	if len(out) > photoMaxBytes {
		t.Errorf("the photo weighs %d bytes, over %d", len(out), photoMaxBytes)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("the photo is not a JPEG: %v", err)
	}
	return img
}

func TestConvertPhoto_ALargePNGIsScaledToALongerSideOf1024(t *testing.T) {
	t.Parallel()
	img := convertedJPEG(t, contracts.Photo{ContentType: "image/png", Data: encodePNG(t, filled(3000, 2000, color.NRGBA{R: 40, G: 90, B: 160, A: 255}))})
	if b := img.Bounds(); b.Dx() != 1024 || b.Dy() != 682 {
		t.Errorf("the photo is %d×%d, want 1024×682", b.Dx(), b.Dy())
	}
	r, g, b, _ := img.At(512, 341).RGBA()
	if near(r>>8, 40) && near(g>>8, 90) && near(b>>8, 160) {
		return
	}
	t.Errorf("the photo's colour is %d,%d,%d, want about 40,90,160", r>>8, g>>8, b>>8)
}

func TestConvertPhoto_APortraitIsScaledByItsHeight(t *testing.T) {
	t.Parallel()
	img := convertedJPEG(t, contracts.Photo{ContentType: "image/jpeg", Data: encodeJPEG(t, filled(1500, 2000, color.Gray{Y: 128}), 90)})
	if b := img.Bounds(); b.Dx() != 768 || b.Dy() != 1024 {
		t.Errorf("the photo is %d×%d, want 768×1024", b.Dx(), b.Dy())
	}
}

func TestConvertPhoto_ASmallJPEGKeepsItsSize(t *testing.T) {
	t.Parallel()
	in := encodeJPEG(t, filled(64, 48, color.Gray{Y: 200}), 90)
	out, err := convertPhoto(contracts.Photo{ContentType: "image/jpeg", Data: in})
	if err != nil {
		t.Fatalf("convertPhoto: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("the photo is not a JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 64 || b.Dy() != 48 {
		t.Errorf("the photo is %d×%d, want 64×48", b.Dx(), b.Dy())
	}
	if len(out) > 10_000 {
		t.Errorf("a 64×48 photo weighs %d bytes", len(out))
	}
}

func TestConvertPhoto_TransparencyBecomesWhite(t *testing.T) {
	t.Parallel()
	img := convertedJPEG(t, contracts.Photo{ContentType: "image/png", Data: encodePNG(t, filled(200, 200, color.NRGBA{}))})
	r, g, b, _ := img.At(100, 100).RGBA()
	if !near(r>>8, 255) || !near(g>>8, 255) || !near(b>>8, 255) {
		t.Errorf("a transparent pixel is %d,%d,%d, want white", r>>8, g>>8, b>>8)
	}
}

func TestConvertPhoto_ANoisy4096SquareJPEGLandsUnder4MB(t *testing.T) {
	t.Parallel()
	img := convertedJPEG(t, contracts.Photo{ContentType: "image/jpeg", Data: encodeJPEG(t, noise(4096, 4096), 100)})
	if b := img.Bounds(); b.Dx() != 1024 || b.Dy() != 1024 {
		t.Errorf("the photo is %d×%d, want 1024×1024", b.Dx(), b.Dy())
	}
}

func TestConvertPhoto_WhatCannotBeDecodedIsRefused(t *testing.T) {
	t.Parallel()
	for name, data := range map[string][]byte{
		"garbage":           []byte("not a picture at all"),
		"empty":             nil,
		"a truncated JPEG":  encodeJPEG(t, filled(100, 100, color.Gray{Y: 10}), 90)[:200],
		"wider than admits": encodePNG(t, filled(photoMaxSourceSide+1, 1, color.Gray{Y: 10})),
	} {
		if out, err := convertPhoto(contracts.Photo{ContentType: "image/jpeg", Data: data}); !errors.Is(err, errPhotoUndecodable) || out != nil {
			t.Errorf("%s: %d bytes, %v; want errPhotoUndecodable", name, len(out), err)
		}
	}
}

// near reports whether a colour channel is within a JPEG's rounding of want.
func near(got uint32, want uint32) bool {
	return got+6 >= want && got <= want+6
}
