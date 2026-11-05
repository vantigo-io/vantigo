package products_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/imagemeta"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/products"
	"github.com/vantigo-io/vantigo/server/internal/storage"
)

// This file is delivery P of the Messenger design (N13, N15): a product's
// images — what upload refuses, the normalisation it does, the primary rules
// and the two catalog calls.

// secretCamera is the text the fixtures' EXIF carries as the camera's make,
// which must be nowhere in what is stored.
const secretCamera = "SECRETCAMERA"

type imageJSON struct {
	Id          int32    `json:"id"`
	ProductId   int32    `json:"productId"`
	AltText     *string  `json:"altText"`
	ContentType string   `json:"contentType"`
	ByteSize    int32    `json:"byteSize"`
	Width       int32    `json:"width"`
	Height      int32    `json:"height"`
	SortOrder   int32    `json:"sortOrder"`
	IsPrimary   bool     `json:"isPrimary"`
	Uses        []string `json:"uses"`
}

type imageProblemJSON struct {
	Code string `json:"code"`
}

// imagesHarness is one installation with a real file object store on a
// temporary root, returned beside the root so a test can count the files.
func imagesHarness(t *testing.T, opts ...modtest.Option) (*modtest.Harness, string) {
	t.Helper()
	root := t.TempDir()
	h := newHarness(t, append([]modtest.Option{
		modtest.WithEnv("STORAGE_PROVIDER", "fs"),
		modtest.WithEnv("STORAGE_FS_ROOT", root),
		// t.TempDir() honours the process umask, which the sandbox the tests
		// run under sets to 0002 — group-writable, which the fs driver
		// refuses outside a relaxed root. The harness always runs with
		// APP_ENV=development, so this is the escape hatch a development
		// deployment uses.
		modtest.WithEnv("STORAGE_FS_ALLOW_INSECURE_ROOT", "1"),
	}, opts...)...)
	return h, root
}

// quadrants is a w×h picture of four flat colours: red top left, green top
// right, blue bottom left, yellow bottom right.
func quadrants(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{255, 0, 0, 255}
			switch {
			case x >= w/2 && y < h/2:
				c = color.RGBA{0, 255, 0, 255}
			case x < w/2 && y >= h/2:
				c = color.RGBA{0, 0, 255, 255}
			case x >= w/2 && y >= h/2:
				c = color.RGBA{255, 255, 0, 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func jpegBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode JPEG: %v", err)
	}
	return buf.Bytes()
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buf.Bytes()
}

// withExif is a JPEG with an APP1 Exif segment after its SOI: the camera's
// make and, when orientation is not 0, the orientation.
func withExif(data []byte, orientation uint16) []byte {
	make := append([]byte(secretCamera), 0)
	entries := uint16(1)
	if orientation != 0 {
		entries = 2
	}
	ifdSize := 2 + int(entries)*12 + 4
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0}
	tiff = binary.LittleEndian.AppendUint16(tiff, entries)
	// Make (0x010F), ASCII, its text after the directory.
	tiff = binary.LittleEndian.AppendUint16(tiff, 0x010F)
	tiff = binary.LittleEndian.AppendUint16(tiff, 2)
	tiff = binary.LittleEndian.AppendUint32(tiff, uint32(len(make)))
	tiff = binary.LittleEndian.AppendUint32(tiff, uint32(8+ifdSize))
	if orientation != 0 {
		tiff = binary.LittleEndian.AppendUint16(tiff, 0x0112)
		tiff = binary.LittleEndian.AppendUint16(tiff, 3)
		tiff = binary.LittleEndian.AppendUint32(tiff, 1)
		tiff = binary.LittleEndian.AppendUint16(tiff, orientation)
		tiff = append(tiff, 0, 0)
	}
	tiff = binary.LittleEndian.AppendUint32(tiff, 0)
	tiff = append(tiff, make...)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	seg = append(seg, payload...)
	out := append([]byte{}, data[:2]...)
	out = append(out, seg...)
	return append(out, data[2:]...)
}

// withPNGText is a PNG with a tEXt chunk after its IHDR.
func withPNGText(data []byte, text string) []byte {
	payload := []byte("Comment\x00" + text)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(payload)))
	chunk = append(chunk, "tEXt"...)
	chunk = append(chunk, payload...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(append([]byte("tEXt"), payload...)))
	const afterIHDR = 8 + 25
	out := append([]byte{}, data[:afterIHDR]...)
	out = append(out, chunk...)
	return append(out, data[afterIHDR:]...)
}

