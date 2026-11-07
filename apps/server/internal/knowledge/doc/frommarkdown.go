package doc

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Importer says how a Markdown file's references become a document's (K23).
// Each callback answers false when the reference is not carried over: an
// article link is then its label as text, an image its alt text.
type Importer struct {
	// Wiki resolves a [[wiki link]]'s target — the text before any "|" or
	// "#" — to an article id.
	Wiki func(target string) (id string, ok bool)
	// Relative resolves a relative link's destination, unescaped, to an
	// article id.
	Relative func(dest string) (id string, ok bool)
	// Image resolves an image's destination, unescaped, or an embed's
	// ![[target]], to a file id.
	Image func(dest string) (id string, ok bool)
	// Warn records something that was not carried over.
	Warn func(message string)
}

// FromMarkdown reads Markdown — CommonMark with GFM's tables, task lists and
// strikethrough, Obsidian's callouts and [[wiki links]] — into a document of
// the allowlisted nodes (K23). It answers the document's JSON object; the
// caller validates it with Parse. Headings deeper than 3 become level 3, HTML
// becomes its text, and a "> [!kind]" callout becomes a callout — a folded
// one ("> [!kind]-" or "+") a toggle.
func FromMarkdown(src []byte, im Importer) map[string]any {
	r := mdReader{im: im}
	src = r.placeholders(src)
	r.src = src
	root := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(src))
	content := r.blocks(root)
	if len(content) == 0 {
		content = []any{node("paragraph")}
	}
	return map[string]any{"type": "doc", "content": content}
}

// wikiPattern is a wiki link or an embed, [[target|alias]] or ![[target]].
var wikiPattern = regexp.MustCompile(`(!?)\[\[([^\[\]\n]{1,500}?)\]\]`)

// placeholderPattern is what placeholders replaces a wiki link with.
var placeholderPattern = regexp.MustCompile("([0-9]+)")

// calloutPattern is the first line of an Obsidian callout.
var calloutPattern = regexp.MustCompile(`^\[!([A-Za-z-]{1,32})\]([+-]?)[ \t]*(.*)$`)

type wikiRef struct {
	original string
	embed    bool
	target   string
	label    string
}

type mdReader struct {
	src    []byte
	im     Importer
	wikis  []wikiRef
	warned map[string]bool
}

func (r *mdReader) warn(msg string) {
	if r.im.Warn == nil {
		return
	}
	if r.warned == nil {
		r.warned = map[string]bool{}
	}
	if !r.warned[msg] {
		r.warned[msg] = true
		r.im.Warn(msg)
	}
}

// placeholders replaces every wiki link with a placeholder no Markdown
// syntax touches, so CommonMark's own link parsing cannot split it; the text
// and the code that hold one put it back.
func (r *mdReader) placeholders(src []byte) []byte {
	return wikiPattern.ReplaceAllFunc(src, func(m []byte) []byte {
		sub := wikiPattern.FindSubmatch(m)
		inner := string(sub[2])
		target, label := inner, ""
		if t, l, ok := strings.Cut(inner, "|"); ok {
			target, label = t, l
		}
		if t, _, ok := strings.Cut(target, "#"); ok {
			target = t
		}
		target = strings.TrimSpace(target)
		if label == "" {
			label = target
		}
		r.wikis = append(r.wikis, wikiRef{original: string(m), embed: len(sub[1]) == 1, target: target, label: strings.TrimSpace(label)})
		return fmt.Appendf(nil, "%d", len(r.wikis)-1)
	})
}

// restore puts the wiki links back into code.
func (r *mdReader) restore(s string) string {
	return placeholderPattern.ReplaceAllStringFunc(s, func(m string) string {
		i, err := strconv.Atoi(placeholderPattern.FindStringSubmatch(m)[1])
		if err != nil || i >= len(r.wikis) {
			return m
		}
		return r.wikis[i].original
	})
}

func node(typ string) map[string]any { return map[string]any{"type": typ} }

func withContent(n map[string]any, content []any) map[string]any {
	if len(content) > 0 {
		n["content"] = content
	}
	return n
}

// blocks reads a node's block children.
func (r *mdReader) blocks(parent ast.Node) []any {
	var out []any
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		out = append(out, r.block(c)...)
	}
	return out
}

