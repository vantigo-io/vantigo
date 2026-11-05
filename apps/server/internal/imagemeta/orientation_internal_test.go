package imagemeta

import (
	"encoding/binary"
	"errors"
	"testing"
)

// exifTIFF is a little- or big-endian TIFF structure whose first directory
// holds one Orientation entry of the given type, count and value.
func exifTIFF(order binary.AppendByteOrder, typ uint16, count uint32, value uint16) []byte {
	b := []byte{'I', 'I'}
	if order == binary.BigEndian {
		b = []byte{'M', 'M'}
	}
	b = order.AppendUint16(b, 42)
	b = order.AppendUint32(b, 8)
	b = order.AppendUint16(b, 1)
	b = order.AppendUint16(b, 0x0112)
	b = order.AppendUint16(b, typ)
	b = order.AppendUint32(b, count)
	b = order.AppendUint16(b, value)
	b = append(b, 0, 0)
	return order.AppendUint32(b, 0)
}

func jpegWithExif(t *testing.T, tiff []byte) []byte {
	t.Helper()
	data := stripJPEGOf(t, 16, 16)
	app1 := jpegSegment(0xE1, append([]byte("Exif\x00\x00"), tiff...))
	return insertAt(data, 2, app1)
}

func TestOrientation_JPEG(t *testing.T) {
	t.Parallel()
	for _, order := range []binary.AppendByteOrder{binary.LittleEndian, binary.BigEndian} {
		for want := 1; want <= 8; want++ {
			got, err := Orientation(jpegWithExif(t, exifTIFF(order, 3, 1, uint16(want))))
			if err != nil || got != want {
				t.Errorf("%v orientation %d: got %d, %v", order, want, got, err)
			}
		}
	}
}

func TestOrientation_PNGExif(t *testing.T) {
	t.Parallel()
	data := stripPNGOf(t, 16, 16)
	chunk := pngChunk("eXIf", exifTIFF(binary.BigEndian, 3, 1, 6))
	got, err := Orientation(insertAt(data, len(pngSignature)+25, chunk))
	if err != nil || got != 6 {
		t.Errorf("PNG eXIf orientation: got %d, %v, want 6", got, err)
	}
}

func TestOrientation_NoneIsOne(t *testing.T) {
	t.Parallel()
	for name, data := range map[string][]byte{
		"a JPEG with no Exif":     stripJPEGOf(t, 16, 16),
		"a PNG with no eXIf":      stripPNGOf(t, 16, 16),
		"a value of 0":            jpegWithExif(t, exifTIFF(binary.LittleEndian, 3, 1, 0)),
		"a value of 9":            jpegWithExif(t, exifTIFF(binary.LittleEndian, 3, 1, 9)),
		"a wrong type":            jpegWithExif(t, exifTIFF(binary.LittleEndian, 4, 1, 6)),
		"a wrong count":           jpegWithExif(t, exifTIFF(binary.LittleEndian, 3, 2, 6)),
		"a cut-short TIFF":        jpegWithExif(t, exifTIFF(binary.LittleEndian, 3, 1, 6)[:10]),
		"a TIFF of no byte order": jpegWithExif(t, []byte("XX*\x00\x08\x00\x00\x00")),
	} {
		got, err := Orientation(data)
		if err != nil || got != 1 {
			t.Errorf("%s: got %d, %v, want 1, nil", name, got, err)
		}
	}
}

func TestOrientation_BrokenStructure(t *testing.T) {
	t.Parallel()
	for name, data := range map[string][]byte{
		"a JPEG cut short":    stripJPEGOf(t, 16, 16)[:5],
		"a PNG cut short":     stripPNGOf(t, 16, 16)[:20],
		"neither JPEG or PNG": []byte("GIF89a"),
		"nothing":             nil,
	} {
		if _, err := Orientation(data); !errors.Is(err, ErrStructure) {
			t.Errorf("%s: err = %v, want ErrStructure", name, err)
		}
	}
}
