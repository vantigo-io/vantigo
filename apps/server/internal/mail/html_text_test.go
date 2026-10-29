package mail_test

import (
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/mail"
)

func TestTextFromHTML(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"paragraphs", `<p>One</p><p>Two</p>`, "One\n\nTwo"},
		{"br", `<p>line one<br>line two<br/>line three</p>`, "line one\nline two\nline three"},
		{"divs", `<div>a</div><div>b</div>`, "a\nb"},
		{"lists", `<p>Items:</p><ul><li>one</li><li>two <b>bold</b></li></ul><ol><li>first</li></ol>`, "Items:\n\n- one\n- two bold\n\n- first"},
		{"entities", `<p>Fish &amp; chips &lt;3 &nbsp;&quot;x&quot; &aelig;&oslash;&aring; &euro;</p>`, `Fish & chips <3 "x" æøå €`},
		{"whitespace collapsed", "<p>  lots\n\tof   \n space  </p>", "lots of space"},
		{"inline elements keep spaces", `<p>a <b>b</b> <i>c</i>d</p>`, "a b cd"},
		{"links are their text", `<p>See <a href="https://example.com">our site</a>.</p>`, "See our site."},
		{"headings", `<h1>Title</h1>Body`, "Title\n\nBody"},
		{"blank lines capped", `<p>a</p><br><br><br><br><br><br><p>b</p>`, "a\n\n\nb"},
		{"pre kept", "<pre>  x\n    y</pre>", "  x\n    y"},
		{"table cells", `<table><tr><td>a</td><td>b</td></tr><tr><td>c</td></tr></table>`, "a b\nc"},
		{"empty", ``, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mail.TextFromHTML(c.in); got != c.want {
				t.Errorf("TextFromHTML(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestTextFromHTML_CorpusHTMLOnly(t *testing.T) {
	in := mustParse(t, "html-only.eml")
	got := mail.TextFromHTML(mail.SanitizeHTML(in.HTML, nil))
	if want := "Nyheter\n\nFørste linje\nandre linje\n\n- En\n- To"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