func (r *mdReader) block(n ast.Node) []any {
	switch b := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return r.paragraphs(r.inlines(n, nil))
	case *ast.Heading:
		level := min(max(b.Level, 1), 3)
		h := node("heading")
		h["attrs"] = map[string]any{"level": level}
		return []any{withContent(h, r.flat(r.inlines(n, nil)))}
	case *ast.ThematicBreak:
		return []any{node("horizontalRule")}
	case *ast.FencedCodeBlock:
		lang := strings.ToLower(string(b.Language(r.src)))
		return []any{r.code(n, lang)}
	case *ast.CodeBlock:
		return []any{r.code(n, "")}
	case *ast.Blockquote:
		return []any{r.quote(b)}
	case *ast.List:
		return []any{r.list(b)}
	case *ast.HTMLBlock:
		r.warn("HTML was imported as its text.")
		var sb strings.Builder
		for i := 0; i < b.Lines().Len(); i++ {
			line := b.Lines().At(i)
			sb.Write(line.Value(r.src))
		}
		s := strings.TrimSpace(r.restore(sb.String()))
		if s == "" {
			return nil
		}
		return []any{withContent(node("paragraph"), []any{textNode(s, nil)})}
	case *east.Table:
		return []any{r.table(b)}
	}
	return r.blocks(n)
}

func (r *mdReader) code(n ast.Node, lang string) map[string]any {
	var sb strings.Builder
	for i := 0; i < n.Lines().Len(); i++ {
		line := n.Lines().At(i)
		sb.Write(line.Value(r.src))
	}
	cb := node("codeBlock")
	if lang != "" && languagePattern.MatchString(lang) {
		cb["attrs"] = map[string]any{"language": lang}
	} else {
		cb["attrs"] = map[string]any{"language": nil}
	}
	code := strings.TrimSuffix(r.restore(sb.String()), "\n")
	if code != "" {
		cb["content"] = []any{textNode(code, nil)}
	}
	return cb
}

// quote reads a blockquote: an Obsidian callout when its first line is
// "[!kind]", a folded one a toggle.
func (r *mdReader) quote(q *ast.Blockquote) map[string]any {
	first, ok := q.FirstChild().(*ast.Paragraph)
	if ok && first.Lines().Len() > 0 {
		line := first.Lines().At(0)
		if m := calloutPattern.FindStringSubmatch(strings.TrimRight(string(line.Value(r.src)), "\r\n")); m != nil {
			kind := calloutKind(strings.ToLower(m[1]))
			title := strings.TrimSpace(html.UnescapeString(string(util.UnescapePunctuations([]byte(m[3])))))
			// The first paragraph's own content is what follows its first line.
			rest := r.afterFirstLine(r.inlines(first, nil))
			var body []any
			body = append(body, r.paragraphs(rest)...)
			for c := first.NextSibling(); c != nil; c = c.NextSibling() {
				body = append(body, r.block(c)...)
			}
			if len(body) == 0 {
				body = []any{node("paragraph")}
			}
			if m[2] != "" {
				d := node("details")
				d["attrs"] = map[string]any{"open": m[2] == "+"}
				summary := node("detailsSummary")
				if title != "" {
					summary["content"] = []any{textNode(r.restore(title), nil)}
				}
				d["content"] = []any{summary, withContent(node("detailsContent"), body)}
				return d
			}
			c := node("callout")
			var emoji any
			if title != "" && isEmojiOnly(title) {
				emoji = title
			} else if title != "" {
				bold := textNode(r.restore(title), []any{map[string]any{"type": "bold"}})
				body = append([]any{withContent(node("paragraph"), []any{bold})}, body...)
			}
			c["attrs"] = map[string]any{"kind": kind, "emoji": emoji}
			c["content"] = body
			return c
		}
	}
	content := r.blocks(q)
	if len(content) == 0 {
		content = []any{node("paragraph")}
	}
	return withContent(node("blockquote"), content)
}

// calloutKind maps Obsidian's callout types onto the four kinds (K18).
func calloutKind(t string) string {
	switch t {
	case "tip", "hint", "important", "success", "check", "done":
		return "tip"
	case "warning", "caution", "attention", "question", "help", "faq":
		return "warning"
	case "danger", "error", "failure", "fail", "missing", "bug":
		return "danger"
	}
	return "info"
}

// isEmojiOnly reports whether s is a few symbols and no letters or digits.
func isEmojiOnly(s string) bool {
	if len(s) > 32 {
		return false
	}
	for _, c := range s {
		if c < 0x2000 || c == ' ' {
			return false
		}
	}
	return true
}

