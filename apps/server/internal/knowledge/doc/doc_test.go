package doc

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

// everything is a document with every node and mark the allowlist accepts,
// with the attributes the editor writes.
const everything = `{"type":"doc","content":[
 {"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"Routines"}]},
 {"type":"paragraph","content":[
   {"type":"text","text":"Read "},
   {"type":"text","marks":[{"type":"link","attrs":{"href":"https://example.com/a","target":"_blank","rel":"noopener noreferrer nofollow","class":null,"title":null}}],"text":"the guide"},
   {"type":"text","text":" and "},
   {"type":"text","marks":[{"type":"bold"},{"type":"italic"},{"type":"strike"},{"type":"underline"},{"type":"code"}],"text":"this"},
   {"type":"hardBreak"},
   {"type":"text","marks":[{"type":"link","attrs":{"href":"/knowledge/articles/1"}}],"text":"next"}
 ]},
 {"type":"bulletList","content":[
   {"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]},
     {"type":"orderedList","attrs":{"start":3,"type":null},"content":[
       {"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"three"}]}]},
       {"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"four"}]}]}]}]},
   {"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"two"}]}]}]},
 {"type":"taskList","content":[
   {"type":"taskItem","attrs":{"checked":true},"content":[{"type":"paragraph","content":[{"type":"text","text":"done"}]}]},
   {"type":"taskItem","attrs":{"checked":false},"content":[{"type":"paragraph","content":[{"type":"text","text":"todo"}]}]}]},
 {"type":"blockquote","content":[{"type":"paragraph","content":[{"type":"text","text":"quoted"}]},{"type":"paragraph","content":[{"type":"text","text":"twice"}]}]},
 {"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x := 1\ny := 2"}]},
 {"type":"horizontalRule"},
 {"type":"paragraph"},
 {"type":"heading","attrs":{"level":3},"content":[{"type":"text","text":"End"}]}
]}`

func TestParse_AcceptsEveryAllowedNodeAndMark(t *testing.T) {
	n, err := Parse([]byte(everything))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if n.Type != "doc" || len(n.Content) != 9 {
		t.Fatalf("doc = %s with %d blocks, want doc with 9", n.Type, len(n.Content))
	}
	if err := Validate([]byte(`{"type":"doc"}`)); err != nil {
		t.Errorf("an empty document: %v", err)
	}
	if err := Validate([]byte(`{"type":"doc","content":[]}`)); err != nil {
		t.Errorf("a document with no blocks: %v", err)
	}
}

func TestText_IsBlockSeparatedAndMarkdownish(t *testing.T) {
	n, err := Parse([]byte(everything))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"# Routines",
		"Read the guide and this\nnext",
		"- one\n  3. three\n  4. four\n- two",
		"- [x] done\n- [ ] todo",
		"> quoted\n>\n> twice",
		"```go\nx := 1\ny := 2\n```",
		"---",
		"### End",
	}, "\n\n")
	if got := Text(n); got != want {
		t.Errorf("Text =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(Text(n), "example.com") || strings.Contains(Text(n), "/knowledge/articles/1") {
		t.Error("a link's target reached the text; only its own text may (K8)")
	}
	if Text(nil) != "" {
		t.Error("Text(nil) is not empty")
	}
}

