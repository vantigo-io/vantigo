package imagemeta

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

// stripImage is a w×h image with a little in it, so the encoders write
// real data.
func stripImage(w, h int) image.Image {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	return img
}

func stripJPEGOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, stripImage(w, h), nil); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	return buf.Bytes()
}

func stripPNGOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, stripImage(w, h)); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buf.Bytes()
}

// jpegSegment is one JPEG segment: the marker, its length and payload.
func jpegSegment(marker byte, payload []byte) []byte {
	out := []byte{0xFF, marker, 0, 0}
	binary.BigEndian.PutUint16(out[2:], uint16(len(payload)+2))
	return append(out, payload...)
}

// pngChunk is one PNG chunk with its CRC.
func pngChunk(kind string, payload []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(payload)))
	out = append(out, kind...)
	out = append(out, payload...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(kind), payload...)))
}

// insertAt is data with extra put in at i.
func insertAt(data []byte, i int, extra ...[]byte) []byte {
	out := append([]byte{}, data[:i]...)
	for _, e := range extra {
		out = append(out, e...)
	}
	return append(out, data[i:]...)
}

// A JPEG's APP1 (Exif, XMP) and APP13 (IPTC) segments are left out, every
// other segment and the scan kept: the stripped file decodes to the same
// size and the same pixels.
func TestStrip_JPEG(t *testing.T) {
	t.Parallel()
	clean := stripJPEGOf(t, 24, 16)
	exif := jpegSegment(0xE1, append([]byte("Exif\x00\x00MM\x00\x2a"), []byte("GPS 59.91N 10.75E")...))
	xmp := jpegSegment(0xE1, []byte("http://ns.adobe.com/xap/1.0/\x00<x:xmpmeta>Oslo</x:xmpmeta>"))
	iptc := jpegSegment(0xED, []byte("Photoshop 3.0\x00Byline: Kari"))
	icc := jpegSegment(0xE2, []byte("ICC_PROFILE\x00\x01\x01kept"))
	tagged := insertAt(clean, 2, exif, icc, xmp, iptc)

	got, err := Strip("image/jpeg", tagged)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	for _, secret := range []string{"Exif", "GPS", "xmpmeta", "Oslo", "Photoshop", "Kari"} {
		if bytes.Contains(got, []byte(secret)) {
			t.Errorf("the stripped JPEG still carries %q", secret)
		}
	}
	if !bytes.Contains(got, []byte("ICC_PROFILE")) {
		t.Error("the stripped JPEG lost its colour profile, which is no metadata")
	}
	if want := len(clean) + len(icc); len(got) != want {
		t.Errorf("stripped length %d, want %d: the clean file and the profile", len(got), want)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil || format != "jpeg" || cfg.Width != 24 || cfg.Height != 16 {
		t.Errorf("DecodeConfig = %+v %q %v, want a 24×16 jpeg", cfg, format, err)
	}
	want, _ := jpeg.Decode(bytes.NewReader(clean))
	img, err := jpeg.Decode(bytes.NewReader(got))
	if err != nil || !bytes.Equal(img.(*image.Gray).Pix, want.(*image.Gray).Pix) {
		t.Errorf("the stripped JPEG decodes to other pixels (err %v)", err)
	}
}

// A PNG's eXIf, tEXt, zTXt and iTXt chunks are left out, every other chunk
// kept, and it still decodes to the same size and pixels.
func TestStrip_PNG(t *testing.T) {
	t.Parallel()
	clean := stripPNGOf(t, 20, 30)
	ihdrEnd := len(pngSignature) + 8 + 13 + 4
	tagged := insertAt(clean, ihdrEnd,
		pngChunk("tEXt", []byte("Author\x00Kari Nordmann")),
		pngChunk("eXIf", []byte("MM\x00\x2aGPS 59.91N")),
		pngChunk("zTXt", []byte("Comment\x00\x00compressed")),
		pngChunk("iTXt", []byte("Location\x00\x00\x00\x00\x00Oslo")),
		pngChunk("gAMA", []byte{0, 0, 0xB1, 0x8F}),
	)

	got, err := Strip("image/png", tagged)
	if err != nil {
		t.Fatalf("strip: %v", err)
	}
	for _, secret := range []string{"tEXt", "eXIf", "zTXt", "iTXt", "Kari", "GPS", "Oslo"} {
		if bytes.Contains(got, []byte(secret)) {
			t.Errorf("the stripped PNG still carries %q", secret)
		}
	}
	if !bytes.Contains(got, []byte("gAMA")) {
		t.Error("the stripped PNG lost its gAMA chunk, which is no metadata")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil || format != "png" || cfg.Width != 20 || cfg.Height != 30 {
		t.Errorf("DecodeConfig = %+v %q %v, want a 20×30 png", cfg, format, err)
	}
	img, err := png.Decode(bytes.NewReader(got))
	if err != nil || !bytes.Equal(img.(*image.Gray).Pix, stripImage(20, 30).(*image.Gray).Pix) {
		t.Errorf("the stripped PNG decodes to other pixels (err %v)", err)
	}
}

// A photo with no metadata is stored byte for byte as it came.
func TestStrip_NothingToStripIsIdentical(t *testing.T) {
	t.Parallel()
	for contentType, data := range map[string][]byte{
		"image/jpeg": stripJPEGOf(t, 8, 8),
		"image/png":  stripPNGOf(t, 8, 8),
	} {
		got, err := Strip(contentType, data)
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("%s: strip = %d bytes, %v; want the %d bytes unchanged", contentType, len(got), err, len(data))
		}
	}
}

// A file that cannot be walked — garbage, cut short at any byte, a length
// running past the end, no scan or no IEND — is refused, never a panic.
func TestStrip_ABrokenFileIsRefused(t *testing.T) {
	t.Parallel()
	jpg := insertAt(stripJPEGOf(t, 8, 8), 2, jpegSegment(0xE1, []byte("Exif\x00\x00")))
	pngData := insertAt(stripPNGOf(t, 8, 8), len(pngSignature)+8+13+4, pngChunk("tEXt", []byte("a\x00b")))

	for name, c := range map[string]struct {
		contentType string
		data        []byte
	}{
		"garbage as a JPEG":           {"image/jpeg", []byte("not an image at all")},
		"garbage as a PNG":            {"image/png", []byte("not an image at all")},
		"an empty JPEG":               {"image/jpeg", nil},
		"a JPEG's SOI alone":          {"image/jpeg", []byte{0xFF, 0xD8}},
		"a JPEG ending before a scan": {"image/jpeg", []byte{0xFF, 0xD8, 0xFF, 0xD9}},
		"a JPEG segment past the end": {"image/jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x10, 0x00, 0x01}},
		"a JPEG length below two":     {"image/jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x01, 0xFF, 0xDA}},
		"a JPEG with junk for a mark": {"image/jpeg", []byte{0xFF, 0xD8, 0x12, 0x34}},
		"a PNG signature alone":       {"image/png", pngSignature},
		"a PNG without IEND":          {"image/png", pngData[:len(pngData)-12]},
		"a PNG chunk past the end":    {"image/png", append(append([]byte{}, pngSignature...), 0x7F, 0xFF, 0xFF, 0xFF, 'I', 'H', 'D', 'R')},
		"another type":                {"image/gif", []byte("GIF89a")},
	} {
		if got, err := Strip(c.contentType, c.data); err == nil {
			t.Errorf("%s: strip = %d bytes, want refused", name, len(got))
		}
	}
	for contentType, data := range map[string][]byte{"image/jpeg": jpg, "image/png": pngData} {
		for n := range len(data) {
			cut := data[:n]
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("%s cut at %d bytes panicked: %v", contentType, n, r)
					}
				}()
				_, _ = Strip(contentType, cut)
			}()
		}
	}
}
