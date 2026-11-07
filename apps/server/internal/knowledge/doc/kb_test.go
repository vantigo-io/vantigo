package doc

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

const (
	fileA    = "0b5f0c6e-8f3a-4b8e-9a3d-1c2b3a4d5e6f"
	fileB    = "1c6f1d7f-9f4b-4c9f-8b4e-2d3c4b5e6f70"
	personA  = "2d7a2e80-a05c-4da0-9c5f-3e4d5c6f7081"
	articleA = "3e8b3f91-b16d-4eb1-8d60-4f5e6d708192"
)

// kb is a document with every node K-B adds (K18–K22).
var kb = `{"type":"doc","content":[
 {"type":"heading","attrs":{"id":"h1","level":2},"content":[{"type":"text","text":"Routines"}]},
 {"type":"tableOfContents","attrs":{"id":"toc"}},
 {"type":"callout","attrs":{"id":"c1","kind":"warning","emoji":"⚠️"},"content":[{"type":"paragraph","attrs":{"id":null},"content":[{"type":"text","text":"Mind the step"}]}]},
 {"type":"details","attrs":{"id":"d1","open":false},"content":[
   {"type":"detailsSummary","content":[{"type":"text","text":"More"}]},
   {"type":"detailsContent","content":[{"type":"paragraph","content":[{"type":"text","text":"Hidden text"}]}]}]},
 {"type":"table","attrs":{"id":"t1"},"content":[
   {"type":"tableRow","content":[
     {"type":"tableHeader","attrs":{"colspan":1,"rowspan":1,"colwidth":[120]},"content":[{"type":"paragraph","content":[{"type":"text","text":"Name"}]}]},
     {"type":"tableHeader","attrs":{"colspan":1,"rowspan":1,"colwidth":null},"content":[{"type":"paragraph","content":[{"type":"text","text":"Role"}]}]}]},
   {"type":"tableRow","content":[
     {"type":"tableCell","attrs":{"colspan":1,"rowspan":1,"colwidth":null},"content":[{"type":"paragraph","content":[{"type":"text","text":"Kari"}]}]},
     {"type":"tableCell","attrs":{"colspan":1,"rowspan":1,"colwidth":null},"content":[{"type":"paragraph","content":[{"type":"text","text":"Lead | owner"}]}]}]}]},
 {"type":"codeBlock","attrs":{"id":"k1","language":"go"},"content":[{"type":"text","text":"x := 1"}]},
 {"type":"image","attrs":{"id":"i1","src":"/api/v1/knowledge/files/` + fileA + `","alt":"The office","title":null,"width":640}},
 {"type":"fileAttachment","attrs":{"id":"f1","fileId":"` + fileB + `","filename":"plan.pdf","size":1234,"contentType":"application/pdf"}},
 {"type":"paragraph","content":[
   {"type":"text","text":"Ask "},
   {"type":"mention","attrs":{"id":"` + personA + `","label":"Kari Nordmann"}},
   {"type":"text","text":" about "},
   {"type":"articleLink","attrs":{"id":"` + articleA + `","label":"Secret salaries"}},
   {"type":"text","text":" and "},
   {"type":"recordLink","attrs":{"path":"/customers/42","label":"Acme AS"}}
 ]}
]}`

func TestParse_AcceptsTheKBNodes(t *testing.T) {
	n, err := Parse([]byte(kb))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(n.Content) != 9 {
		t.Fatalf("%d blocks, want 9", len(n.Content))
	}
}

func TestText_KBNodes(t *testing.T) {
	n, err := Parse([]byte(kb))
	if err != nil {
		t.Fatal(err)
	}
	got := Text(n)
	for _, want := range []string{"## Routines", "Mind the step", "More", "Hidden text", "Name | Role", "Kari | Lead | owner", "The office", "plan.pdf", "@Kari Nordmann"} {
		if !strings.Contains(got, want) {
			t.Errorf("Text lacks %q:\n%s", want, got)
		}
	}
	// An article link's and a record link's label never reach the text (K8).
	for _, leak := range []string{"Secret salaries", "Acme AS", "/customers/42", articleA} {
		if strings.Contains(got, leak) {
			t.Errorf("Text holds %q:\n%s", leak, got)
		}
	}
}

func TestReferences(t *testing.T) {
	n, err := Parse([]byte(kb))
	if err != nil {
		t.Fatal(err)
	}
	r := References(n)
	if !slices.Equal(r.Articles, []string{articleA}) || !slices.Equal(r.Mentions, []string{personA}) || !slices.Equal(r.Files, []string{fileA, fileB}) {
		t.Errorf("References = %+v", r)
	}
}