func TestParse_RefusesWithThePath(t *testing.T) {
	for _, tc := range []struct {
		name, doc, path string
	}{
		{"not JSON", `{"type":`, "$"},
		{"two values", `{"type":"doc"} {}`, "$"},
		{"a root that is not doc", `{"type":"paragraph"}`, "$.type"},
		{"an array root", `[]`, "$.type"},
		{"an unknown node", `{"type":"doc","content":[{"type":"video","attrs":{"src":"x"}}]}`, "$.content[0].type"},
		{"a nested doc", `{"type":"doc","content":[{"type":"doc"}]}`, "$.content[0].type"},
		{"an unknown field", `{"type":"doc","content":[{"type":"paragraph","html":"<b>"}]}`, "$.content[0].html"},
		{"an unknown attribute", `{"type":"doc","content":[{"type":"paragraph","attrs":{"textAlign":"left"}}]}`, "$.content[0].attrs.textAlign"},
		{"a heading of level 4", `{"type":"doc","content":[{"type":"heading","attrs":{"level":4}}]}`, "$.content[0].attrs.level"},
		{"a heading level as text", `{"type":"doc","content":[{"type":"heading","attrs":{"level":"1"}}]}`, "$.content[0].attrs.level"},
		{"an ordered list's start as a fraction", `{"type":"doc","content":[{"type":"orderedList","attrs":{"start":1.5}}]}`, "$.content[0].attrs.start"},
		{"an ordered list's unknown type", `{"type":"doc","content":[{"type":"orderedList","attrs":{"type":"disc"}}]}`, "$.content[0].attrs.type"},
		{"a task item's checked as text", `{"type":"doc","content":[{"type":"taskList","content":[{"type":"taskItem","attrs":{"checked":"yes"}}]}]}`, "$.content[0].content[0].attrs.checked"},
		{"a code block's odd language", `{"type":"doc","content":[{"type":"codeBlock","attrs":{"language":"<script>"}}]}`, "$.content[0].attrs.language"},
		{"an unknown mark", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a","marks":[{"type":"highlight"}]}]}]}`, "$.content[0].content[0].marks[0].type"},
		{"a mark on a block", `{"type":"doc","content":[{"type":"paragraph","marks":[{"type":"bold"}]}]}`, "$.content[0].marks"},
		{"a mark with an unknown field", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a","marks":[{"type":"bold","style":"x"}]}]}]}`, "$.content[0].content[0].marks[0].style"},
		{"a bold with attributes", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a","marks":[{"type":"bold","attrs":{"x":1}}]}]}]}`, "$.content[0].content[0].marks[0].attrs.x"},
		{"a javascript link", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}]}]}`, "$.content[0].content[0].marks[0].attrs.href"},
		{"a link without href", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a","marks":[{"type":"link","attrs":{"target":"_blank"}}]}]}]}`, "$.content[0].content[0].marks[0].attrs.href"},
		{"a link's other target", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a","marks":[{"type":"link","attrs":{"href":"/a","target":"_top"}}]}]}]}`, "$.content[0].content[0].marks[0].attrs.target"},
		{"a link's other rel", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a","marks":[{"type":"link","attrs":{"href":"/a","rel":"opener"}}]}]}]}`, "$.content[0].content[0].marks[0].attrs.rel"},
		{"a link's class", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a","marks":[{"type":"link","attrs":{"href":"/a","class":"x"}}]}]}]}`, "$.content[0].content[0].marks[0].attrs.class"},
		{"text on a paragraph", `{"type":"doc","content":[{"type":"paragraph","text":"a"}]}`, "$.content[0].text"},
		{"an empty text", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":""}]}]}`, "$.content[0].content[0].text"},
		{"a text without text", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text"}]}]}`, "$.content[0].content[0].text"},
		{"content in a leaf", `{"type":"doc","content":[{"type":"horizontalRule","content":[]}]}`, "$.content[0].content"},
		{"content that is not a list", `{"type":"doc","content":{"type":"paragraph"}}`, "$.content"},
		{"a node that is not an object", `{"type":"doc","content":["text"]}`, "$.content[0]"},
		{"attrs that are not an object", `{"type":"doc","content":[{"type":"heading","attrs":[1]}]}`, "$.content[0].attrs"},
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

func TestParse_RefusesDeepNesting(t *testing.T) {
	doc := `{"type":"doc","content":[` + strings.Repeat(`{"type":"blockquote","content":[`, maxDepth+1) +
		strings.Repeat(`]}`, maxDepth+1) + `]}`
	var invalid *InvalidError
	if err := Validate([]byte(doc)); !errors.As(err, &invalid) || !strings.Contains(invalid.Reason, "nested") {
		t.Errorf("Validate = %v, want the nesting refused", err)
	}
}

func TestParse_RefusesOver2MiB(t *testing.T) {
	text := strings.Repeat("a", MaxBytes)
	doc := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"` + text + `"}]}]}`
	if err := Validate([]byte(doc)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Validate = %v, want ErrTooLarge", err)
	}
	// Just under the limit is accepted, and its text derived whole.
	under := strings.Repeat("b", MaxBytes-200)
	n, err := Parse([]byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"` + under + `"}]}]}`))
	if err != nil {
		t.Fatalf("Parse just under the limit: %v", err)
	}
	if got := Text(n); len(got) != len(under) {
		t.Errorf("len(Text) = %d, want %d", len(got), len(under))
	}
}

