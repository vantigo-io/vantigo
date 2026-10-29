package mail_test

import (
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/mail"
)

const inlineDownload = "/api/v1/communications/attachments/0f9e8d7c-6b5a-4321-9876-543210fedcba/download"

// knownCID resolves the one inline part the tests' messages carry.
func knownCID(id string) (string, bool) {
	if id == "logo@example.com" {
		return inlineDownload, true
	}
	return "", false
}

const pngData = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

// TestSanitizeHTML_XSS runs the XSS list: each input must come out with
// none of the forbidden fragments, in any case.
func TestSanitizeHTML_XSS(t *testing.T) {
	cases := []struct {
		name, in string
		forbid   []string
		keep     string
	}{
		{"script", `<p>hi</p><script>alert(1)</script>`, []string{"<script", "alert"}, "<p>hi</p>"},
		{"img onerror", `<img src="x" onerror="alert(1)">ok`, []string{"onerror", "<img", "alert"}, "ok"},
		{"javascript href", `<a href="javascript:alert(1)">x</a>`, []string{"javascript", "href"}, "x"},
		{"mixed-case javascript with tab and entities", `<a href="jAvA&#x09;ScRiPt&colon;alert(1)">x</a><a href="  JaVaScRiPt:alert(2)">y</a>`, []string{"script", "alert", "href"}, "y"},
		{"data text/html link", `<a href="data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==">x</a>`, []string{"data:", "href"}, "x"},
		{"data image link", `<a href="` + pngData + `">x</a>`, []string{"data:", "href"}, "x"},
		{"svg script", `<svg><script>alert(1)</script><circle onload="alert(2)"/></svg>ok`, []string{"<svg", "<script", "alert", "onload"}, "ok"},
		{"math", `<math><mi xlink:href="javascript:alert(1)">x</mi></math>ok`, []string{"<math", "<mi", "javascript"}, "ok"},
		{"iframe", `<iframe src="https://evil.example"></iframe>ok`, []string{"<iframe", "evil"}, "ok"},
		{"object", `<object data="https://evil.example/x.swf"></object>ok`, []string{"<object", "evil"}, "ok"},
		{"embed", `<embed src="https://evil.example/x.swf">ok`, []string{"<embed", "evil"}, "ok"},
		{"form and input", `<form action="https://evil.example"><input name="password" type="password"><button>Go</button></form>`, []string{"<form", "<input", "action", "evil"}, ""},
		{"meta refresh", `<meta http-equiv="refresh" content="0;url=https://evil.example">ok`, []string{"<meta", "refresh", "evil"}, "ok"},
		{"base href", `<base href="https://evil.example/"><a href="x">rel</a>`, []string{"<base", "evil", "href"}, "rel"},
		{"link stylesheet", `<link rel="stylesheet" href="https://evil.example/x.css">ok`, []string{"<link", "evil"}, "ok"},
		{"style element", `<style>body{background:url(https://evil.example/t.png)}</style>ok`, []string{"<style", "evil", "url("}, "ok"},
		{"style url", `<p style="background:url(https://evil.example/t.png);color:red">x</p>`, []string{"url(", "evil", "background:"}, "color: red"},
		{"style background-image", `<p style="background-image:url('https://evil.example/t.png')">x</p>`, []string{"url(", "evil", "background-image"}, "x"},
		{"style position fixed", `<div style="position:fixed;top:0;left:0;width:100%">x</div>`, []string{"position", "top:", "left:"}, "width: 100%"},
		{"style expression", `<p style="width:expression(alert(1));color:blue">x</p>`, []string{"expression", "alert"}, "color: blue"},
		{"srcset", `<img src="` + pngData + `" srcset="https://evil.example/a.png 2x">`, []string{"srcset", "evil"}, `src="data:image/png;base64,`},
		{"background attribute", `<table background="https://evil.example/t.png"><tr><td>x</td></tr></table>`, []string{"background", "evil"}, "<td>x</td>"},
		{"remote img", `<p>a<img src="https://tracker.example/pixel.gif">b</p>`, []string{"<img", "tracker"}, "<p>ab</p>"},
		{"one-pixel tracker", `<img src="http://tracker.example/o.gif?u=1" width="1" height="1" style="display:none">x`, []string{"<img", "tracker"}, "x"},
		{"protocol-relative img", `<img src="//tracker.example/p.gif">x`, []string{"<img", "tracker"}, "x"},
		{"relative img", `<img src="/api/v1/identity/me">x`, []string{"<img", "identity"}, "x"},
		{"cid rewritten", `<img src="cid:logo@example.com" alt="logo">`, []string{"cid:"}, `<img src="` + inlineDownload + `" alt="logo"/>`},
		{"the inline placeholder written by the message", `<img src="vantigo-inline:0f9e8d7c-6b5a-4321-9876-543210fedcba">x`, []string{"<img", "vantigo-inline", "/api/"}, "x"},
		{"unknown cid", `<img src="cid:other@example.com">x`, []string{"<img", "cid:"}, "x"},
		{"data png kept", `<img src="` + pngData + `">`, nil, `<img src="` + pngData + `"/>`},
		{"data svg", `<img src="data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=">x`, []string{"<img", "svg"}, "x"},
		{"conditional comments", `<!--[if mso]><script>alert(1)</script><![endif]--><p>x</p><!--[if !mso]><!--><p>y</p><!--<![endif]-->`, []string{"<!--", "[if", "alert"}, "<p>x</p>"},
		{"unclosed tags", `<p><b>bold<i>italic<p>next`, []string{"<script"}, "<b>bold<i>italic</i></b>"},
		{"mixed-case tags", `<ScRiPt>alert(1)</sCrIpT><IMG SRC="https://x.example/a.png" ONERROR="alert(2)"><P>ok</P>`, []string{"script", "alert", "<img", "onerror"}, "<p>ok</p>"},
		{"on attributes", `<p onclick="alert(1)" onmouseover="alert(2)">x</p>`, []string{"onclick", "onmouseover", "alert"}, "<p>x</p>"},
		{"head contents", `<html><head><title>secret title</title><meta charset="utf-8"></head><body>x</body></html>`, []string{"title", "<meta", "<head"}, "x"},
		{"vbscript", `<a href="vbscript:msgbox(1)">x</a>`, []string{"vbscript", "href"}, "x"},
		{"relative link", `<a href="/api/v1/admin">x</a>`, []string{"href"}, "x"},
		{"protocol-relative link", `<a href="//evil.example/x">x</a>`, []string{"href", "evil"}, "x"},
		{"area relative href", `<map name="m"><area shape="rect" coords="0,0,1,1" href="/api/v1/admin"></map>ok`, []string{"href", "/api/"}, "ok"},
		{"area protocol-relative href", `<map name="m"><area href="//evil.example/x"></map>ok`, []string{"href", "evil"}, "ok"},
		{"area javascript href", `<map name="m"><area href="javascript:alert(1)"></map>ok`, []string{"href", "javascript"}, "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mail.SanitizeHTML(c.in, knownCID)
			lower := strings.ToLower(got)
			for _, f := range c.forbid {
				if strings.Contains(lower, strings.ToLower(f)) {
					t.Errorf("SanitizeHTML(%q) = %q still holds %q", c.in, got, f)
				}
			}
			if c.keep != "" && !strings.Contains(got, c.keep) {
				t.Errorf("SanitizeHTML(%q) = %q, want it to keep %q", c.in, got, c.keep)
			}
		})
	}
}