// afterFirstLine is inline content after its first line break.
func (r *mdReader) afterFirstLine(list []any) []any {
	for i, c := range list {
		if m, ok := c.(map[string]any); ok && (m["type"] == softBreak || m["type"] == "hardBreak") {
			return list[i+1:]
		}
	}
	return nil
}

func (r *mdReader) list(l *ast.List) map[string]any {
	task := false
	if item := l.FirstChild(); item != nil && item.FirstChild() != nil {
		if _, ok := item.FirstChild().FirstChild().(*east.TaskCheckBox); ok {
			task = true
		}
	}
	var out map[string]any
	switch {
	case task:
		out = node("taskList")
	case l.IsOrdered():
		out = node("orderedList")
		out["attrs"] = map[string]any{"start": min(max(l.Start, -1_000_000), 1_000_000), "type": nil}
	default:
		out = node("bulletList")
	}
	var items []any
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		var it map[string]any
		checked := false
		if first := item.FirstChild(); first != nil {
			if cb, ok := first.FirstChild().(*east.TaskCheckBox); ok {
				checked = cb.IsChecked
			}
		}
		if task {
			it = node("taskItem")
			it["attrs"] = map[string]any{"checked": checked}
		} else {
			it = node("listItem")
		}
		content := r.blocks(item)
		if len(content) == 0 || content[0].(map[string]any)["type"] != "paragraph" {
			content = append([]any{node("paragraph")}, content...)
		}
		it["content"] = content
		items = append(items, it)
	}
	out["content"] = items
	return out
}

func (r *mdReader) table(t *east.Table) map[string]any {
	var rows []any
	for row := t.FirstChild(); row != nil; row = row.NextSibling() {
		cellType := "tableCell"
		if _, ok := row.(*east.TableHeader); ok {
			cellType = "tableHeader"
		}
		var cells []any
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			c := node(cellType)
			c["attrs"] = map[string]any{"colspan": 1, "rowspan": 1, "colwidth": nil}
			c["content"] = []any{withContent(node("paragraph"), r.flat(r.inlines(cell, nil)))}
			cells = append(cells, c)
		}
		rows = append(rows, withContent(node("tableRow"), cells))
	}
	return withContent(node("table"), rows)
}

// softBreak marks a soft line break in inline content until flat turns it
// into a space.
const softBreak = "\x00soft"

// inlines reads n's inline children under marks. An image is a block node in
// the list; paragraphs splits a paragraph around it.
func (r *mdReader) inlines(n ast.Node, marks []any) []any {
	var out []any
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Text:
			s := string(v.Segment.Value(r.src))
			if !v.IsRaw() {
				s = html.UnescapeString(string(util.UnescapePunctuations([]byte(s))))
			}
			out = append(out, r.text(s, marks)...)
			if v.HardLineBreak() {
				out = append(out, node("hardBreak"))
			} else if v.SoftLineBreak() {
				out = append(out, node(softBreak))
			}
		case *ast.String:
			out = append(out, r.text(html.UnescapeString(string(v.Value)), marks)...)
		case *ast.CodeSpan:
			var sb strings.Builder
			for t := v.FirstChild(); t != nil; t = t.NextSibling() {
				if tn, ok := t.(*ast.Text); ok {
					sb.Write(tn.Segment.Value(r.src))
				} else if st, ok := t.(*ast.String); ok {
					sb.Write(st.Value)
				}
			}
			if s := r.restore(sb.String()); s != "" {
				out = append(out, textNode(s, []any{map[string]any{"type": "code"}}))
			}
		case *ast.Emphasis:
			m := "italic"
			if v.Level >= 2 {
				m = "bold"
			}
			out = append(out, r.inlines(v, addMark(marks, map[string]any{"type": m}))...)
		case *east.Strikethrough:
			out = append(out, r.inlines(v, addMark(marks, map[string]any{"type": "strike"}))...)
		case *ast.Link:
			out = append(out, r.link(v, string(v.Destination), marks)...)
		case *ast.AutoLink:
			dest := string(v.URL(r.src))
			if v.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(dest), "mailto:") {
				dest = "mailto:" + dest
			} else if !strings.Contains(dest, "://") && !strings.HasPrefix(strings.ToLower(dest), "mailto:") {
				dest = "https://" + dest
			}
			label := string(v.Label(r.src))
			if LinkHrefAllowed(dest) {
				out = append(out, textNode(label, addMark(marks, linkMark(dest))))
			} else {
				out = append(out, textNode(label, marks))
			}
		case *ast.Image:
			out = append(out, r.image(v)...)
		case *ast.RawHTML:
			r.warn("HTML was imported as its text.")
		case *east.TaskCheckBox:
			// The list reads it.
		default:
			out = append(out, r.inlines(c, marks)...)
		}
	}
	return out
}

