// Package doc is the stored form of a Knowledge article (knowledge design K8):
// a ProseMirror document as the editor's JSON, validated against an
// allowlist of node and mark types and their attributes that mirrors the
// editor's schema (TipTap's StarterKit, Link, TaskList and TaskItem), and the
// plain text derived from it for search and the Assistant.
//
// The browser renders the JSON through the editor's schema and the server
// never emits HTML, so no HTML sanitiser is needed: the allowlist replaces
// it. A node, mark or attribute it does not know is refused with the JSON
// path of the first one found.
package doc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// MaxBytes is the largest document accepted, as its JSON: 2 MiB (K8). A
// larger one is ErrTooLarge, which the API answers 413.
const MaxBytes = 2 << 20

// maxDepth bounds the nesting of nodes, far above anything the editor makes,
// so a crafted document cannot make validation or text derivation recurse
// without end.
const maxDepth = 64

// ErrTooLarge is a document over MaxBytes.
var ErrTooLarge = errors.New("knowledge/doc: the document is larger than 2 MiB")

// InvalidError is a document the allowlist refuses: Path is the JSON path of
// what was refused ("$.content[2].marks[0].attrs.href"), Reason says why.
type InvalidError struct {
	Path   string
	Reason string
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("knowledge/doc: %s: %s", e.Path, e.Reason)
}

// Node is one node of a validated document.
type Node struct {
	Type    string
	Attrs   map[string]any
	Content []Node
	Marks   []Mark
	Text    string
}

// Mark is one mark on a text node.
type Mark struct {
	Type  string
	Attrs map[string]any
}

// attrRule validates one attribute's value; it answers "" when the value is
// allowed, otherwise why not.
type attrRule func(v any) string

// nodeSpec is what the allowlist says about a node type.
type nodeSpec struct {
	attrs map[string]attrRule
	// leaf nodes carry no content; text is the only one that carries text.
	leaf bool
	// inline nodes may carry marks.
	inline bool
}

// The allowlist (K8), mirroring the editor's schema in
// @vantigo/knowledge-ui: StarterKit's nodes (headings 1–3), Link, and the
// task lists. Every attribute the editor writes into its JSON is named, with
// the values it may hold; null is allowed wherever the editor's default is
// null.
var nodes = map[string]nodeSpec{
	"doc":            {},
	"paragraph":      {},
	"heading":        {attrs: map[string]attrRule{"level": intIn(1, 3)}},
	"bulletList":     {},
	"orderedList":    {attrs: map[string]attrRule{"start": intIn(-1_000_000, 1_000_000), "type": nullOr(oneOf("1", "a", "A", "i", "I"))}},
	"listItem":       {},
	"taskList":       {},
	"taskItem":       {attrs: map[string]attrRule{"checked": isBool}},
	"blockquote":     {},
	"codeBlock":      {attrs: map[string]attrRule{"language": nullOr(matches(languagePattern, "a language name"))}},
	"horizontalRule": {leaf: true},
	"hardBreak":      {leaf: true, inline: true},
	"text":           {leaf: true, inline: true},
}

var marks = map[string]map[string]attrRule{
	"bold":   nil,
	"italic": nil,
	"strike": nil,
	"code":   nil,
	"link": {
		"href":   linkHref,
		"target": nullOr(oneOf("_blank")),
		"rel":    nullOr(oneOf("noopener noreferrer nofollow")),
		"class":  isNull,
		"title":  nullOr(stringUpTo(500)),
	},
}

// languagePattern is a code block's language, as lowlight names them.
var languagePattern = regexp.MustCompile(`^[A-Za-z0-9_+#.-]{1,32}$`)