func TestSanitizeHTML_LinksOpenApartWithoutReferrer(t *testing.T) {
	got := mail.SanitizeHTML(`<a href="https://example.com/x">x</a>`, nil)
	for _, want := range []string{`href="https://example.com/x"`, `target="_blank"`, "noopener", "noreferrer", "nofollow"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	mailto := mail.SanitizeHTML(`<a href="mailto:post@example.com">mail us</a>`, nil)
	if !strings.Contains(mailto, `href="mailto:post@example.com"`) {
		t.Errorf("mailto link lost: %q", mailto)
	}
}

func TestSanitizeHTML_KeepsSafeStyles(t *testing.T) {
	in := `<div style="color:#333333;background-color:yellow;font-family:Calibri,sans-serif;font-size:11pt;font-weight:bold;` +
		`text-align:center;margin:0 0 8px 0;padding-left:4px;border:1px solid #ccc;width:600px;line-height:1.4;white-space:nowrap;` +
		`position:absolute;z-index:9;display:none">x</div>`
	got := mail.SanitizeHTML(in, nil)
	for _, want := range []string{"color: #333333", "background-color: yellow", "font-family: Calibri,sans-serif", "font-size: 11pt",
		"font-weight: bold", "text-align: center", "margin: 0 0 8px 0", "padding-left: 4px", "border: 1px solid #ccc",
		"width: 600px", "line-height: 1.4", "white-space: nowrap"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	for _, gone := range []string{"position", "z-index", "display"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q keeps %q", got, gone)
		}
	}
}

func TestSanitizeHTML_KeepsMailStructure(t *testing.T) {
	in := `<table width="100%"><tr><td align="left"><h2>Title</h2><p>Para <b>bold</b> <i>it</i> <u>u</u></p>` +
		`<ul><li>one</li></ul><blockquote>quoted</blockquote></td></tr></table>`
	got := mail.SanitizeHTML(in, nil)
	for _, want := range []string{"<table", "<td", "<h2>Title</h2>", "<b>bold</b>", "<ul><li>one</li></ul>", "<blockquote>quoted</blockquote>"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
}

func TestSanitizeHTML_CIDWithANilResolverIsRemoved(t *testing.T) {
	if got := mail.SanitizeHTML(`<img src="cid:logo@example.com">x`, nil); strings.Contains(got, "<img") {
		t.Errorf("got %q", got)
	}
}

func TestSanitizeHTML_ResolverAnsweringAnotherURLIsRemoved(t *testing.T) {
	got := mail.SanitizeHTML(`<img src="cid:a">x`, func(string) (string, bool) { return "https://evil.example/a.png", true })
	if strings.Contains(got, "<img") || strings.Contains(got, "evil") {
		t.Errorf("got %q", got)
	}
}

func TestSanitizeHTML_CorpusOutlook(t *testing.T) {
	in := mustParse(t, "outlook.eml")
	got := mail.SanitizeHTML(in.HTML, nil)
	if strings.Contains(got, "<style") || strings.Contains(got, "<meta") || strings.Contains(got, "position") || strings.Contains(got, "class=") {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(got, "font-family: Calibri,sans-serif") || !strings.Contains(got, "Vi går videre.") {
		t.Errorf("got %q", got)
	}
}
