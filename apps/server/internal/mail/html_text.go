package mail

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// TextFromHTML answers the plain text of sanitized, HTML SanitizeHTML
// answered, for a message that carries no text body: block elements and
// <br> break lines, paragraphs, headings, lists, tables and quotes are set
// apart by a blank line, each <li> starts "- ", links are their text,
// whitespace outside <pre> is collapsed, and there are never more than two
// blank lines in a row.
func TextFromHTML(sanitized string) string {
	nodes, err := html.ParseFragment(strings.NewReader(sanitized), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return ""
	}
	t := &textWriter{}
	for _, n := range nodes {
		t.node(n, false)
	}
	return t.result()
}

type textWriter struct {
	b strings.Builder
	// space is a collapsed whitespace run waiting for the next word.
	space bool
}

var paragraphAtoms = map[atom.Atom]bool{
	atom.P: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
	atom.Ul: true, atom.Ol: true, atom.Dl: true, atom.Table: true, atom.Blockquote: true, atom.Pre: true,
	atom.Hr: true, atom.Figure: true,
}

var lineAtoms = map[atom.Atom]bool{
	atom.Div: true, atom.Li: true, atom.Tr: true, atom.Dt: true, atom.Dd: true, atom.Section: true,
	atom.Article: true, atom.Aside: true, atom.Header: true, atom.Footer: true, atom.Address: true,
	atom.Caption: true, atom.Figcaption: true, atom.Details: true, atom.Summary: true,
}

func (t *textWriter) node(n *html.Node, pre bool) {
	switch n.Type {
	case html.TextNode:
		t.text(n.Data, pre)
		return
	case html.ElementNode:
	default:
		return
	}
	switch n.DataAtom {
	case atom.Script, atom.Style, atom.Head, atom.Title:
		return
	case atom.Br:
		t.space = false
		if t.b.Len() > 0 {
			t.b.WriteByte('\n')
		}
		return
	case atom.Td, atom.Th:
		if n.PrevSibling != nil {
			t.space = true
		}
	}
	switch {
	case paragraphAtoms[n.DataAtom]:
		t.newline(2)
	case lineAtoms[n.DataAtom]:
		t.newline(1)
	}
	if n.DataAtom == atom.Li {
		t.b.WriteString("- ")
	}
	inPre := pre || n.DataAtom == atom.Pre
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		t.node(c, inPre)
	}
	switch {
	case paragraphAtoms[n.DataAtom]:
		t.newline(2)
	case lineAtoms[n.DataAtom]:
		t.newline(1)
	}
}

func (t *textWriter) text(s string, pre bool) {
	if pre {
		t.flushSpace()
		t.b.WriteString(s)
		return
	}
	for i, word := range strings.Fields(s) {
		if i > 0 || startsWithSpace(s) {
			t.space = true
		}
		t.flushSpace()
		t.b.WriteString(word)
	}
	if endsWithSpace(s) {
		t.space = true
	}
}

func (t *textWriter) flushSpace() {
	if t.space && t.b.Len() > 0 && !strings.HasSuffix(t.b.String(), "\n") && !strings.HasSuffix(t.b.String(), " ") {
		t.b.WriteByte(' ')
	}
	t.space = false
}

// newline ends the current line with at least n line breaks in a row.
func (t *textWriter) newline(n int) {
	t.space = false
	if t.b.Len() == 0 {
		return
	}
	s := t.b.String()
	have := len(s) - len(strings.TrimRight(s, "\n"))
	for ; have < n; have++ {
		t.b.WriteByte('\n')
	}
}

func (t *textWriter) result() string {
	lines := strings.Split(t.b.String(), "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t ")
		if line == "" {
			blank++
			if blank > 2 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

func startsWithSpace(s string) bool { return s != "" && strings.TrimLeft(s, " \t\r\n\f") != s }

func endsWithSpace(s string) bool { return s != "" && strings.TrimRight(s, " \t\r\n\f") != s }