// Parse validates raw against the allowlist and answers the document. Its
// error is ErrTooLarge or an *InvalidError.
func Parse(raw []byte) (*Node, error) {
	if len(raw) > MaxBytes {
		return nil, ErrTooLarge
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, &InvalidError{Path: "$", Reason: "not JSON: " + err.Error()}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, &InvalidError{Path: "$", Reason: "more than one JSON value"}
	}
	obj, ok := v.(map[string]any)
	if !ok || obj["type"] != "doc" {
		return nil, &InvalidError{Path: "$.type", Reason: `the document's root must be a node of type "doc"`}
	}
	n, err := parseNode(obj, "$", 0)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// Validate is Parse without the document.
func Validate(raw []byte) error {
	_, err := Parse(raw)
	return err
}

func parseNode(obj map[string]any, path string, depth int) (Node, error) {
	if depth > maxDepth {
		return Node{}, &InvalidError{Path: path, Reason: fmt.Sprintf("nested more than %d levels deep", maxDepth)}
	}
	for _, key := range sortedKeys(obj) {
		switch key {
		case "type", "attrs", "content", "marks", "text":
		default:
			return Node{}, &InvalidError{Path: path + "." + key, Reason: "not a field of a node"}
		}
	}
	typ, _ := obj["type"].(string)
	spec, ok := nodes[typ]
	if !ok {
		return Node{}, &InvalidError{Path: path + ".type", Reason: fmt.Sprintf("node type %q is not allowed", obj["type"])}
	}
	if typ == "doc" && depth > 0 {
		return Node{}, &InvalidError{Path: path + ".type", Reason: `a "doc" node is only allowed at the root`}
	}
	n := Node{Type: typ}

	attrs, err := parseAttrs(obj["attrs"], spec.attrs, path+".attrs")
	if err != nil {
		return Node{}, err
	}
	n.Attrs = attrs

	if raw, present := obj["text"]; present {
		s, ok := raw.(string)
		if typ != "text" || !ok || s == "" {
			return Node{}, &InvalidError{Path: path + ".text", Reason: "only a text node carries text, a non-empty string"}
		}
		n.Text = s
	} else if typ == "text" {
		return Node{}, &InvalidError{Path: path + ".text", Reason: "a text node needs its text"}
	}

	if raw, present := obj["marks"]; present {
		list, ok := raw.([]any)
		if !ok || !spec.inline {
			return Node{}, &InvalidError{Path: path + ".marks", Reason: "only an inline node carries marks, as a list"}
		}
		for i, m := range list {
			mark, err := parseMark(m, fmt.Sprintf("%s.marks[%d]", path, i))
			if err != nil {
				return Node{}, err
			}
			n.Marks = append(n.Marks, mark)
		}
	}

	if raw, present := obj["content"]; present {
		list, ok := raw.([]any)
		if !ok || spec.leaf {
			return Node{}, &InvalidError{Path: path + ".content", Reason: "only a node that is not a leaf carries content, as a list"}
		}
		for i, c := range list {
			childPath := fmt.Sprintf("%s.content[%d]", path, i)
			child, ok := c.(map[string]any)
			if !ok {
				return Node{}, &InvalidError{Path: childPath, Reason: "a node must be an object"}
			}
			cn, err := parseNode(child, childPath, depth+1)
			if err != nil {
				return Node{}, err
			}
			n.Content = append(n.Content, cn)
		}
	}
	return n, nil
}

func parseMark(v any, path string) (Mark, error) {
	obj, ok := v.(map[string]any)
	if !ok {
		return Mark{}, &InvalidError{Path: path, Reason: "a mark must be an object"}
	}
	for _, key := range sortedKeys(obj) {
		if key != "type" && key != "attrs" {
			return Mark{}, &InvalidError{Path: path + "." + key, Reason: "not a field of a mark"}
		}
	}
	typ, _ := obj["type"].(string)
	rules, ok := marks[typ]
	if !ok {
		return Mark{}, &InvalidError{Path: path + ".type", Reason: fmt.Sprintf("mark type %q is not allowed", obj["type"])}
	}
	attrs, err := parseAttrs(obj["attrs"], rules, path+".attrs")
	if err != nil {
		return Mark{}, err
	}
	if typ == "link" {
		if _, ok := attrs["href"].(string); !ok {
			return Mark{}, &InvalidError{Path: path + ".attrs.href", Reason: "a link needs its href"}
		}
	}
	return Mark{Type: typ, Attrs: attrs}, nil
}

func parseAttrs(v any, rules map[string]attrRule, path string) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, &InvalidError{Path: path, Reason: "attrs must be an object"}
	}
	for _, key := range sortedKeys(obj) {
		value := obj[key]
		rule, ok := rules[key]
		if !ok {
			return nil, &InvalidError{Path: path + "." + key, Reason: fmt.Sprintf("attribute %q is not allowed here", key)}
		}
		if why := rule(value); why != "" {
			return nil, &InvalidError{Path: path + "." + key, Reason: why}
		}
	}
	if len(obj) == 0 {
		return nil, nil
	}
	return obj, nil
}

