package logoimage_test

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"

	"github.com/phpdave11/gofpdf"

	"github.com/vantigo-io/vantigo/server/internal/logoimage"
)

// encoded is an image of w × h pixels encoded by encode.
func encoded(t *testing.T, w, h int, encode func(*bytes.Buffer, image.Image) error) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	img.Set(0, 0, color.Black)
	var b bytes.Buffer
	if err := encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func pngOf(b *bytes.Buffer, img image.Image) error  { return png.Encode(b, img) }
func jpegOf(b *bytes.Buffer, img image.Image) error { return jpeg.Encode(b, img, nil) }
func gifOf(b *bytes.Buffer, img image.Image) error  { return gif.Encode(b, img, nil) }

func code(r *logoimage.Refusal) string {
	if r == nil {
		return ""
	}
	return r.Code
}

// A logo is a PNG or a JPEG by its own bytes, at most 2,000 pixels on its
// longer side and at least 16 on its shorter; anything else is refused with
// its code.
func TestJudge_TheRules(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		data     []byte
		wantType string
		wantCode string
	}{
		{"a PNG", encoded(t, 300, 100, pngOf), logoimage.ContentTypePNG, ""},
		{"a JPEG", encoded(t, 300, 100, jpegOf), logoimage.ContentTypeJPEG, ""},
		{"2,000 wide", encoded(t, 2000, 16, pngOf), logoimage.ContentTypePNG, ""},
		{"a GIF", encoded(t, 300, 100, gifOf), "", logoimage.CodeUnsupportedType},
		{"not an image", []byte("%PDF-1.4 not a logo"), "", logoimage.CodeUnsupportedType},
		{"an SVG", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"/>`), "", logoimage.CodeUnsupportedType},
		{"a PNG's signature and nothing else", []byte("\x89PNG\r\n\x1a\n"), "", logoimage.CodeUnsupportedType},
		{"2,001 wide", encoded(t, 2001, 100, pngOf), "", logoimage.CodeTooWide},
		{"2,001 high", encoded(t, 100, 2001, jpegOf), "", logoimage.CodeTooWide},
		{"15 high", encoded(t, 300, 15, pngOf), "", logoimage.CodeTooSmall},
	} {
		info, refusal := logoimage.Judge(c.data)
		if info.ContentType != c.wantType || code(refusal) != c.wantCode {
			t.Errorf("%s = %q %q, want %q %q", c.name, info.ContentType, code(refusal), c.wantType, c.wantCode)
		}
	}
}

// The icon of a brand is square within 5 %.
func TestInfo_IsSquare(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		w, h int
		want bool
	}{
		{100, 100, true},
		{105, 100, true},
		{100, 105, true},
		{106, 100, false},
		{100, 106, false},
		{300, 100, false},
		{0, 0, false},
	} {
		got := logoimage.Info{Width: c.w, Height: c.h}.IsSquare()
		if got != c.want {
			t.Errorf("%d x %d square = %v, want %v", c.w, c.h, got, c.want)
		}
	}
	info, refusal := logoimage.Judge(encoded(t, 64, 64, pngOf))
	if refusal != nil || info.Width != 64 || info.Height != 64 || !info.IsSquare() {
		t.Errorf("a 64 x 64 PNG judged %+v %v", info, refusal)
	}
}

func multipartOf(t *testing.T, parts ...[2]string) *multipart.Reader {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, p := range parts {
		pw, err := w.CreateFormField(p[0])
		if err != nil {
			t.Fatal(err)
		}
		_, _ = pw.Write([]byte(p[1]))
	}
	_ = w.Close()
	return multipart.NewReader(&body, w.Boundary())
}

// Read takes the one part named file, no further than the bound allows.
func TestRead_TheRules(t *testing.T) {
	t.Parallel()
	big := string(bytes.Repeat([]byte{1}, logoimage.MaxBytes+1))
	just := string(bytes.Repeat([]byte{1}, logoimage.MaxBytes))
	for _, c := range []struct {
		name     string
		mr       *multipart.Reader
		wantLen  int
		wantCode string
	}{
		{"one file", multipartOf(t, [2]string{"file", "abc"}), 3, ""},
		{"another part skipped", multipartOf(t, [2]string{"note", "x"}, [2]string{"file", "abc"}), 3, ""},
		{"exactly 512 KiB", multipartOf(t, [2]string{"file", just}), logoimage.MaxBytes, ""},
		{"512 KiB and a byte", multipartOf(t, [2]string{"file", big}), 0, logoimage.CodeTooLarge},
		{"no file", multipartOf(t, [2]string{"note", "x"}), 0, logoimage.CodeInvalid},
		{"an empty file", multipartOf(t, [2]string{"file", ""}), 0, logoimage.CodeInvalid},
		{"two files", multipartOf(t, [2]string{"file", "a"}, [2]string{"file", "b"}), 0, logoimage.CodeInvalid},
		{"no reader", nil, 0, logoimage.CodeInvalid},
	} {
		data, refusal := logoimage.Read(c.mr)
		if len(data) != c.wantLen || code(refusal) != c.wantCode {
			t.Errorf("%s = %d bytes %q, want %d %q", c.name, len(data), code(refusal), c.wantLen, c.wantCode)
		}
	}
}

