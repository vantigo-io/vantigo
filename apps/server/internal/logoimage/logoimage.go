// Package logoimage judges and normalises a logo before a module stores it
// (invoices phase 6 design I1; brands design B6): a PNG or a JPEG by its own
// bytes, never SVG, that image.DecodeConfig decodes as the same format, at
// most 512 KiB, between 16 and 2,000 pixels, encoded again to drop every piece
// of metadata, and then loadable by the PDF renderer. Invoices stores the
// seller's logo with it and identity stores a brand's. Each caller keeps its
// own storage, keys and snapshot rules; this package holds only the judgement.
//
// It is a platform package: it imports the standard library and gofpdf, no
// business module, and any module may import it. A refusal is a Refusal with
// a stable code and a sentence; the caller turns it into its own problem type.
package logoimage

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/phpdave11/gofpdf"
)

// The logo's bounds. MaxBytes bounds the upload; MaxStoredBytes bounds the
// normalised file, which may be larger than the upload — a JPEG of a
// photograph is encoded again — and is refused as CodeTooLarge past it.
// JPEGQuality is what a JPEG is encoded again at, Products' images' quality.
const (
	MaxBytes        = 512 << 10
	MaxStoredBytes  = 4 << 20
	MaxLongerSide   = 2000
	MinShorterSide  = 16
	JPEGQuality     = 90
	FormField       = "file"
	ContentTypePNG  = "image/png"
	ContentTypeJPEG = "image/jpeg"
)

// The refusals' codes, stable across callers.
const (
	CodeUnsupportedType = "logo_unsupported_type"
	CodeTooLarge        = "logo_too_large"
	CodeTooWide         = "logo_too_wide"
	CodeTooSmall        = "logo_too_small"
	CodeInvalid         = "logo_invalid"
)

// Refusal is why a logo was not accepted: a code from the list above and a
// sentence for the person.
type Refusal struct {
	Code   string
	Detail string
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Detail }

// formats is the format image.DecodeConfig names for each type a logo may be,
// keyed by the type its bytes sniff as, and the extension its key takes.
var formats = map[string]struct{ format, extension string }{
	ContentTypePNG:  {"png", "png"},
	ContentTypeJPEG: {"jpeg", "jpg"},
}

// Extension is the file extension a stored logo of contentType takes, "" for
// a type that is not a logo's.
func Extension(contentType string) string { return formats[contentType].extension }

// Supported reports whether contentType is one a logo may be.
func Supported(contentType string) bool {
	_, ok := formats[contentType]
	return ok
}

func refuse(code, detail string) *Refusal { return &Refusal{Code: code, Detail: detail} }

func unsupported() *Refusal { return refuse(CodeUnsupportedType, "A logo is a PNG or a JPEG") }

func tooLarge() *Refusal { return refuse(CodeTooLarge, "A logo is at most 512 KiB") }

func unreadable() *Refusal {
	return refuse(CodeInvalid, "The request's body could not be read as a multipart form")
}

// Read reads the one file part of mr, at most MaxBytes+1 bytes of it — enough
// to tell "too large" from "just within" — skipping any other part. It answers
// the bytes, or the refusal: CodeTooLarge past the bound (the router's own cap
// included), CodeInvalid for a body that is not a readable multipart form, no
// file part, an empty one, or two.
func Read(mr *multipart.Reader) ([]byte, *Refusal) {
	if mr == nil {
		return nil, unreadable()
	}
	fail := func(err error) ([]byte, *Refusal) {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			return nil, tooLarge()
		}
		return nil, unreadable()
	}
	var data []byte
	files := 0
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fail(err)
		}
		if part.FormName() != FormField {
			_ = part.Close()
			continue
		}
		files++
		body, err := io.ReadAll(io.LimitReader(part, MaxBytes+1))
		_ = part.Close()
		if err != nil {
			return fail(err)
		}
		if len(body) > MaxBytes {
			return nil, tooLarge()
		}
		data = body
	}
	switch {
	case files > 1:
		return nil, refuse(CodeInvalid, "A request sets one logo; this one carried more than one file")
	case files == 0 || len(data) == 0:
		return nil, refuse(CodeInvalid, "A logo file is required, as the multipart part named 'file'")
	}
	return data, nil
}

// Info is what Judge learned of an accepted logo.
type Info struct {
	ContentType   string
	Width, Height int
}

// IsSquare reports whether the sides are within 5 % of each other, as a
// brand's icon must be: the longer is at most 1.05 times the shorter.
func (i Info) IsSquare() bool {
	longer, shorter := max(i.Width, i.Height), min(i.Width, i.Height)
	return shorter > 0 && longer*100 <= shorter*105
}

// Judge is what data is, or its refusal: a PNG or JPEG by its own bytes that
// image.DecodeConfig — which reads the header alone, never decompressing, so a
// decompression bomb costs nothing — decodes as that same format, at most
// MaxLongerSide on its longer side and at least MinShorterSide on its shorter.
func Judge(data []byte) (Info, *Refusal) {
	contentType := http.DetectContentType(data)
	want, ok := formats[contentType]
	if !ok {
		return Info{}, unsupported()
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != want.format {
		return Info{}, unsupported()
	}
	longer, shorter := max(cfg.Width, cfg.Height), min(cfg.Width, cfg.Height)
	switch {
	case longer > MaxLongerSide:
		return Info{}, refuse(CodeTooWide, fmt.Sprintf("A logo is at most %d pixels on its longer side", MaxLongerSide))
	case shorter < MinShorterSide:
		return Info{}, refuse(CodeTooSmall, fmt.Sprintf("A logo is at least %d pixels on its shorter side", MinShorterSide))
	}
	return Info{ContentType: contentType, Width: cfg.Width, Height: cfg.Height}, nil
}

// Normalise is a judged logo as it is stored: decoded whole — bounded by
// Judge's 2,000 pixels — and encoded again, a PNG from an 8-bit NRGBA picture
// (never interlaced, never 16-bit), a JPEG as baseline at quality 90. Encoding
// again drops every piece of metadata. The result must then load in the PDF
// renderer: gofpdf refuses some files silently at render time (an interlaced
// PNG among them), and maroto would print its error into every document issued
// with the logo, so one gofpdf will not take is CodeInvalid. The probe lives
// here, not in the caller, because a brand's logo is printed on documents too.
func Normalise(data []byte, contentType string) ([]byte, *Refusal) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, unsupported()
	}
	b := src.Bounds()
	flat := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(flat, flat.Bounds(), src, b.Min, draw.Src)
	var out bytes.Buffer
	pdfType := "PNG"
	if contentType == ContentTypeJPEG {
		pdfType = "JPG"
		err = jpeg.Encode(&out, flat, &jpeg.Options{Quality: JPEGQuality})
	} else {
		err = png.Encode(&out, flat)
	}
	if err != nil {
		return nil, unsupported()
	}
	if out.Len() > MaxStoredBytes {
		return nil, tooLarge()
	}
	probe := gofpdf.New("P", "mm", "A4", "")
	probe.RegisterImageOptionsReader("logo", gofpdf.ImageOptions{ImageType: pdfType}, bytes.NewReader(out.Bytes()))
	if probe.Err() {
		return nil, refuse(CodeInvalid, "This picture cannot be printed on a document; save it again as an ordinary PNG or JPEG")
	}
	return out.Bytes(), nil
}
