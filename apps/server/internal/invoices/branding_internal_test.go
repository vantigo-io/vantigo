package invoices

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// This file tests the branding of invoices phase 6 delivery A in-process: the
// logo's rules (I1), its place in the PDF (I3), the phone in the seller block
// (I4) and the HTML mail (I5, I6).

// testLogo is testdata/logo.png, a 120 × 40 PNG: a fixed file, so the pinned
// render below does not move with Go's PNG encoder.
func testLogo(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "logo.png"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

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

// A logo is a PNG or a JPEG by its own bytes, at most 2,000 pixels on its
// longer side and at least 16 on its shorter (I1); anything else is refused
// with its code.
func TestJudgeLogo_TheRules(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		data     []byte
		wantType string
		wantCode string
	}{
		{"a PNG", encoded(t, 300, 100, pngOf), contentTypePNG, ""},
		{"a JPEG", encoded(t, 300, 100, jpegOf), contentTypeJPEG, ""},
		{"2,000 wide", encoded(t, 2000, 16, pngOf), contentTypePNG, ""},
		{"a GIF", encoded(t, 300, 100, gifOf), "", codeLogoUnsupportedType},
		{"not an image", []byte("%PDF-1.4 not a logo"), "", codeLogoUnsupportedType},
		{"a PNG's signature and nothing else", []byte("\x89PNG\r\n\x1a\n"), "", codeLogoUnsupportedType},
		{"2,001 wide", encoded(t, 2001, 100, pngOf), "", codeLogoTooWide},
		{"2,001 high", encoded(t, 100, 2001, jpegOf), "", codeLogoTooWide},
		{"15 high", encoded(t, 300, 15, pngOf), "", codeLogoTooSmall},
	} {
		got, refusal := judgeLogo(c.data)
		code := ""
		if refusal != nil {
			code = *refusal.Code
			if *refusal.Status != 400 {
				t.Errorf("%s: status %d, want 400", c.name, *refusal.Status)
			}
		}
		if got != c.wantType || code != c.wantCode {
			t.Errorf("%s = %q %q, want %q %q", c.name, got, code, c.wantType, c.wantCode)
		}
	}
}

// The key is named by the content and the type.
func TestLogoKey_IsContentAddressed(t *testing.T) {
	t.Parallel()
	if got := logoKey("ab12", contentTypeJPEG); got != "branding/logo-ab12.jpg" {
		t.Errorf("jpeg key = %q", got)
	}
	if got := logoKey("ab12", contentTypePNG); got != "branding/logo-ab12.png" {
		t.Errorf("png key = %q", got)
	}
}

// The phone prints in the seller block after the e-mail (I4), and a seller
// without one prints the block as before.
func TestPDFModel_ThePhoneFollowsTheEmail(t *testing.T) {
	t.Parallel()
	d := anInvoice()
	d.seller.phone = "+47 22 33 44 55"
	m := buildPDFModel(d)
	want := []string{"Kraft-Verket AS", "Storgata 1", "0155 Oslo", "Org.nr. 974 760 673 MVA", "Foretaksregisteret", "faktura@kraft-verket.no", "+47 22 33 44 55"}
	if !slices.Equal(m.seller, want) {
		t.Errorf("seller = %q, want %q", m.seller, want)
	}
	if without := buildPDFModel(anInvoice()); len(without.seller) != len(want)-1 {
		t.Errorf("without a phone the seller = %q", without.seller)
	}
}

// A document with a logo renders reproducibly, to other bytes than the same
// document without one — whose render the pin in TestRenderPDF_IsReproducible
// holds unchanged — and to one fixed render, pinned as that one is.
func TestRenderPDF_WithALogo(t *testing.T) {
	t.Parallel()
	d := anInvoice()
	d.logo, d.logoType = testLogo(t), contentTypePNG
	first, err := renderPDF(buildPDFModel(d))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	second, err := renderPDF(buildPDFModel(d))
	if err != nil {
		t.Fatalf("render again: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two renders of one document with a logo differ")
	}
	without, err := renderPDF(buildPDFModel(anInvoice()))
	if err != nil {
		t.Fatalf("render without: %v", err)
	}
	if bytes.Equal(first, without) {
		t.Fatal("the logo changed nothing in the render")
	}
	if !bytes.Contains(first, []byte("/Subtype /Image")) {
		t.Error("the render has no image object")
	}
	sum := sha256.Sum256(first)
	if got := hex.EncodeToString(sum[:]); got != pinnedLogoRenderSHA256 {
		t.Errorf("the fixed render with a logo's SHA-256 = %s, want %s", got, pinnedLogoRenderSHA256)
	}
}

const pinnedLogoRenderSHA256 = "5ddd1d17cdfda9c7f9a53c5319569b3a90adb8ff21c89033b4c0b0f33a97520d"

// A JPEG logo renders too.
func TestRenderPDF_WithAJPEGLogo(t *testing.T) {
	t.Parallel()
	d := anInvoice()
	d.logo, d.logoType = encoded(t, 200, 80, jpegOf), contentTypeJPEG
	if _, err := renderPDF(buildPDFModel(d)); err != nil {
		t.Fatalf("render: %v", err)
	}
}

// The cover mail with a personal message (I6): after the greeting and above
// the standard text in the text body, the rest of it word for word as
// without; in the HTML body escaped, its line breaks kept as <br>. The HTML
// says the same paragraphs, carries the logo only when the mail does, and
// links nothing but the pay link.
func TestCoverMail_TheMessageAndTheHTML(t *testing.T) {
	t.Parallel()
	inv := mailDocument(t, kindInvoice, "nb", 1001, "15045", "", "")
	message := "Takk for sist!\n<script>alert(1)</script> & co"
	without, err := coverMail(inv, nil, mustRat("15045"), "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	with, err := coverMail(inv, nil, mustRat("15045"), "https://pay.example/l/abc", message, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(with.body, "Hei,\n\n"+message+"\n\nVedlagt følger faktura 1001") {
		t.Errorf("text body = %q, want the message after the greeting", with.body)
	}
	if strings.Contains(without.body, "Takk for sist") || !strings.HasPrefix(without.body, "Hei,\n\nVedlagt følger") {
		t.Errorf("without a message the text body = %q", without.body)
	}
	html := with.html
	for _, want := range []string{
		`<html lang="nb">`,
		`<img src="cid:logo" alt="Kraft-Verket AS" style="max-height:60px">`,
		`Takk for sist!<br>&lt;script&gt;alert(1)&lt;/script&gt; &amp; co`,
		`<p style="margin:0 0 16px">Vedlagt følger faktura 1001 fra Kraft-Verket AS på NOK ` + money(mustRat("15045"), "nb") + `, med forfall 12.10.2026.</p>`,
		`Du kan også betale med Vipps: <a href="https://pay.example/l/abc"`,
		`Med vennlig hilsen<br>Kraft-Verket AS`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<script>") {
		t.Errorf("the message is not escaped:\n%s", html)
	}
	if n := strings.Count(html, "http"); n != 2 {
		t.Errorf("HTML names %d URLs, want the pay link alone (its href and its text):\n%s", n, html)
	}
	if strings.Contains(without.html, "cid:logo") || strings.Contains(without.html, "<a ") {
		t.Errorf("without a logo or a pay link the HTML = %s", without.html)
	}
}