func TestExtension(t *testing.T) {
	t.Parallel()
	if logoimage.Extension(logoimage.ContentTypePNG) != "png" || logoimage.Extension(logoimage.ContentTypeJPEG) != "jpg" || logoimage.Extension("image/gif") != "" {
		t.Error("extensions are png, jpg and none for anything else")
	}
	if !logoimage.Supported(logoimage.ContentTypePNG) || logoimage.Supported("image/svg+xml") {
		t.Error("only a PNG and a JPEG are supported")
	}
}

// gofpdfTakes reports whether gofpdf — the renderer under maroto — registers
// data as an image of pdfType ("PNG" or "JPG") without an error.
func gofpdfTakes(data []byte, pdfType string) bool {
	probe := gofpdf.New("P", "mm", "A4", "")
	probe.RegisterImageOptionsReader("probe", gofpdf.ImageOptions{ImageType: pdfType}, bytes.NewReader(data))
	return !probe.Err()
}

// A logo is normalised before it is stored: an interlaced PNG, a 16-bit PNG
// and a progressive JPEG — files gofpdf refuses or mishandles — are accepted,
// encoded again as an 8-bit non-interlaced PNG or a baseline JPEG, and the
// result loads in gofpdf.
func TestNormalise_WhatGofpdfRefusesIsEncodedAgain(t *testing.T) {
	t.Parallel()
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	deep := image.NewRGBA64(image.Rect(0, 0, 64, 32))
	for i := range deep.Pix {
		deep.Pix[i] = byte(i * 7)
	}
	var sixteen bytes.Buffer
	if err := png.Encode(&sixteen, deep); err != nil {
		t.Fatal(err)
	}
	interlaced := read("logo-interlaced.png")
	if gofpdfTakes(interlaced, "PNG") {
		t.Fatal("the interlaced fixture loads in gofpdf as it is; it no longer proves the normalisation")
	}
	for _, c := range []struct {
		name, contentType, pdfType string
		data                       []byte
	}{
		{"an interlaced PNG", logoimage.ContentTypePNG, "PNG", interlaced},
		{"a 16-bit PNG", logoimage.ContentTypePNG, "PNG", sixteen.Bytes()},
		{"a progressive JPEG", logoimage.ContentTypeJPEG, "JPG", read("logo-progressive.jpg")},
	} {
		info, refusal := logoimage.Judge(c.data)
		if refusal != nil || info.ContentType != c.contentType {
			t.Errorf("%s: judged %q %+v", c.name, info.ContentType, refusal)
			continue
		}
		stored, refusal := logoimage.Normalise(c.data, info.ContentType)
		if refusal != nil {
			t.Errorf("%s: refused %s", c.name, refusal.Code)
			continue
		}
		if !gofpdfTakes(stored, c.pdfType) {
			t.Errorf("%s: the normalised file does not load in gofpdf", c.name)
		}
		if info.ContentType == logoimage.ContentTypePNG {
			// IHDR's bit depth (byte 24 of the file) and interlace method (28).
			if _, err := png.DecodeConfig(bytes.NewReader(stored)); err != nil || stored[24] != 8 || stored[28] != 0 {
				t.Errorf("%s: the stored PNG has bit depth %d and interlace %d (%v), want 8 and 0", c.name, stored[24], stored[28], err)
			}
		}
	}
}

// Encoding again strips metadata: a JPEG with a comment segment comes back
// without it.
func TestNormalise_DropsMetadata(t *testing.T) {
	t.Parallel()
	plain := encoded(t, 64, 64, jpegOf)
	marker := []byte("SECRET-GPS-NOTE")
	commented := append([]byte{}, plain[:2]...)
	commented = append(commented, 0xFF, 0xFE, 0x00, byte(len(marker)+2))
	commented = append(commented, marker...)
	commented = append(commented, plain[2:]...)
	if !bytes.Contains(commented, marker) {
		t.Fatal("the fixture lost its marker")
	}
	if _, refusal := logoimage.Judge(commented); refusal != nil {
		t.Fatalf("the commented JPEG is refused: %v", refusal)
	}
	stored, refusal := logoimage.Normalise(commented, logoimage.ContentTypeJPEG)
	if refusal != nil {
		t.Fatal(refusal)
	}
	if bytes.Contains(stored, marker) {
		t.Error("the stored JPEG still carries the comment")
	}
}
