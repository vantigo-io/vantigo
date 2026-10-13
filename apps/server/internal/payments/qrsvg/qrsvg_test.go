package qrsvg_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/boombuler/barcode/qr"

	"github.com/vantigo-io/vantigo/server/internal/payments/qrsvg"
)

// The image is the code's modules exactly — one 1×1 square per dark module,
// offset by the quiet zone — and nothing else: three elements, a fixed set of
// attributes, no script, no event handler, no reference, and none of the
// encoded text, however hostile.
func TestRender_TheModulesAndNothingElse(t *testing.T) {
	t.Parallel()
	const text = `https://qr.example.no/p/abc?x="><script>alert(1)</script><img onerror=alert(1)>`
	svg, err := qrsvg.Render(text)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	again, _ := qrsvg.Render(text)
	if again != svg {
		t.Error("Render is not deterministic")
	}

	tags := regexp.MustCompile(`<(/?)([a-zA-Z][^\s/>]*)`).FindAllStringSubmatch(svg, -1)
	for _, tag := range tags {
		if tag[2] != "svg" && tag[2] != "rect" && tag[2] != "path" {
			t.Errorf("element %q in the image", tag[2])
		}
	}
	allowed := map[string]bool{"xmlns": true, "viewBox": true, "shape-rendering": true, "width": true, "height": true, "fill": true, "d": true}
	for _, attr := range regexp.MustCompile(`\s([a-zA-Z:-]+)=`).FindAllStringSubmatch(svg, -1) {
		if !allowed[attr[1]] {
			t.Errorf("attribute %q in the image", attr[1])
		}
	}
	for _, banned := range []string{"script", "onerror", "alert", "href", "qr.example.no", "<img"} {
		if strings.Contains(strings.ToLower(svg), banned) {
			t.Errorf("the image contains %q", banned)
		}
	}

	code, err := qr.Encode(text, qr.M, qr.Auto)
	if err != nil {
		t.Fatal(err)
	}
	b := code.Bounds()
	want := map[string]bool{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if r, g, bl, _ := code.At(x, y).RGBA(); r+g+bl < 3*0x8000 {
				want[strconv.Itoa(x-b.Min.X+4)+" "+strconv.Itoa(y-b.Min.Y+4)] = true
			}
		}
	}
	got := map[string]bool{}
	for _, m := range regexp.MustCompile(`M(\d+) (\d+)h1v1h-1z`).FindAllStringSubmatch(svg, -1) {
		got[m[1]+" "+m[2]] = true
	}
	if len(got) == 0 || len(got) != len(want) {
		t.Fatalf("%d dark squares, want the code's %d dark modules", len(got), len(want))
	}
	for k := range want {
		if !got[k] {
			t.Errorf("module %s is missing", k)
		}
	}
	size := strconv.Itoa(b.Dx() + 8)
	if !strings.Contains(svg, `viewBox="0 0 `+size+` `+size+`"`) {
		t.Errorf("the view box is not the code plus a four-module quiet zone: %.120s", svg)
	}
}

func TestRender_RefusesNothing(t *testing.T) {
	t.Parallel()
	if _, err := qrsvg.Render(""); err == nil {
		t.Error("an empty text rendered")
	}
}