func addMark(marks []any, m map[string]any) []any {
	out := make([]any, 0, len(marks)+1)
	out = append(out, marks...)
	return append(out, m)
}

func linkMark(href string) map[string]any {
	return map[string]any{"type": "link", "attrs": map[string]any{"href": href}}
}

func textNode(s string, marks []any) map[string]any {
	t := map[string]any{"type": "text", "text": s}
	if len(marks) > 0 {
		t["marks"] = marks
	}
	return t
}

// text is s with its wiki links: an article link, an embedded image, or the
// label as text.
func (r *mdReader) text(s string, marks []any) []any {
	var out []any
	last := 0
	for _, loc := range placeholderPattern.FindAllStringSubmatchIndex(s, -1) {
		if loc[0] > last {
			out = append(out, textNode(s[last:loc[0]], marks))
		}
		last = loc[1]
		i, _ := strconv.Atoi(s[loc[2]:loc[3]])
		if i >= len(r.wikis) {
			continue
		}
		w := r.wikis[i]
		if w.embed {
			// Resolved where it lands: an image only where a block can be.
			out = append(out, map[string]any{"type": pendingImage, "dest": w.target, "alt": w.label, "embed": true, "marks": marks})
			continue
		}
		out = append(out, r.wikiLink(w.target, w.label, marks)...)
	}
	if last < len(s) {
		out = append(out, textNode(s[last:], marks))
	}
	return out
}

// pendingImage is an image not yet resolved: paragraphs makes it an image
// block, flat — in a heading or a table cell — its text, so an image that
// ends up as text is never uploaded.
const pendingImage = "\x00image"

// wikiLink is a wiki link to an imported page as an article link — its
// label never stored (K21) — or its label as text.
func (r *mdReader) wikiLink(target, label string, marks []any) []any {
	if r.im.Wiki != nil {
		if id, ok := r.im.Wiki(target); ok {
			return []any{articleLinkNode(id)}
		}
	}
	return []any{textNode(label, marks)}
}

func articleLinkNode(id string) map[string]any {
	return map[string]any{"type": "articleLink", "attrs": map[string]any{"id": id, "label": nil}}
}

// clampRunes is s cut to at most n characters.
func clampRunes(s string, n int) string {
	if runes := []rune(s); len(runes) > n {
		return string(runes[:n])
	}
	return s
}

// asText is a pending image as text: an embed of a page an article link,
// anything else its alt text.
func (r *mdReader) asText(m map[string]any) []any {
	alt, _ := m["alt"].(string)
	if embed, _ := m["embed"].(bool); embed {
		dest, _ := m["dest"].(string)
		if r.im.Wiki != nil {
			if id, ok := r.im.Wiki(dest); ok {
				return []any{articleLinkNode(id)}
			}
		}
		return []any{textNode(alt, asList(m["marks"]))}
	}
	if alt == "" {
		return nil
	}
	return []any{textNode(alt, nil)}
}

// resolveImage is a pending image as an image block, when it is one of the
// import's files, else as text.
func (r *mdReader) resolveImage(m map[string]any) (map[string]any, []any) {
	dest, _ := m["dest"].(string)
	alt, _ := m["alt"].(string)
	if r.im.Image != nil {
		if id, ok := r.im.Image(dest); ok {
			return map[string]any{"type": "image", "attrs": map[string]any{"src": FilePathPrefix + id, "alt": clampRunes(alt, 1000), "title": nil, "width": nil}}, nil
		}
	}
	text := r.asText(m)
	if embed, _ := m["embed"].(bool); embed {
		if len(text) == 0 || text[0].(map[string]any)["type"] == "text" {
			r.warn("An embedded file or page that is not in the import was left as its name.")
		}
	} else {
		r.warn("An image that is not in the import was left as its alt text.")
	}
	return nil, text
}