func TestParse_RefusesKBAttributes(t *testing.T) {
	wrap := func(block string) string { return `{"type":"doc","content":[` + block + `]}` }
	para := func(inline string) string { return wrap(`{"type":"paragraph","content":[` + inline + `]}`) }
	for _, tc := range []struct{ name, doc, path string }{
		{"an image elsewhere", wrap(`{"type":"image","attrs":{"src":"https://evil.example/x.png"}}`), "$.content[0].attrs.src"},
		{"an image of a path with more", wrap(`{"type":"image","attrs":{"src":"/api/v1/knowledge/files/` + fileA + `/../x"}}`), "$.content[0].attrs.src"},
		{"an image of a data URL", wrap(`{"type":"image","attrs":{"src":"data:image/png;base64,AAAA"}}`), "$.content[0].attrs.src"},
		{"an image without src", wrap(`{"type":"image","attrs":{"alt":"x"}}`), "$.content[0].attrs.src"},
		{"an image's fractional width", wrap(`{"type":"image","attrs":{"src":"/api/v1/knowledge/files/` + fileA + `","width":1.5}}`), "$.content[0].attrs.width"},
		{"a callout of another kind", wrap(`{"type":"callout","attrs":{"kind":"success"}}`), "$.content[0].attrs.kind"},
		{"a callout's long emoji", wrap(`{"type":"callout","attrs":{"kind":"info","emoji":"` + strings.Repeat("x", 33) + `"}}`), "$.content[0].attrs.emoji"},
		{"a toggle's open as text", wrap(`{"type":"details","attrs":{"open":"yes"}}`), "$.content[0].attrs.open"},
		{"a cell's colwidth as text", wrap(`{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","attrs":{"colwidth":["1"]}}]}]}`), "$.content[0].content[0].content[0].attrs.colwidth"},
		{"a cell's zero colspan", wrap(`{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","attrs":{"colspan":0}}]}]}`), "$.content[0].content[0].content[0].attrs.colspan"},
		{"a block id with markup", wrap(`{"type":"paragraph","attrs":{"id":"<b>"}}`), "$.content[0].attrs.id"},
		{"a file attachment without its id", wrap(`{"type":"fileAttachment","attrs":{"filename":"x"}}`), "$.content[0].attrs.fileId"},
		{"a file attachment's id not a uuid", wrap(`{"type":"fileAttachment","attrs":{"fileId":"../x"}}`), "$.content[0].attrs.fileId"},
		{"a mention's id not a uuid", para(`{"type":"mention","attrs":{"id":"kari"}}`), "$.content[0].content[0].attrs.id"},
		{"an article link without id", para(`{"type":"articleLink","attrs":{"label":"x"}}`), "$.content[0].content[0].attrs.id"},
		{"a record link to another site", para(`{"type":"recordLink","attrs":{"path":"https://evil.example"}}`), "$.content[0].content[0].attrs.path"},
		{"a record link protocol-relative", para(`{"type":"recordLink","attrs":{"path":"//evil.example"}}`), "$.content[0].content[0].attrs.path"},
		{"a record link with a query", para(`{"type":"recordLink","attrs":{"path":"/customers?x=<b>"}}`), "$.content[0].content[0].attrs.path"},
		{"a table of contents with content", wrap(`{"type":"tableOfContents","content":[]}`), "$.content[0].content"},
		{"a mention with content", para(`{"type":"mention","attrs":{"id":"` + personA + `"},"content":[]}`), "$.content[0].content[0].content"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate([]byte(tc.doc))
			var invalid *InvalidError
			if !errors.As(err, &invalid) {
				t.Fatalf("Validate = %v, want an *InvalidError", err)
			}
			if invalid.Path != tc.path {
				t.Errorf("Path = %q (%s), want %q", invalid.Path, invalid.Reason, tc.path)
			}
		})
	}
}