// pngHeaderOnly is a PNG signature and IHDR claiming w×h, enough for
// image.DecodeConfig and nothing a decoder would accept.
func pngHeaderOnly(w, h uint32) []byte {
	ihdr := binary.BigEndian.AppendUint32(nil, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, 8, 6, 0, 0, 0)
	out := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	out = binary.BigEndian.AppendUint32(out, uint32(len(ihdr)))
	out = append(out, "IHDR"...)
	out = append(out, ihdr...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
}

// multipartFiles is a form of files, each in the field file, as the Do's
// RawBody option sends it.
func multipartFiles(t *testing.T, files ...[]byte) modtest.RequestOption {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, data := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="file"; filename="photo.bin"`)
		h.Set("Content-Type", "application/octet-stream")
		part, err := w.CreatePart(h)
		if err != nil {
			t.Fatalf("create part: %v", err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the form: %v", err)
	}
	return modtest.RawBody(w.FormDataContentType(), buf.Bytes())
}

func imagesPath(productID int32) string { return fmt.Sprintf("/api/v1/products/%d/images", productID) }

func imagePath(productID, imageID int32) string {
	return fmt.Sprintf("/api/v1/products/%d/images/%d", productID, imageID)
}

// newProduct is a product to hang images on.
func newProduct(t *testing.T, h *modtest.Harness, c *modtest.Client) int32 {
	t.Helper()
	name := sku(t, "imaged")
	tax := insertTaxCategory(t, h, "Rate "+name, 0.25)
	return createProduct(t, c, newProductBody(tax, name, name)).Id
}

// upload posts data and requires 201.
func upload(t *testing.T, c *modtest.Client, productID int32, data []byte) imageJSON {
	t.Helper()
	r := c.Do(http.MethodPost, imagesPath(productID), nil, multipartFiles(t, data))
	if r.Status != http.StatusCreated {
		t.Fatalf("upload: status %d body %s, want 201", r.Status, r.Body)
	}
	var img imageJSON
	r.JSON(&img)
	return img
}

func listImages(t *testing.T, c *modtest.Client, productID int32) []imageJSON {
	t.Helper()
	r := c.Do(http.MethodGet, imagesPath(productID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list images: status %d body %s", r.Status, r.Body)
	}
	var out []imageJSON
	r.JSON(&out)
	return out
}

func patchImage(c *modtest.Client, productID, imageID int32, body map[string]any) *modtest.Response {
	return c.Do(http.MethodPatch, imagePath(productID, imageID), body)
}

// fetchImage is the stored picture (or its thumbnail), decoded.
func fetchImage(t *testing.T, c *modtest.Client, productID, imageID int32, thumb bool) (image.Image, []byte) {
	t.Helper()
	path := imagePath(productID, imageID) + "/content"
	if thumb {
		path += "?size=thumb"
	}
	r := c.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("content: status %d body %q", r.Status, r.Body)
	}
	img, _, err := image.Decode(bytes.NewReader(r.Body))
	if err != nil {
		t.Fatalf("decode the stored image: %v", err)
	}
	return img, r.Body
}

// near reports whether the pixel at (x, y) of img is close to want, JPEG's
// loss allowed for.
func near(img image.Image, x, y int, want color.RGBA) bool {
	r, g, b, _ := img.At(x, y).RGBA()
	d := func(a uint32, w uint8) bool {
		v := int(a >> 8)
		return v-int(w) < 40 && int(w)-v < 40
	}
	return d(r, want.R) && d(g, want.G) && d(b, want.B)
}

var (
	red    = color.RGBA{255, 0, 0, 255}
	green  = color.RGBA{0, 255, 0, 255}
	blue   = color.RGBA{0, 0, 255, 255}
	yellow = color.RGBA{255, 255, 0, 255}
)

// corners are the colours at the middle of each quarter of img: top left,
// top right, bottom left, bottom right.
func corners(img image.Image) [4]func(color.RGBA) bool {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	at := func(x, y int) func(color.RGBA) bool {
		return func(c color.RGBA) bool { return near(img, x, y, c) }
	}
	return [4]func(color.RGBA) bool{at(w/4, h/4), at(3*w/4, h/4), at(w/4, 3*h/4), at(3*w/4, 3*h/4)}
}

// TestImages_OrientationAppliedAndMetadataGone uploads a phone's photo, held
// turned (EXIF orientation 6 and 8) and carrying the camera's make: the
// stored picture is upright and none of the metadata is in it or its
// thumbnail.
func TestImages_OrientationAppliedAndMetadataGone(t *testing.T) {
	t.Parallel()
	// The source is 300×200 landscape. Turned a quarter clockwise (6) its top
	// left lands top right; turned a quarter counter-clockwise (8), bottom left.
	cases := []struct {
		orientation uint16
		want        [4]color.RGBA // top left, top right, bottom left, bottom right, as stored
	}{
		{6, [4]color.RGBA{blue, red, yellow, green}},
		{8, [4]color.RGBA{green, yellow, red, blue}},
		{3, [4]color.RGBA{yellow, blue, green, red}},
		{1, [4]color.RGBA{red, green, blue, yellow}},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("orientation %d", tc.orientation), func(t *testing.T) {
			t.Parallel()
			h, _ := imagesHarness(t)
			c := authenticatedClient(t, h)
			productID := newProduct(t, h, c)
			raw := withExif(jpegBytes(t, quadrants(300, 200)), tc.orientation)
			if got, err := imagemeta.Orientation(raw); err != nil || got != int(tc.orientation) {
				t.Fatalf("the fixture's orientation = %d, %v, want %d", got, err, tc.orientation)
			}
			img := upload(t, c, productID, raw)

			wantW, wantH := int32(300), int32(200)
			if tc.orientation == 6 || tc.orientation == 8 {
				wantW, wantH = 200, 300
			}
			if img.Width != wantW || img.Height != wantH || img.ContentType != "image/jpeg" {
				t.Errorf("image = %dx%d %s, want %dx%d image/jpeg", img.Width, img.Height, img.ContentType, wantW, wantH)
			}
			for _, thumb := range []bool{false, true} {
				stored, body := fetchImage(t, c, productID, img.Id, thumb)
				if bytes.Contains(body, []byte(secretCamera)) || bytes.Contains(body, []byte("Exif")) {
					t.Errorf("thumb=%v: the stored file still carries its metadata", thumb)
				}
				if o, err := imagemeta.Orientation(body); err != nil || o != 1 {
					t.Errorf("thumb=%v: stored orientation = %d, %v, want 1", thumb, o, err)
				}
				for i, ok := range corners(stored) {
					if !ok(tc.want[i]) {
						t.Errorf("thumb=%v: quarter %d is not %v", thumb, i, tc.want[i])
					}
				}
			}
			if int(img.ByteSize) == 0 {
				t.Errorf("byteSize = 0")
			}
		})
	}
}

// TestImages_PNGMetadataGone proves a PNG's text chunks are gone too, and
// that a PNG stays a PNG, transparency and all.
func TestImages_PNGMetadataGone(t *testing.T) {
	t.Parallel()
	h, _ := imagesHarness(t)
	c := authenticatedClient(t, h)
	productID := newProduct(t, h, c)
	src := image.NewNRGBA(image.Rect(0, 0, 240, 240))
	for y := range 240 {
		for x := range 240 {
			src.Set(x, y, color.NRGBA{200, 10, 10, 128})
		}
	}
	img := upload(t, c, productID, withPNGText(pngBytes(t, src), secretCamera))
	if img.ContentType != "image/png" {
		t.Fatalf("contentType = %q, want image/png", img.ContentType)
	}
	stored, body := fetchImage(t, c, productID, img.Id, false)
	if bytes.Contains(body, []byte(secretCamera)) || bytes.Contains(body, []byte("tEXt")) {
		t.Errorf("the stored PNG still carries its text chunk")
	}
	if _, _, _, a := stored.At(100, 100).RGBA(); a>>8 < 120 || a>>8 > 136 {
		t.Errorf("alpha = %d, want about 128: transparency was lost", a>>8)
	}
}

// TestImages_ScaledAndThumbnail proves the longer side is at most 2048 and
// the thumbnail's at most 320, and that a picture is never scaled up.
func TestImages_ScaledAndThumbnail(t *testing.T) {
	t.Parallel()
	h, _ := imagesHarness(t)
	c := authenticatedClient(t, h)
	productID := newProduct(t, h, c)

	big := upload(t, c, productID, jpegBytes(t, quadrants(3000, 1000)))
	if big.Width != 2048 || big.Height != 682 {
		t.Errorf("big = %dx%d, want 2048x682", big.Width, big.Height)
	}
	thumb, _ := fetchImage(t, c, productID, big.Id, true)
	if b := thumb.Bounds(); b.Dx() != 320 || b.Dy() != 106 {
		t.Errorf("thumbnail = %dx%d, want 320x106", b.Dx(), b.Dy())
	}

	small := upload(t, c, productID, jpegBytes(t, quadrants(260, 300)))
	if small.Width != 260 || small.Height != 300 {
		t.Errorf("small = %dx%d, want 260x300 unscaled", small.Width, small.Height)
	}
	thumb, _ = fetchImage(t, c, productID, small.Id, true)
	if b := thumb.Bounds(); b.Dx() != 260 || b.Dy() != 300 {
		t.Errorf("small thumbnail = %dx%d, want 260x300 (320 at most, never up)", b.Dx(), b.Dy())
	}
}

// TestImages_Refusals proves what upload refuses, each with its code: 422 for
// the file, 400 for a form that is none.
func TestImages_Refusals(t *testing.T) {
	t.Parallel()
	h, root := imagesHarness(t)
	c := authenticatedClient(t, h)
	productID := newProduct(t, h, c)

	webp := append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 64)...)
	heic := append([]byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic"), make([]byte, 64)...)
	tooBig := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 10*1024*1024+10)...)
	cases := []struct {
		name string
		data []byte
		code string
	}{
		{"WebP", webp, "image_type_unsupported"},
		{"HEIC", heic, "image_type_unsupported"},
		{"text", []byte("not a picture at all"), "image_type_unsupported"},
		{"over 10 MiB", tooBig, "image_too_large"},
		{"over 40 megapixels", pngHeaderOnly(7000, 6000), "image_too_large"},
		{"narrower than 200", jpegBytes(t, quadrants(199, 400)), "image_too_small"},
		{"shorter than 200", jpegBytes(t, quadrants(400, 150)), "image_too_small"},
		{"a JPEG that is no picture", append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte("garbage")...), "image_invalid"},
		{"a PNG cut short", pngBytes(t, quadrants(300, 300))[:200], "image_invalid"},
	}
	for _, tc := range cases {
		r := c.Do(http.MethodPost, imagesPath(productID), nil, multipartFiles(t, tc.data))
		var p imageProblemJSON
		r.JSON(&p)
		if r.Status != http.StatusUnprocessableEntity || p.Code != tc.code {
			t.Errorf("%s: status %d code %q, want 422 %s", tc.name, r.Status, p.Code, tc.code)
		}
	}

	good := jpegBytes(t, quadrants(300, 300))
	for name, opt := range map[string]modtest.RequestOption{
		"no file":       modtest.RawBody("multipart/form-data; boundary=x", []byte("--x--\r\n")),
		"an empty file": multipartFiles(t, []byte{}),
		"two files":     multipartFiles(t, good, good),
		"no form":       modtest.RawBody("text/plain", []byte("hello")),
	} {
		if r := c.Do(http.MethodPost, imagesPath(productID), nil, opt); r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s, want 400", name, r.Status, r.Body)
		}
	}
	if n := countFiles(t, root); n != 0 {
		t.Errorf("%d object(s) in the store after every refusal, want 0", n)
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM products.product_images`); n != 0 {
		t.Errorf("%d image row(s) after every refusal, want 0", n)
	}
}

