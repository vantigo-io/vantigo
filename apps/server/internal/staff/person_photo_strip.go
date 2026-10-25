package staff

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// This file strips a photo's metadata before it is stored (person-glance
// design §4, "The photo"; server review M2): a phone's photo carries where
// and when it was taken, the device and often a thumbnail of the uncropped
// picture, and none of it is the person's face. The pixels are never
// decoded — the segments and chunks are walked and copied, all but the
// metadata ones — so a decompression bomb still costs nothing, and a file
// without metadata is stored byte for byte as it came.

// errPhotoStructure is a photo whose segments or chunks do not hold
// together: cut short, a length running past the end, or no image data.
var errPhotoStructure = errors.New("staff: the photo's structure is broken")

// jpegDropped are the JPEG markers whose segments are left out: APP1 (Exif
// and XMP) and APP13 (Photoshop's IPTC).
var jpegDropped = map[byte]bool{0xE1: true, 0xED: true}

// pngDropped are the PNG chunk types left out: Exif and the three kinds of
// text.
var pngDropped = map[string]bool{"eXIf": true, "tEXt": true, "zTXt": true, "iTXt": true}

// pngSignature opens every PNG.
var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// stripPhotoMetadata is data, a photo of contentType that parsePhoto
// accepted, without its metadata; errPhotoStructure when it cannot be
// walked.
func stripPhotoMetadata(contentType string, data []byte) ([]byte, error) {
	switch contentType {
	case "image/jpeg":
		return stripJPEG(data)
	case "image/png":
		return stripPNG(data)
	}
	return nil, errPhotoStructure
}

// stripJPEG copies data's segments from the SOI on, but APP1 and APP13's,
// up to the first SOS, and from there the rest verbatim: the entropy-coded
// data that follows is not segments. Fill bytes before a marker are dropped.
func stripJPEG(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, errPhotoStructure
	}
	out := make([]byte, 0, len(data))
	out = append(out, data[:2]...)
	i := 2
	for {
		if i >= len(data) || data[i] != 0xFF {
			return nil, errPhotoStructure
		}
		for i < len(data) && data[i] == 0xFF {
			i++
		}
		if i >= len(data) {
			return nil, errPhotoStructure
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
			return nil, errPhotoStructure
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			// TEM and RSTn stand alone, with no length.
			out = append(out, 0xFF, marker)
			continue
		}
		if i+2 > len(data) {
			return nil, errPhotoStructure
		}
		length := int(binary.BigEndian.Uint16(data[i:]))
		if length < 2 || i+length > len(data) {
			return nil, errPhotoStructure
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
		return nil, errPhotoStructure
	}
	out := make([]byte, 0, len(data))
	out = append(out, pngSignature...)
	i := len(pngSignature)
	for {
		if i+8 > len(data) {
			return nil, errPhotoStructure
		}
		length := binary.BigEndian.Uint32(data[i:])
		if length > uint32(len(data)) {
			return nil, errPhotoStructure
		}
		end := i + 8 + int(length) + 4
		if end > len(data) {
			return nil, errPhotoStructure
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
