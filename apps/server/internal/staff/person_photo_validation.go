package staff

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg" // registers image/jpeg with image.DecodeConfig
	_ "image/png"  // registers image/png with image.DecodeConfig
	"io"
	"mime/multipart"
	"net/http"
)

// This file reads and judges a photo's multipart body (person-glance design
// §4, "The photo"): one part, file, read no further than the bound allows,
// and its own bytes deciding what it is — the name and the type it was sent
// under are no evidence either way, so a JPEG a phone called .png is a JPEG.

// The photo's bounds (§4). maxPhotoRequestBytes is the router's cap for the
// upload (bodyLimits): the file plus the multipart framing, the documents'
// margin.
const (
	maxPhotoBytes        = 5 * 1024 * 1024
	maxPhotoRequestBytes = maxPhotoBytes + 64*1024
	maxPhotoDimension    = 4096
)

// The photo's refusals, each photo_invalid on file and naming its rule.
const (
	photoTypeMessage      = "The photo must be a JPEG or PNG"
	photoSizeMessage      = "A photo is at most 5 MiB"
	photoDimensionMessage = "A photo is at most 4096 pixels on each side"
)

// photoFormats is the format image.DecodeConfig names for each type a photo
// may be, keyed by the type its bytes sniff as.
var photoFormats = map[string]string{"image/jpeg": "jpeg", "image/png": "png"}

// photoForm is one photo upload as read: the file's bytes and how many file
// parts came; tooLarge and unreadable say why the file could not be had.
type photoForm struct {
	data       []byte
	files      int
	tooLarge   bool
	unreadable bool
}

// readPhotoForm reads every part of mr, the file up to maxPhotoBytes+1 bytes
// — enough to tell "too large" from "just within" — and skips any other part.
// A read the router's own cap stops (http.MaxBytesError) is too large as
// well, and any other failure an unreadable body.
func readPhotoForm(mr *multipart.Reader) photoForm {
	var f photoForm
	if mr == nil {
		f.unreadable = true
		return f
	}
	fail := func(err error) photoForm {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			f.tooLarge = true
		} else {
			f.unreadable = true
		}
		return f
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return f
		}
		if err != nil {
			return fail(err)
		}
		if part.FormName() != documentFormField {
			_ = part.Close()
			continue
		}
		f.files++
		data, err := io.ReadAll(io.LimitReader(part, maxPhotoBytes+1))
		_ = part.Close()
		if err != nil {
			return fail(err)
		}
		if len(data) > maxPhotoBytes {
			f.tooLarge = true
			return f
		}
		f.data = data
	}
}

// parsePhoto judges f: one file, within the bound, a JPEG or PNG by its own
// bytes that image.DecodeConfig — which reads the header alone, never
// decompressing, so a decompression bomb costs nothing — decodes as that same
// format within maxPhotoDimension on each side (identity's avatar rule). It
// answers the content type the photo is stored and served as.
func parsePhoto(f photoForm) (string, fieldErrors) {
	var errs fieldErrors
	switch {
	case f.tooLarge:
		errs.addCoded(documentFormField, codePhotoInvalid, photoSizeMessage)
	case f.unreadable:
		errs.add(documentFormField, "The request's body could not be read as a multipart form")
	case f.files > 1:
		errs.add(documentFormField, "A request sets one photo; this one carried more than one file")
	case f.files == 0 || len(f.data) == 0:
		errs.add(documentFormField, "A photo file is required")
	default:
		contentType, message := photoTypeOf(f.data)
		errs.addCoded(documentFormField, codePhotoInvalid, message)
		return contentType, errs
	}
	return "", errs
}

// photoTypeOf is the type data is stored as, or the rule it breaks.
func photoTypeOf(data []byte) (contentType, message string) {
	kind, ok := sniffDocumentType(data)
	format, known := photoFormats[kind.ContentType]
	if !ok || !known {
		return "", photoTypeMessage
	}
	cfg, decoded, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || decoded != format {
		return "", photoTypeMessage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxPhotoDimension || cfg.Height > maxPhotoDimension {
		return "", photoDimensionMessage
	}
	return kind.ContentType, ""
}