// countFiles is how many files lie under root.
func countFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return n
}

// TestImages_PrimaryRules proves the first image is primary, making another
// primary moves it, the primary cannot be unmarked, one is enforced by the
// schema, and deleting the primary promotes the next by order.
func TestImages_PrimaryRules(t *testing.T) {
	t.Parallel()
	h, root := imagesHarness(t)
	c := authenticatedClient(t, h)
	productID := newProduct(t, h, c)
	pic := jpegBytes(t, quadrants(300, 300))

	a, b, d := upload(t, c, productID, pic), upload(t, c, productID, pic), upload(t, c, productID, pic)
	if !a.IsPrimary || b.IsPrimary || d.IsPrimary {
		t.Fatalf("primary = %v %v %v, want the first only", a.IsPrimary, b.IsPrimary, d.IsPrimary)
	}
	if a.SortOrder != 0 || b.SortOrder != 1 || d.SortOrder != 2 {
		t.Errorf("sort orders = %d %d %d, want 0 1 2", a.SortOrder, b.SortOrder, d.SortOrder)
	}
	if countFiles(t, root) != 6 {
		t.Errorf("%d objects for three images, want 6 (each with its thumbnail)", countFiles(t, root))
	}

	// Another image primary: the first stops being it.
	if r := patchImage(c, productID, d.Id, map[string]any{"isPrimary": true}); r.Status != http.StatusOK {
		t.Fatalf("make primary: %d %s", r.Status, r.Body)
	}
	got := listImages(t, c, productID)
	if got[0].IsPrimary || got[1].IsPrimary || !got[2].IsPrimary {
		t.Errorf("primary after the change = %v %v %v, want the third", got[0].IsPrimary, got[1].IsPrimary, got[2].IsPrimary)
	}
	// The primary stays primary until another is made.
	if r := patchImage(c, productID, d.Id, map[string]any{"isPrimary": false}); r.Status != http.StatusBadRequest {
		t.Errorf("unmark the primary: %d %s, want 400", r.Status, r.Body)
	}
	// Unmarking a non-primary is no change.
	if r := patchImage(c, productID, a.Id, map[string]any{"isPrimary": false}); r.Status != http.StatusOK {
		t.Errorf("unmark a non-primary: %d %s, want 200", r.Status, r.Body)
	}
	// The schema alone allows one.
	var conflict error
	func() {
		defer func() { _ = recover() }()
		_, conflict = h.Pool().Exec(context.Background(), `UPDATE products.product_images SET is_primary = true WHERE id = $1`, a.Id)
	}()
	if conflict == nil {
		t.Errorf("a second primary image of one product was accepted by the schema")
	}

	// Deleting the primary promotes the next by sort_order, and closes the gap.
	if r := c.Do(http.MethodDelete, imagePath(productID, d.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	got = listImages(t, c, productID)
	if len(got) != 2 || !got[0].IsPrimary || got[1].IsPrimary || got[0].Id != a.Id {
		t.Errorf("after deleting the primary: %+v, want the first of the two primary", got)
	}
	if got[0].SortOrder != 0 || got[1].SortOrder != 1 {
		t.Errorf("sort orders after the delete = %d %d, want 0 1", got[0].SortOrder, got[1].SortOrder)
	}
	// Deleting the first (now primary) promotes the last remaining one.
	if r := c.Do(http.MethodDelete, imagePath(productID, a.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if got = listImages(t, c, productID); len(got) != 1 || !got[0].IsPrimary || got[0].SortOrder != 0 {
		t.Errorf("the last image = %+v, want primary at 0", got)
	}
	// Both of a deleted image's objects are gone from the store.
	if n := countFiles(t, root); n != 2 {
		t.Errorf("%d objects for one image, want 2", n)
	}
	if r := c.Do(http.MethodGet, imagePath(productID, d.Id)+"/content", nil); r.Status != http.StatusNotFound {
		t.Errorf("a deleted image's content: %d, want 404", r.Status)
	}
	if r := c.Do(http.MethodDelete, imagePath(productID, d.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("deleting twice: %d, want 404", r.Status)
	}
}

// TestImages_Update proves the alt text, the order and the uses change, and
// what each refuses.
func TestImages_Update(t *testing.T) {
	t.Parallel()
	h, _ := imagesHarness(t)
	c := authenticatedClient(t, h)
	productID := newProduct(t, h, c)
	pic := jpegBytes(t, quadrants(300, 300))
	a, b, d := upload(t, c, productID, pic), upload(t, c, productID, pic), upload(t, c, productID, pic)

	if len(a.Uses) != 1 || a.Uses[0] != "communications" {
		t.Errorf("a new image's uses = %v, want [communications]", a.Uses)
	}
	// Alt text and uses.
	r := patchImage(c, productID, b.Id, map[string]any{"altText": "  A blue box  ", "uses": []string{"marketing", "communications", "marketing"}})
	var got imageJSON
	r.JSON(&got)
	if r.Status != http.StatusOK || got.AltText == nil || *got.AltText != "A blue box" || len(got.Uses) != 2 {
		t.Errorf("update = %d %s, want 200 with the trimmed alt text and two uses", r.Status, r.Body)
	}
	r = patchImage(c, productID, b.Id, map[string]any{"altText": "", "uses": []string{}})
	got = imageJSON{}
	r.JSON(&got)
	if r.Status != http.StatusOK || got.AltText != nil || len(got.Uses) != 0 {
		t.Errorf("clear = %d %s, want 200 with no alt text and no uses", r.Status, r.Body)
	}
	// A change that sends nothing changes nothing.
	if r = patchImage(c, productID, b.Id, map[string]any{}); r.Status != http.StatusOK {
		t.Errorf("empty update = %d, want 200", r.Status)
	}
	for name, body := range map[string]map[string]any{
		"251 characters":  {"altText": string(bytes.Repeat([]byte("å"), 251))},
		"an unknown use":  {"uses": []string{"sms"}},
		"a negative sort": {"sortOrder": -1},
	} {
		if r := patchImage(c, productID, b.Id, body); r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, r.Status)
		}
	}
	if r := patchImage(c, productID, b.Id, map[string]any{"altText": string(bytes.Repeat([]byte("å"), 250))}); r.Status != http.StatusOK {
		t.Errorf("250 characters: status %d, want 200", r.Status)
	}
	if r := patchImage(c, productID, 99999, map[string]any{"altText": "x"}); r.Status != http.StatusNotFound {
		t.Errorf("an unknown image: %d, want 404", r.Status)
	}

	// Moving: to the front, to the end, past the end.
	order := func() []int32 {
		var ids []int32
		for i, img := range listImages(t, c, productID) {
			if img.SortOrder != int32(i) {
				t.Errorf("image %d has sortOrder %d, want %d", img.Id, img.SortOrder, i)
			}
			ids = append(ids, img.Id)
		}
		return ids
	}
	want := func(step string, ids ...int32) {
		t.Helper()
		got := order()
		if fmt.Sprint(got) != fmt.Sprint(ids) {
			t.Errorf("%s: order = %v, want %v", step, got, ids)
		}
	}
	want("uploaded", a.Id, b.Id, d.Id)
	patchImage(c, productID, d.Id, map[string]any{"sortOrder": 0})
	want("last to first", d.Id, a.Id, b.Id)
	patchImage(c, productID, d.Id, map[string]any{"sortOrder": 1})
	want("first to second", a.Id, d.Id, b.Id)
	patchImage(c, productID, a.Id, map[string]any{"sortOrder": 99})
	want("first past the end", d.Id, b.Id, a.Id)
}

// TestImages_ReadsAndPermissions proves who may do what, and the 404s.
func TestImages_ReadsAndPermissions(t *testing.T) {
	t.Parallel()
	h, _ := imagesHarness(t)
	c := authenticatedClient(t, h)
	productID := newProduct(t, h, c)
	img := upload(t, c, productID, jpegBytes(t, quadrants(300, 300)))

	viewer := h.SignIn(t, "products:products-view")
	if got := listImages(t, viewer, productID); len(got) != 1 {
		t.Errorf("a viewer lists %d images, want 1", len(got))
	}
	r := viewer.Do(http.MethodGet, imagePath(productID, img.Id)+"/content", nil)
	if r.Status != http.StatusOK || r.Header("Cache-Control") != "private, max-age=300" || r.Header("X-Content-Type-Options") != "nosniff" || r.Header("Content-Type") != "image/jpeg" {
		t.Errorf("a viewer's content = %d %s %s %s", r.Status, r.Header("Cache-Control"), r.Header("X-Content-Type-Options"), r.Header("Content-Type"))
	}
	if r := viewer.Do(http.MethodPost, imagesPath(productID), nil, multipartFiles(t, jpegBytes(t, quadrants(300, 300)))); r.Status != http.StatusForbidden {
		t.Errorf("a viewer uploads: %d, want 403", r.Status)
	}
	if r := viewer.Do(http.MethodPatch, imagePath(productID, img.Id), map[string]any{"altText": "x"}); r.Status != http.StatusForbidden {
		t.Errorf("a viewer changes: %d, want 403", r.Status)
	}
	if r := viewer.Do(http.MethodDelete, imagePath(productID, img.Id), nil); r.Status != http.StatusForbidden {
		t.Errorf("a viewer deletes: %d, want 403", r.Status)
	}
	nobody := h.SignIn(t)
	if r := nobody.Do(http.MethodGet, imagesPath(productID), nil); r.Status != http.StatusForbidden {
		t.Errorf("no permission lists: %d, want 403", r.Status)
	}

	if r := c.Do(http.MethodGet, imagesPath(99999), nil); r.Status != http.StatusNotFound {
		t.Errorf("an unknown product's list: %d, want 404", r.Status)
	}
	if r := c.Do(http.MethodPost, imagesPath(99999), nil, multipartFiles(t, jpegBytes(t, quadrants(300, 300)))); r.Status != http.StatusNotFound {
		t.Errorf("an unknown product's upload: %d, want 404", r.Status)
	}
	other := newProduct(t, h, c)
	if r := c.Do(http.MethodGet, imagePath(other, img.Id)+"/content", nil); r.Status != http.StatusNotFound {
		t.Errorf("another product's image: %d, want 404", r.Status)
	}
	if r := c.Do(http.MethodGet, imagePath(productID, img.Id)+"/content?size=huge", nil); r.Status != http.StatusBadRequest {
		t.Errorf("size=huge: %d, want 400", r.Status)
	}
	if got := listImages(t, c, other); len(got) != 0 {
		t.Errorf("a product with none lists %d", len(got))
	}
}

// TestImages_UsesCheck proves the schema allows only the two uses.
func TestImages_UsesCheck(t *testing.T) {
	t.Parallel()
	h, _ := imagesHarness(t)
	c := authenticatedClient(t, h)
	productID := newProduct(t, h, c)
	img := upload(t, c, productID, jpegBytes(t, quadrants(300, 300)))
	for _, uses := range []string{`{sms}`, `{communications,sms}`} {
		_, err := h.Pool().Exec(context.Background(), `UPDATE products.product_images SET uses = $2::text[] WHERE id = $1`, img.Id, uses)
		if err == nil {
			t.Errorf("uses %s was accepted by the schema", uses)
		}
	}
	if _, err := h.Pool().Exec(context.Background(), `UPDATE products.product_images SET uses = '{}' WHERE id = $1`, img.Id); err != nil {
		t.Errorf("no uses was refused: %v", err)
	}
}

// failingStore is an object store whose writes fail, or whose reads do.
type failingStore struct {
	storage.ObjectStore
	failPut, failGet bool
}

func (s *failingStore) Put(ctx context.Context, key string, r io.Reader, contentType string) error {
	if s.failPut {
		return errors.New("the store is down")
	}
	return s.ObjectStore.Put(ctx, key, r, contentType)
}

func (s *failingStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if s.failGet {
		return nil, errors.New("the store is down")
	}
	return s.ObjectStore.Get(ctx, key)
}

// memoryStore is an object store in a map.
type memoryStore map[string][]byte

func (m memoryStore) Put(_ context.Context, key string, r io.Reader, _ string) error {
	b, err := io.ReadAll(r)
	m[key] = b
	return err
}

func (m memoryStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := m[key]
	if !ok {
		return nil, storage.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m memoryStore) Exists(_ context.Context, key string) (bool, error) {
	_, ok := m[key]
	return ok, nil
}
func (m memoryStore) Delete(_ context.Context, key string) error { delete(m, key); return nil }

// TestImages_StoreFailures proves a store that cannot write is 503 with no
// row recorded, one that cannot read is 503, and an object that is gone is 404.
func TestImages_StoreFailures(t *testing.T) {
	t.Parallel()
	mem := memoryStore{}
	store := &failingStore{ObjectStore: mem}
	h := newHarness(t, modtest.WithObjectStore(store))
	c := authenticatedClient(t, h)
	productID := newProduct(t, h, c)
	pic := jpegBytes(t, quadrants(300, 300))

	store.failPut = true
	r := c.Do(http.MethodPost, imagesPath(productID), nil, multipartFiles(t, pic))
	var p imageProblemJSON
	r.JSON(&p)
	if r.Status != http.StatusServiceUnavailable || p.Code != "storage_unavailable" {
		t.Errorf("upload with a failing store: %d %s, want 503 storage_unavailable", r.Status, r.Body)
	}
	if n := modtest.One[int](t, h, `SELECT count(*) FROM products.product_images`); n != 0 {
		t.Errorf("%d row(s) recorded for a failed upload, want 0", n)
	}
	store.failPut = false

	img := upload(t, c, productID, pic)
	store.failGet = true
	if r := c.Do(http.MethodGet, imagePath(productID, img.Id)+"/content", nil); r.Status != http.StatusServiceUnavailable {
		t.Errorf("content with a failing store: %d, want 503", r.Status)
	}
	store.failGet = false
	for k := range mem {
		delete(mem, k)
	}
	if r := c.Do(http.MethodGet, imagePath(productID, img.Id)+"/content", nil); r.Status != http.StatusNotFound {
		t.Errorf("content of an object that is gone: %d, want 404", r.Status)
	}
}

// TestImages_Catalog proves the catalog's two calls (N15): the images marked
// for a use in their order, the bytes of one, and nil for one that is gone.
func TestImages_Catalog(t *testing.T) {
	t.Parallel()
	h, _ := imagesHarness(t)
	c := authenticatedClient(t, h)
	productID := newProduct(t, h, c)
	catalog := products.Module().Products(h.Deps())
	ctx := context.Background()

	a := upload(t, c, productID, jpegBytes(t, quadrants(300, 200)))
	b := upload(t, c, productID, pngBytes(t, quadrants(240, 240)))
	d := upload(t, c, productID, jpegBytes(t, quadrants(300, 300)))
	patchImage(c, productID, a.Id, map[string]any{"altText": "Front", "uses": []string{"communications", "marketing"}})
	patchImage(c, productID, b.Id, map[string]any{"uses": []string{"marketing"}})
	patchImage(c, productID, d.Id, map[string]any{"sortOrder": 0})

	ids := func(use string) []int32 {
		got, err := catalog.ProductImages(ctx, productID, use)
		if err != nil {
			t.Fatalf("ProductImages(%q): %v", use, err)
		}
		var out []int32
		for _, img := range got {
			out = append(out, img.ID)
		}
		return out
	}
	if got := fmt.Sprint(ids("communications")); got != fmt.Sprint([]int32{d.Id, a.Id}) {
		t.Errorf("communications images = %s, want d then a, in order", got)
	}
	if got := fmt.Sprint(ids("marketing")); got != fmt.Sprint([]int32{a.Id, b.Id}) {
		t.Errorf("marketing images = %s, want a then b", got)
	}
	if got := ids("sms"); len(got) != 0 {
		t.Errorf("an unknown use answers %v, want none", got)
	}
	if got, err := catalog.ProductImages(ctx, 99999, "communications"); err != nil || len(got) != 0 {
		t.Errorf("an unknown product answers %v, %v, want none", got, err)
	}

	got, _ := catalog.ProductImages(ctx, productID, "marketing")
	first := got[0]
	if first.ID != a.Id || first.ProductID != productID || first.AltText != "Front" || first.ContentType != "image/jpeg" ||
		first.Width != 300 || first.Height != 200 || first.SortOrder != 1 || fmt.Sprint(first.Uses) != "[communications marketing]" {
		t.Errorf("ProductImage = %+v", first)
	}

	body, info, err := catalog.OpenProductImage(ctx, b.Id)
	if err != nil || body == nil {
		t.Fatalf("OpenProductImage: %v, %v", body, err)
	}
	defer func() { _ = body.Close() }()
	data, _ := io.ReadAll(body)
	if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || format != "png" || info.ID != b.Id || info.ContentType != "image/png" {
		t.Errorf("OpenProductImage = %s %v %+v, want the PNG", format, err, info)
	}

	for name, id := range map[string]int32{"an unknown image": 99999} {
		body, info, err := catalog.OpenProductImage(ctx, id)
		if body != nil || info.ID != 0 || err != nil {
			t.Errorf("%s: %v, %+v, %v, want nil, zero, nil", name, body, info, err)
		}
	}
	// An image whose object is gone is gone, not a failure.
	c.Do(http.MethodDelete, imagePath(productID, d.Id), nil)
	if body, info, err := catalog.OpenProductImage(ctx, d.Id); body != nil || info.ID != 0 || err != nil {
		t.Errorf("a deleted image: %v, %+v, %v, want nil, zero, nil", body, info, err)
	}
}