func TestMarkdown_Export(t *testing.T) {
	n, err := Parse([]byte(kb))
	if err != nil {
		t.Fatal(err)
	}
	got := Markdown(n, Exporter{
		Article: func(id string) (string, bool) { return "", false },
		File: func(id string) (string, bool) {
			return "assets/" + id + ".bin", id == fileA || id == fileB
		},
	})
	for _, want := range []string{
		"## Routines",
		"> [!warning] ⚠️\n> Mind the step",
		"> [!note]- More\n> Hidden text",
		"| Name | Role |\n| --- | --- |\n| Kari | Lead \\| owner |",
		"```go\nx := 1\n```",
		"![The office](assets/" + fileA + ".bin)",
		"[plan.pdf](assets/" + fileB + ".bin)",
		"Ask @Kari Nordmann about No access and [Acme AS](/customers/42)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Markdown lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Secret salaries") {
		t.Errorf("an export without access to the target wrote its stored label:\n%s", got)
	}
	readable := Markdown(n, Exporter{Article: func(id string) (string, bool) { return "Pay [2026]", id == articleA }})
	if !strings.Contains(readable, "[[Pay (2026)]]") {
		t.Errorf("a readable link is not a wiki link:\n%s", readable)
	}
}

func TestMarkdown_EscapesWhatWouldBeMarkup(t *testing.T) {
	d := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"# not a heading *nor* [a link](x) <b>"}]},
	 {"type":"paragraph","content":[{"type":"text","text":"1. not a list"}]},
	 {"type":"paragraph","content":[{"type":"text","marks":[{"type":"bold"}],"text":"bold "},{"type":"text","marks":[{"type":"code"}],"text":"a ` + "`" + `tick"}]}]}`
	n, err := Parse([]byte(d))
	if err != nil {
		t.Fatal(err)
	}
	md := Markdown(n, Exporter{})
	back := FromMarkdown([]byte(md), Importer{})
	raw, _ := json.Marshal(back)
	n2, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(FromMarkdown): %v\n%s", err, raw)
	}
	if Text(n2) != Text(n) {
		t.Errorf("round trip of text:\n%s\nwant\n%s\n(markdown %q)", Text(n2), Text(n), md)
	}
}

func TestFromMarkdown(t *testing.T) {
	src := "# Title\n\nSome **bold** and *it* and ~~gone~~ and `code` and [site](https://example.com).\n" +
		"A [[Other page|other]] link, [rel](Other%20page.md) and ![[pic.png]] embedded.\n\n" +
		"> [!TIP] 💡\n> Be kind.\n\n> [!faq]- Why?\n> Because.\n\n> Plain quote\n\n" +
		"- one\n  - nested\n- two\n\n1. first\n2. second\n\n- [ ] todo\n- [x] done\n\n" +
		"| A | B |\n|---|---|\n| 1 | 2 |\n\n```python\nprint('[[not a link]]')\n```\n\n---\n\n#### Deep\n\n![missing](https://example.com/x.png)\n"
	var warnings []string
	body := FromMarkdown([]byte(src), Importer{
		Wiki:     func(t string) (string, bool) { return articleA, t == "Other page" },
		Relative: func(d string) (string, bool) { return articleA, d == "Other page.md" },
		Image:    func(d string) (string, bool) { return fileA, d == "pic.png" },
		Warn:     func(m string) { warnings = append(warnings, m) },
	})
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	n, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, raw)
	}
	var types []string
	for _, c := range n.Content {
		types = append(types, c.Type)
	}
	want := []string{"heading", "paragraph", "image", "paragraph", "callout", "details", "blockquote", "bulletList", "orderedList", "taskList", "table", "codeBlock", "horizontalRule", "heading", "paragraph"}
	if !slices.Equal(types, want) {
		t.Fatalf("blocks = %v\nwant %v\n%s", types, want, raw)
	}
	refs := References(n)
	if !slices.Equal(refs.Articles, []string{articleA}) || !slices.Equal(refs.Files, []string{fileA}) {
		t.Errorf("refs = %+v", refs)
	}
	text := Text(n)
	for _, w := range []string{"Some bold and it and gone and code and site.", "Be kind.", "Why?", "Because.", "- [x] done", "A | B", "print('[[not a link]]')", "### Deep"} {
		if !strings.Contains(text, w) {
			t.Errorf("text lacks %q:\n%s", w, text)
		}
	}
	if c := n.Content[4]; c.Attrs["kind"] != "tip" || c.Attrs["emoji"] != "💡" {
		t.Errorf("callout attrs = %v", c.Attrs)
	}
	if len(warnings) == 0 {
		t.Error("a remote image was dropped without a warning")
	}
}

