package doc

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Exporter says how a document's references are written as Markdown (K23).
type Exporter struct {
	// Article answers the title an article link is written with, as
	// [[Title]], and false when the reader may not read the target: the link
	// is then NoAccess, never a stored label (K21).
	Article func(id string) (title string, ok bool)
	// File answers the relative path an image or a file is written with, and
	// false when it is not exported: an image is then its alt text, a file
	// its name.
	File func(id string) (path string, ok bool)
}

// Markdown writes n as Markdown (K23): CommonMark with GFM's tables, task
// lists and strikethrough, and Obsidian's callouts — a callout as
// "> [!kind]", a toggle as a folded "> [!note]-" callout whose title is its
// summary. Images and files are relative links the Exporter names, article
// links [[Title]], mentions @Name, record links Markdown links. The table of
// contents and underline have no Markdown and are left out.
func Markdown(n *Node, e Exporter) string {
	if n == nil {
		return ""
	}
	w := mdWriter{e: e}
	out := w.blocks(n.Content)
	if out == "" {
		return ""
	}
	return out + "\n"
}

type mdWriter struct{ e Exporter }

// NoAccess is what an export writes for a link to an article its reader may
// not read.
const NoAccess = "No access"

// blocks writes a run of blocks, separated by a blank line.
func (w mdWriter) blocks(list []Node) string {
	var parts []string
	for i := range list {
		if s := w.block(&list[i]); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

func (w mdWriter) block(n *Node) string {
	switch n.Type {
	case "paragraph":
		return w.inline(n.Content, false)
	case "heading":
		level := 1
		if v, ok := n.Attrs["level"].(json.Number); ok {
			if i, err := v.Int64(); err == nil && i >= 1 && i <= 6 {
				level = int(i)
			}
		}
		text := w.inline(n.Content, false)
		if text == "" {
			return ""
		}
		return strings.Repeat("#", level) + " " + strings.ReplaceAll(text, "\n", " ")
	case "bulletList", "orderedList", "taskList":
		return w.list(n)
	case "blockquote":
		return quote(w.blocks(n.Content))
	case "codeBlock":
		lang, _ := n.Attrs["language"].(string)
		code := plain(n)
		fence := "```"
		for strings.Contains(code, fence) {
			fence += "`"
		}
		return fence + lang + "\n" + code + "\n" + fence
	case "horizontalRule":
		return "---"
	case "callout":
		kind, _ := n.Attrs["kind"].(string)
		head := "[!" + kind + "]"
		if emoji, _ := n.Attrs["emoji"].(string); emoji != "" {
			head += " " + emoji
		}
		body := w.blocks(n.Content)
		if body == "" {
			return quote(head)
		}
		return quote(head + "\n" + body)
	case "details":
		head := "[!note]-"
		if open, _ := n.Attrs["open"].(bool); open {
			head = "[!note]+"
		}
		var body string
		for i := range n.Content {
			c := &n.Content[i]
			switch c.Type {
			case "detailsSummary":
				if s := strings.ReplaceAll(w.inline(c.Content, false), "\n", " "); s != "" {
					head += " " + s
				}
			case "detailsContent":
				body = w.blocks(c.Content)
			}
		}
		if body == "" {
			return quote(head)
		}
		return quote(head + "\n" + body)
	case "table":
		return w.table(n)
	case "image":
		alt, _ := n.Attrs["alt"].(string)
		src, _ := n.Attrs["src"].(string)
		id, _ := FileIDOfSrc(src)
		if path, ok := w.file(id); ok {
			return "![" + escape(alt) + "](" + destination(path) + ")"
		}
		return escape(alt)
	case "fileAttachment":
		id, _ := n.Attrs["fileId"].(string)
		name, _ := n.Attrs["filename"].(string)
		if name == "" {
			name = "file"
		}
		if path, ok := w.file(id); ok {
			return "[" + escape(name) + "](" + destination(path) + ")"
		}
		return escape(name)
	case "tableOfContents":
		return ""
	}
	return w.inline(n.Content, false)
}

func (w mdWriter) file(id string) (string, bool) {
	if w.e.File == nil || id == "" {
		return "", false
	}
	return w.e.File(id)
}

// list writes a list, a nested block indented under its item.
func (w mdWriter) list(n *Node) string {
	number := int64(1)
	if v, ok := n.Attrs["start"].(json.Number); ok {
		if i, err := v.Int64(); err == nil {
			number = i
		}
	}
	var items []string
	for i := range n.Content {
		item := &n.Content[i]
		var marker string
		switch {
		case n.Type == "orderedList":
			marker = strconv.FormatInt(number, 10) + ". "
			number++
		case item.Type == "taskItem":
			if checked, _ := item.Attrs["checked"].(bool); checked {
				marker = "- [x] "
			} else {
				marker = "- [ ] "
			}
		default:
			marker = "- "
		}
		indent := strings.Repeat(" ", len(marker))
		if item.Type == "taskItem" {
			indent = "  "
		}
		var parts []string
		for j := range item.Content {
			child := &item.Content[j]
			s := w.block(child)
			if s == "" {
				continue
			}
			// A nested list follows its item's first line directly.
			if len(parts) > 0 && (child.Type == "bulletList" || child.Type == "orderedList" || child.Type == "taskList") {
				parts[len(parts)-1] += "\n" + s
				continue
			}
			parts = append(parts, s)
		}
		body := indentLines(strings.Join(parts, "\n\n"), indent)
		items = append(items, marker+strings.TrimPrefix(body, indent))
	}
	return strings.Join(items, "\n")
}

// table writes a GFM table, its first row the header.
func (w mdWriter) table(n *Node) string {
	var rows [][]string
	width := 0
	for i := range n.Content {
		var cells []string
		for j := range n.Content[i].Content {
			cell := &n.Content[i].Content[j]
			var parts []string
			for k := range cell.Content {
				if s := w.inline(cell.Content[k].Content, true); s != "" {
					parts = append(parts, s)
				}
			}
			cells = append(cells, strings.Join(parts, "<br>"))
		}
		width = max(width, len(cells))
		rows = append(rows, cells)
	}
	if len(rows) == 0 || width == 0 {
		return ""
	}
	line := func(cells []string) string {
		for len(cells) < width {
			cells = append(cells, "")
		}
		return "| " + strings.Join(cells, " | ") + " |"
	}
	sep := make([]string, width)
	for i := range sep {
		sep[i] = "---"
	}
	out := []string{line(rows[0]), line(sep)}
	for _, r := range rows[1:] {
		out = append(out, line(r))
	}
	return strings.Join(out, "\n")
}

// inline writes inline content; inTable escapes the pipe and writes a hard
// break as <br>.
func (w mdWriter) inline(list []Node, inTable bool) string {
	var b strings.Builder
	for i := 0; i < len(list); i++ {
		c := &list[i]
		switch c.Type {
		case "text":
			// Neighbours with the same marks are written as one run.
			text := c.Text
			for i+1 < len(list) && list[i+1].Type == "text" && sameMarks(list[i+1].Marks, c.Marks) {
				i++
				text += list[i].Text
			}
			b.WriteString(marked(text, c.Marks, inTable))
		case "hardBreak":
			if inTable {
				b.WriteString("<br>")
			} else {
				b.WriteString("\\\n")
			}
		case "mention":
			label, _ := c.Attrs["label"].(string)
			if label != "" {
				b.WriteString("@" + escapeText(label, inTable))
			}
		case "articleLink":
			id, _ := c.Attrs["id"].(string)
			if w.e.Article != nil {
				if title, ok := w.e.Article(id); ok {
					b.WriteString("[[" + wikiTarget(title) + "]]")
					continue
				}
			}
			b.WriteString(NoAccess)
		case "recordLink":
			path, _ := c.Attrs["path"].(string)
			label, _ := c.Attrs["label"].(string)
			if label == "" {
				label = path
			}
			b.WriteString("[" + escapeText(label, inTable) + "](" + destination(path) + ")")
		default:
			b.WriteString(w.inline(c.Content, inTable))
		}
	}
	return b.String()
}

func sameMarks(a, b []Mark) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type {
			return false
		}
		ah, _ := a[i].Attrs["href"].(string)
		bh, _ := b[i].Attrs["href"].(string)
		if ah != bh {
			return false
		}
	}
	return true
}