// LinkHrefAllowed reports whether href may be a link's target (K8): an
// https:, http: or mailto: URL, or an application path starting with a
// single "/". A protocol-relative "//host" is not a path, and no control
// character or space is allowed anywhere.
func LinkHrefAllowed(href string) bool {
	if href == "" || len(href) > 2048 {
		return false
	}
	for _, r := range href {
		if r <= ' ' || r == 0x7f || r == '\\' {
			return false
		}
	}
	if strings.HasPrefix(href, "/") {
		return !strings.HasPrefix(href, "//")
	}
	scheme, rest, ok := strings.Cut(href, ":")
	if !ok || rest == "" {
		return false
	}
	switch strings.ToLower(scheme) {
	case "https", "http":
		return strings.HasPrefix(rest, "//") && len(rest) > 2
	case "mailto":
		return true
	}
	return false
}

// Allowlist answers the node and mark types the allowlist accepts, each with
// its attributes' names sorted, for the test that compares it with the
// editor's exported schema (K8).
func Allowlist() (nodeAttrs, markAttrs map[string][]string) {
	nodeAttrs = map[string][]string{}
	for typ, spec := range nodes {
		nodeAttrs[typ] = sortedKeys(spec.attrs)
	}
	markAttrs = map[string][]string{}
	for typ, rules := range marks {
		markAttrs[typ] = sortedKeys(rules)
	}
	return nodeAttrs, markAttrs
}

// sortedKeys is m's keys in order, so the first refusal found is always the
// same one.
func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

func linkHref(v any) string {
	s, ok := v.(string)
	if !ok || !LinkHrefAllowed(s) {
		return "a link's href must be an https:, http: or mailto: URL, or a path starting with /"
	}
	return ""
}

func isNull(v any) string {
	if v != nil {
		return "must be null"
	}
	return ""
}

func isBool(v any) string {
	if _, ok := v.(bool); !ok {
		return "must be true or false"
	}
	return ""
}

func nullOr(rule attrRule) attrRule {
	return func(v any) string {
		if v == nil {
			return ""
		}
		return rule(v)
	}
}

func oneOf(values ...string) attrRule {
	return func(v any) string {
		s, _ := v.(string)
		for _, allowed := range values {
			if s == allowed {
				return ""
			}
		}
		quoted := make([]string, len(values))
		for i, allowed := range values {
			quoted[i] = strconv.Quote(allowed)
		}
		return "must be one of " + strings.Join(quoted, ", ")
	}
}

func intIn(lo, hi int64) attrRule {
	return func(v any) string {
		num, ok := v.(json.Number)
		if !ok {
			return "must be a whole number"
		}
		i, err := num.Int64()
		if err != nil || i < lo || i > hi {
			return fmt.Sprintf("must be a whole number from %d to %d", lo, hi)
		}
		return ""
	}
}

func matches(re *regexp.Regexp, what string) attrRule {
	return func(v any) string {
		s, ok := v.(string)
		if !ok || !re.MatchString(s) {
			return "must be " + what
		}
		return ""
	}
}

func stringUpTo(n int) attrRule {
	return func(v any) string {
		s, ok := v.(string)
		if !ok || len(s) > n {
			return fmt.Sprintf("must be text of at most %d bytes", n)
		}
		return ""
	}
}
