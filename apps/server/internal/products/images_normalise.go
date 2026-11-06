package products

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"

	"github.com/vantigo-io/vantigo/server/internal/imagemeta"
)

// This file is the normalisation every uploaded image goes through before it
// is stored (messenger design N13); only the normalised file is kept.
//
//  1. image.DecodeConfig reads the size before anything is decoded, so more
//     than 40 megapixels is refused at no cost — a decompression bomb costs
//     nothing.
//  2. The EXIF orientation is read, through imagemeta.Orientation.
//  3. The picture is decoded, scaled so its longer side is at most 2048
//     pixels, turned upright and encoded again: JPEG at quality 90, or PNG.
//  4. Encoding again drops all metadata, so no camera location reaches a
//     customer.
//  5. A thumbnail, its longer side at most 320 pixels, is made in the same
//     pass.
//
// Scaling before turning is the same picture as turning before scaling, and
// turns the smaller one.

const (
	// maxImageBytes is the largest file an upload may be.
	maxImageBytes = 10 * 1024 * 1024
	// maxImageRequestBytes is the router's cap for the upload: the file plus
	// room for the multipart framing, Expenses' and staff's margin.
	maxImageRequestBytes = maxImageBytes + 64*1024
	// maxImagePixels is the most pixels a decoded picture may have.
	maxImagePixels = 40_000_000
	// minImageSide is the shortest side a picture may have.
	minImageSide = 200
	// maxImageSide and thumbSide are the longer side of the stored picture
	// and of its thumbnail.
	maxImageSide = 2048
	thumbSide    = 320
	// maxConcurrentNormalisations is how many uploads are normalised at once.
	maxConcurrentNormalisations = 3
	// jpegQuality is the quality the stored JPEG and its thumbnail are
	// encoded at.
	jpegQuality = 90
)

// The codes of an image's refusals, 422 each (storage_unavailable is a 503).
const (
	codeImageTypeUnsupported = "image_type_unsupported"
	codeImageTooLarge        = "image_too_large"
	codeImageTooSmall        = "image_too_small"
	codeImageInvalid         = "image_invalid"
	codeStorageUnavailable   = "storage_unavailable"
)

// imageRefusal is why an upload was refused: the code and the sentence a
// person reads.
type imageRefusal struct {
	code, title, detail string
}

func (r *imageRefusal) Error() string { return r.code }

// normalisedImage is an upload as it is stored: the picture and its
// thumbnail, each a whole file of contentType, and the picture's size.
type normalisedImage struct {
	contentType   string // "image/jpeg" | "image/png"
	data, thumb   []byte
	width, height int
	sha256        string
}

// normaliseImage judges raw — a JPEG or a PNG by its own bytes, never by the
// name or type it was sent under — and answers it normalised, or why not.
func normaliseImage(raw []byte) (*normalisedImage, *imageRefusal) {
	var contentType string
	switch {
	case bytes.HasPrefix(raw, []byte{0xFF, 0xD8, 0xFF}):
		contentType = "image/jpeg"
	case bytes.HasPrefix(raw, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		contentType = "image/png"
	default:
		return nil, &imageRefusal{codeImageTypeUnsupported, "Image type not supported",
			"Only JPEG and PNG images can be uploaded; WebP and HEIC cannot."}
	}
	broken := &imageRefusal{codeImageInvalid, "Image cannot be read", "The file could not be read as a picture."}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format == "jpeg") != (contentType == "image/jpeg") {
		return nil, broken
	}
	// The product of two int32-sized sides fits an int on every platform the
	// server runs on.
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxImagePixels {
		return nil, &imageRefusal{codeImageTooLarge, "Image too large",
			"The picture has more than 40 megapixels."}
	}
	if cfg.Width < minImageSide || cfg.Height < minImageSide {
		return nil, &imageRefusal{codeImageTooSmall, "Image too small",
			"The picture must be at least 200 by 200 pixels."}
	}
	// DecodeConfig has read the picture, so an orientation that cannot be read
	// is no reason to refuse it: it is taken as upright.
	orientation, err := imagemeta.Orientation(raw)
	if err != nil {
		orientation = 1
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, broken
	}

	upright := orient(fit(src, maxImageSide), orientation)
	thumb := fit(upright, thumbSide)
	data, err := encodeImage(contentType, upright)
	if err != nil {
		return nil, broken
	}
	thumbData, err := encodeImage(contentType, thumb)
	if err != nil {
		return nil, broken
	}
	sum := sha256.Sum256(data)
	return &normalisedImage{
		contentType: contentType, data: data, thumb: thumbData,
		width: upright.Bounds().Dx(), height: upright.Bounds().Dy(),
		sha256: hex.EncodeToString(sum[:]),
	}, nil
}

// encodeImage is img as a file of contentType: a JPEG at jpegQuality or a
// PNG, neither with any metadata.
func encodeImage(contentType string, img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	switch contentType {
	case "image/jpeg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, err
		}
	case "image/png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("products: no encoder for " + contentType)
	}
	return buf.Bytes(), nil
}

// fit is src as an NRGBA picture whose longer side is at most side, scaled
// down with Catmull-Rom; a smaller picture is copied at its own size, never
// scaled up.
func fit(src image.Image, side int) *image.NRGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > side || h > side {
		if w >= h {
			w, h = side, max(1, h*side/w)
		} else {
			w, h = max(1, w*side/h), side
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

// orient turns img upright from its EXIF orientation (1 to 8): 2 mirrored,
// 3 turned half a circle, 4 mirrored vertically, 5 transposed, 6 a quarter
// turn clockwise from how it is held, 7 transversed, 8 a quarter turn
// counter-clockwise. 1, and any other value, is img itself.
func orient(img *image.NRGBA, orientation int) *image.NRGBA {
	if orientation < 2 || orientation > 8 {
		return img
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	ow, oh := w, h
	if orientation >= 5 {
		ow, oh = h, w
	}
	out := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for y := range oh {
		for x := range ow {
			var sx, sy int
			switch orientation {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			copy(out.Pix[out.PixOffset(x, y):out.PixOffset(x, y)+4], img.Pix[img.PixOffset(sx, sy):img.PixOffset(sx, sy)+4])
		}
	}
	return out
}