// Markdown and back keeps a document's structure (K23).
func TestMarkdown_RoundTrip(t *testing.T) {
	n, err := Parse([]byte(kb))
	if err != nil {
		t.Fatal(err)
	}
	md := Markdown(n, Exporter{
		Article: func(id string) (string, bool) { return "Pay", true },
		File:    func(id string) (string, bool) { return "assets/" + id + ".png", true },
	})
	back := FromMarkdown([]byte(md), Importer{
		Wiki: func(t string) (string, bool) { return articleA, t == "Pay" },
		Image: func(d string) (string, bool) {
			return strings.TrimSuffix(strings.TrimPrefix(d, "assets/"), ".png"), true
		},
	})
	raw, _ := json.Marshal(back)
	n2, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, raw)
	}
	var got []string
	for _, c := range n2.Content {
		got = append(got, c.Type)
	}
	// The table of contents has no Markdown; a file attachment comes back as
	// a link to its file.
	want := []string{"heading", "callout", "details", "table", "codeBlock", "image", "paragraph", "paragraph"}
	if !slices.Equal(got, want) {
		t.Errorf("blocks = %v\nwant %v\n%s", got, want, md)
	}
}

// The stored form (K21, review 6): an article link's label is dropped and
// every id is lower-case.
func TestCanonical_DropsLinkLabelsAndLowersIDs(t *testing.T) {
	upper := strings.ToUpper(fileA)
	raw := `{"type":"doc","content":[{"type":"image","attrs":{"src":"/api/v1/knowledge/files/` + upper + `"}},
	 {"type":"fileAttachment","attrs":{"fileId":"` + strings.ToUpper(fileB) + `"}},
	 {"type":"paragraph","content":[{"type":"articleLink","attrs":{"id":"` + strings.ToUpper(articleA) + `","label":"Salaries"}},
	   {"type":"mention","attrs":{"id":"` + strings.ToUpper(personA) + `","label":"Kari"}}]}]}`
	out, n, err := Canonical([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "Salaries") || strings.Contains(s, upper) || !strings.Contains(s, fileB) || !strings.Contains(s, articleA) || !strings.Contains(s, personA) || !strings.Contains(s, `"label":null`) || !strings.Contains(s, "Kari") {
		t.Errorf("Canonical = %s", s)
	}
	if r := References(n); r.FilePaths[fileA] != "$.content[0]" || r.FilePaths[fileB] != "$.content[1]" {
		t.Errorf("FilePaths = %v", r.FilePaths)
	}
	if err := Validate(out); err != nil {
		t.Errorf("the canonical form does not validate: %v", err)
	}
}

// An import drops what the allowlist refuses, never the whole article.
func TestRepair_DropsOnlyWhatIsRefused(t *testing.T) {
	body := map[string]any{"type": "doc", "content": []any{
		map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "kept"}}},
		map[string]any{"type": "heading", "attrs": map[string]any{"level": 9}, "content": []any{map[string]any{"type": "text", "text": "heading kept"}}},
		map[string]any{"type": "video"},
		map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "link", "marks": []any{
			map[string]any{"type": "link", "attrs": map[string]any{"href": "javascript:alert(1)"}}}}}},
	}}
	var warnings []string
	out, n, err := Repair(body, func(w string) { warnings = append(warnings, w) })
	if err != nil {
		t.Fatalf("Repair: %v", err)
	}
	text := Text(n)
	if !strings.Contains(text, "kept") || !strings.Contains(text, "heading kept") || !strings.Contains(text, "link") ||
		strings.Contains(string(out), "javascript") || strings.Contains(string(out), "video") || len(warnings) < 3 {
		t.Errorf("Repair = %s (%q), warnings %v", out, text, warnings)
	}
}

// FromMarkdown keeps to the allowlist's limits: no link label, a clamped
// alt text and list start, and images that land in a heading or a table
// cell are text, never resolved (so never uploaded).
func TestFromMarkdown_Clamps(t *testing.T) {
	asked := map[string]bool{}
	src := "99999999. far\n\n![" + strings.Repeat("a", 2000) + "](pic.png)\n\n# ![head](head.png)\n\n| ![cell](cell.png) |\n|---|\n| x |\n\n[[Page|alias]]\n"
	src = strings.ReplaceAll(src, "\\n", "\n")
	body := FromMarkdown([]byte(src), Importer{
		Wiki:  func(string) (string, bool) { return articleA, true },
		Image: func(d string) (string, bool) { asked[d] = true; return fileA, true },
	})
	out, _, err := Repair(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !asked["pic.png"] || asked["head.png"] || asked["cell.png"] {
		t.Errorf("images asked for: %v", asked)
	}
	if strings.Contains(s, "alias") || !strings.Contains(s, `"start":1000000`) || strings.Contains(s, strings.Repeat("a", 1001)) {
		t.Errorf("FromMarkdown = %s", s)
	}
}
