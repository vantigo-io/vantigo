// Package imagemeta strips a picture's metadata before it is stored or
// served (person-glance design §4, "The photo"; server review M2; the photo
// sync review's privacy finding): a phone's photo carries where and when it
// was taken, the device and often a thumbnail of the uncropped picture, and
// none of it is the person's face. Staff strips HR's photo of a person, and
// identity a user's own avatar and the managed one, since Staff serves
// either to the person's colleagues. The pixels are never decoded — the
// segments and chunks are walked and copied, all but the metadata ones — so
// a decompression bomb still costs nothing, and a file without metadata is
// returned byte for byte as it came. It is a platform package: it imports
// the standard library alone, and any module may import it.
package imagemeta

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// ErrStructure is a picture whose segments or chunks do not hold together:
// cut short, a length running past the end, or no image data.
var ErrStructure = errors.New("imagemeta: the picture's structure is broken")

// jpegDropped are the JPEG markers whose segments are left out: APP1 (Exif
// and XMP) and APP13 (Photoshop's IPTC).
var jpegDropped = map[byte]bool{0xE1: true, 0xED: true}

// pngDropped are the PNG chunk types left out: Exif and the three kinds of
// text.
var pngDropped = map[string]bool{"eXIf": true, "tEXt": true, "zTXt": true, "iTXt": true}

// pngSignature opens every PNG.
var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// Strip is data, a picture of contentType — "image/jpeg" or "image/png",
// as its own bytes say — without its metadata: a JPEG's APP1 (Exif, XMP) and
// APP13 (IPTC) segments, a PNG's eXIf, tEXt, zTXt and iTXt chunks.
// ErrStructure when it cannot be walked, or for any other type.
func Strip(contentType string, data []byte) ([]byte, error) {
	switch contentType {
	case "image/jpeg":
		return stripJPEG(data)
	case "image/png":
		return stripPNG(data)
	}
	return nil, ErrStructure
}

// stripJPEG copies data's segments from the SOI on, but APP1 and APP13's,
// up to the first SOS, and from there the rest verbatim: the entropy-coded
// data that follows is not segments. Fill bytes before a marker are dropped.
func stripJPEG(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, ErrStructure
	}
	out := make([]byte, 0, len(data))
	out = append(out, data[:2]...)
	i := 2
	for {
		if i >= len(data) || data[i] != 0xFF {
			return nil, ErrStructure
		}
		for i < len(data) && data[i] == 0xFF {
			i++
		}
		if i >= len(data) {
			return nil, ErrStructure
		}
		marker := data[i]
		start := i - 1
		i++
		switch {
		case marker == 0xDA:
			return append(out, data[start:]...), nil
		case marker == 0x00 || marker == 0xD8 || marker == 0xD9:
			// A stuffed zero, a second SOI or an EOI before any scan:
			// no image data.
			return nil, ErrStructure
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			// TEM and RSTn stand alone, with no length.
			out = append(out, 0xFF, marker)
			continue
		}
		if i+2 > len(data) {
			return nil, ErrStructure
		}
		length := int(binary.BigEndian.Uint16(data[i:]))
		if length < 2 || i+length > len(data) {
			return nil, ErrStructure
		}
		if !jpegDropped[marker] {
			out = append(out, 0xFF, marker)
			out = append(out, data[i:i+length]...)
		}
		i += length
	}
}

// stripPNG copies data's signature and chunks, but the metadata ones,
// through IEND; anything after IEND is no chunk and is dropped.
func stripPNG(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, pngSignature) {
		return nil, ErrStructure
	}
	out := make([]byte, 0, len(data))
	out = append(out, pngSignature...)
	i := len(pngSignature)
	for {
		if i+8 > len(data) {
			return nil, ErrStructure
		}
		length := binary.BigEndian.Uint32(data[i:])
		if length > uint32(len(data)) {
			return nil, ErrStructure
		}
		end := i + 8 + int(length) + 4
		if end > len(data) {
			return nil, ErrStructure
		}
		kind := string(data[i+4 : i+8])
		if !pngDropped[kind] {
			out = append(out, data[i:end]...)
		}
		i = end
		if kind == "IEND" {
			return out, nil
		}
	}
}