// link reads a link: an https:, http: or mailto: URL or an application path
// as a link mark; a relative link to an imported page as an article link;
// anything else as its text.
func (r *mdReader) link(l *ast.Link, dest string, marks []any) []any {
	label := r.inlines(l, marks)
	lower := strings.ToLower(dest)
	switch {
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"), strings.HasPrefix(lower, "mailto:"), strings.HasPrefix(dest, "/"):
		if LinkHrefAllowed(dest) {
			return r.inlines(l, addMark(marks, linkMark(dest)))
		}
	case !strings.Contains(dest, ":") && r.im.Relative != nil:
		unescaped, err := url.PathUnescape(dest)
		if err != nil {
			unescaped = dest
		}
		if id, ok := r.im.Relative(unescaped); ok {
			return []any{articleLinkNode(id)}
		}
	}
	return label
}

// image reads an image: one of the import's files as an image node, any
// other as its alt text.
func (r *mdReader) image(img *ast.Image) []any {
	alt := plainOf(r.inlines(img, nil))
	dest := string(img.Destination)
	if !strings.Contains(dest, ":") {
		unescaped, err := url.PathUnescape(dest)
		if err != nil {
			unescaped = dest
		}
		return []any{map[string]any{"type": pendingImage, "dest": unescaped, "alt": alt}}
	}
	r.warn("An image that is not in the import was left as its alt text.")
	if alt == "" {
		return nil
	}
	return []any{textNode(alt, nil)}
}

// plainOf is inline content's text.
func plainOf(list []any) string {
	var b strings.Builder
	for _, c := range list {
		m, _ := c.(map[string]any)
		switch m["type"] {
		case "text":
			b.WriteString(m["text"].(string))
		case softBreak, "hardBreak":
			b.WriteByte(' ')
		case "mention":
			if l, ok := m["attrs"].(map[string]any)["label"].(string); ok {
				b.WriteString(l)
			}
		case pendingImage:
			if alt, ok := m["alt"].(string); ok {
				b.WriteString(alt)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// paragraphs splits inline content into paragraphs around its images, which
// are blocks.
func (r *mdReader) paragraphs(list []any) []any {
	var out, run []any
	flush := func() {
		if flat := r.flat(run); len(flat) > 0 {
			out = append(out, withContent(node("paragraph"), flat))
		}
		run = nil
	}
	for _, c := range list {
		if m, ok := c.(map[string]any); ok && m["type"] == pendingImage {
			img, text := r.resolveImage(m)
			if img == nil {
				run = append(run, text...)
				continue
			}
			flush()
			out = append(out, img)
			continue
		}
		run = append(run, c)
	}
	flush()
	return out
}

// flat is inline content as stored: a soft break a space, an image its alt
// text, neighbouring texts with the same marks one, empty texts and leading
// and trailing breaks gone.
func (r *mdReader) flat(list []any) []any {
	var out []any
	for _, c := range list {
		m := c.(map[string]any)
		switch m["type"] {
		case softBreak:
			m = textNode(" ", nil)
			if len(out) > 0 {
				if prev := out[len(out)-1].(map[string]any); prev["type"] == "text" {
					m = textNode(" ", asList(prev["marks"]))
				}
			}
		case pendingImage:
			text := r.asText(m)
			if len(text) == 0 {
				continue
			}
			m = text[0].(map[string]any)
		}
		if m["type"] == "text" {
			if m["text"] == "" {
				continue
			}
			if len(out) > 0 {
				prev := out[len(out)-1].(map[string]any)
				if prev["type"] == "text" && fmt.Sprint(prev["marks"]) == fmt.Sprint(m["marks"]) {
					prev["text"] = prev["text"].(string) + m["text"].(string)
					continue
				}
			}
			m = textNode(m["text"].(string), asList(m["marks"]))
		}
		out = append(out, m)
	}
	for len(out) > 0 && out[len(out)-1].(map[string]any)["type"] == "hardBreak" {
		out = out[:len(out)-1]
	}
	if len(out) > 0 {
		if first := out[0].(map[string]any); first["type"] == "text" {
			first["text"] = strings.TrimLeft(first["text"].(string), " \t")
			if first["text"] == "" {
				out = out[1:]
			}
		}
	}
	if len(out) > 0 {
		if last := out[len(out)-1].(map[string]any); last["type"] == "text" {
			last["text"] = strings.TrimRight(last["text"].(string), " \t")
			if last["text"] == "" {
				out = out[:len(out)-1]
			}
		}
	}
	return out
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}