// marked writes text with its marks: a code mark as a code span, the others
// around the escaped text, a link outermost. Leading and trailing spaces stay
// outside the delimiters, where CommonMark needs them.
func marked(text string, marks []Mark, inTable bool) string {
	var code, bold, italic, strike bool
	href := ""
	for _, m := range marks {
		switch m.Type {
		case "code":
			code = true
		case "bold":
			bold = true
		case "italic":
			italic = true
		case "strike":
			strike = true
		case "link":
			href, _ = m.Attrs["href"].(string)
		}
	}
	core := strings.TrimSpace(text)
	if core == "" {
		return escapeText(text, inTable)
	}
	lead := text[:strings.Index(text, core)]
	trail := text[len(lead)+len(core):]
	var s string
	if code {
		s = codeSpan(core)
		if inTable {
			s = strings.ReplaceAll(s, "|", "\\|")
		}
	} else {
		s = escapeText(core, inTable)
	}
	if strike {
		s = "~~" + s + "~~"
	}
	if italic {
		s = "*" + s + "*"
	}
	if bold {
		s = "**" + s + "**"
	}
	if href != "" {
		s = "[" + s + "](" + destination(href) + ")"
	}
	return escapeText(lead, inTable) + s + escapeText(trail, inTable)
}

// codeSpan is a code span around s, its backtick run longer than any in s.
func codeSpan(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

// escape is escapeText outside a table.
func escape(s string) string { return escapeText(s, false) }

// escapeText backslash-escapes what CommonMark would read as markup: the
// inline punctuation anywhere, and what opens a block at a line's start.
func escapeText(s string, inTable bool) string {
	var b strings.Builder
	lineStart := true
	for i, r := range s {
		switch r {
		case '\\', '`', '*', '_', '[', ']', '<', '>', '~', '!':
			if r == '!' && (i+1 >= len(s) || s[i+1] != '[') {
				b.WriteRune(r)
				break
			}
			b.WriteByte('\\')
			b.WriteRune(r)
		case '|':
			if inTable {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		case '#', '-', '+', '=':
			if lineStart {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		case '&':
			b.WriteString("&amp;")
		case '\n':
			if inTable {
				b.WriteByte(' ')
			} else {
				b.WriteRune(r)
			}
		default:
			if lineStart && r >= '0' && r <= '9' {
				// "1." at a line's start would open a list.
				rest := s[i:]
				j := 0
				for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
					j++
				}
				if j < len(rest) && (rest[j] == '.' || rest[j] == ')') {
					b.WriteString(rest[:j])
					b.WriteByte('\\')
					// The digits are written; skip them in the loop below by
					// writing the rest of this run here.
					return b.String() + escapeText(rest[j:], inTable)
				}
			}
			b.WriteRune(r)
		}
		lineStart = r == '\n'
	}
	return b.String()
}

// destination is a link destination, in angle brackets when it holds a
// space or a parenthesis.
func destination(s string) string {
	if strings.ContainsAny(s, " ()<>") {
		return "<" + strings.NewReplacer("<", "%3C", ">", "%3E").Replace(s) + ">"
	}
	return s
}

// wikiTarget is a title as a wiki link's target: without what would end or
// split the link.
func wikiTarget(title string) string {
	t := strings.NewReplacer("[", "(", "]", ")", "|", "-", "#", "", "^", "", "\n", " ").Replace(title)
	if strings.TrimSpace(t) == "" {
		return "Untitled"
	}
	return t
}

// plain is a node's text with nothing escaped: a code block's code.
func plain(n *Node) string {
	var b strings.Builder
	for i := range n.Content {
		c := &n.Content[i]
		switch c.Type {
		case "text":
			b.WriteString(c.Text)
		case "hardBreak":
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// quote prefixes every line with "> ".
func quote(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + l
		}
	}
	return strings.Join(lines, "\n")
}

// indentLines indents every line but empty ones.
func indentLines(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = indent + l
		}
	}
	return strings.Join(lines, "\n")
}