func TestLinkHrefAllowed(t *testing.T) {
	for href, want := range map[string]bool{
		"https://example.com":           true,
		"HTTPS://example.com/x?y=1#z":   true,
		"http://example.com":            true,
		"mailto:kari@example.com":       true,
		"/knowledge/articles/abc":       true,
		"/":                             true,
		"//evil.example.com":            false,
		"/\\evil.example.com":           false,
		"javascript:alert(1)":           false,
		"JavaScript:alert(1)":           false,
		"data:text/html,<b>":            false,
		"vbscript:x":                    false,
		"ftp://example.com":             false,
		"https:example.com":             false,
		"https://":                      false,
		"example.com":                   false,
		"":                              false,
		"https://exa mple.com":          false,
		"https://example.com/\n":        false,
		"/" + strings.Repeat("a", 2048): false,
	} {
		if got := LinkHrefAllowed(href); got != want {
			t.Errorf("LinkHrefAllowed(%q) = %v, want %v", href, got, want)
		}
	}
}

func TestAllowlist_NamesTheEditorsSchema(t *testing.T) {
	nodes, marks := Allowlist()
	wantNodes := []string{"articleLink", "blockquote", "bulletList", "callout", "codeBlock", "details", "detailsContent", "detailsSummary",
		"doc", "fileAttachment", "hardBreak", "heading", "horizontalRule", "image", "listItem", "mention", "orderedList", "paragraph",
		"recordLink", "table", "tableCell", "tableHeader", "tableOfContents", "tableRow", "taskItem", "taskList", "text"}
	gotNodes := make([]string, 0, len(nodes))
	for k := range nodes {
		gotNodes = append(gotNodes, k)
	}
	slices.Sort(gotNodes)
	if !slices.Equal(gotNodes, wantNodes) {
		t.Errorf("nodes = %v, want %v", gotNodes, wantNodes)
	}
	if !slices.Equal(nodes["orderedList"], []string{"id", "start", "type"}) || !slices.Equal(nodes["heading"], []string{"id", "level"}) ||
		!slices.Equal(nodes["image"], []string{"alt", "id", "src", "title", "width"}) || !slices.Equal(nodes["recordLink"], []string{"label", "path"}) {
		t.Errorf("attributes = %v", nodes)
	}
	if !slices.Equal(marks["link"], []string{"class", "href", "rel", "target", "title"}) || len(marks) != 6 {
		t.Errorf("marks = %v", marks)
	}
}

// The allowlist mirrors the editor's schema exactly (K8): every node and mark
// the editor can write, with the attributes it writes, as the UI package
// exports them to src/editor/schema.json.
func TestAllowlist_MatchesTheEditorsExportedSchema(t *testing.T) {
	raw, err := os.ReadFile("../../../../knowledge/frontend/src/editor/schema.json")
	if err != nil {
		t.Fatalf("read the editor's exported schema: %v", err)
	}
	var schema struct {
		Nodes map[string]struct {
			Attrs []string `json:"attrs"`
		} `json:"nodes"`
		Marks map[string]struct {
			Attrs []string `json:"attrs"`
		} `json:"marks"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse the editor's exported schema: %v", err)
	}
	nodes, marks := Allowlist()
	compare := func(kind string, ours map[string][]string, theirs map[string][]string) {
		for name, attrs := range theirs {
			got, ok := ours[name]
			if !ok {
				t.Errorf("the editor writes %s %q, which the allowlist refuses", kind, name)
				continue
			}
			want := slices.Clone(attrs)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("%s %q: the allowlist names attributes %v, the editor %v", kind, name, got, want)
			}
		}
		for name := range ours {
			if _, ok := theirs[name]; !ok {
				t.Errorf("the allowlist accepts %s %q, which the editor does not have", kind, name)
			}
		}
	}
	editorNodes := map[string][]string{}
	for k, v := range schema.Nodes {
		editorNodes[k] = v.Attrs
	}
	editorMarks := map[string][]string{}
	for k, v := range schema.Marks {
		editorMarks[k] = v.Attrs
	}
	compare("node", nodes, editorNodes)
	compare("mark", marks, editorMarks)
}
