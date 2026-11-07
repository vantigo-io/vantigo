package doc

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Text derives an article's plain text from its document (K8): what search
// indexes as content_text and the Assistant reads in pieces (K14). Blocks are
// separated by a blank line and written Markdown-ish, so the structure
// survives: "# " before a heading, "- ", "1. " and "- [x] " before list
// items, "> " before a quote, fences around a code block, "---" for a
// divider. Marks add nothing: a link is only its own text, never its target,
// so a title the reader may not see cannot reach the text.
func Text(n *Node) string {
	if n == nil {
		return ""
	}
	var blocks []string
	for i := range n.Content {
		if s := block(&n.Content[i], 0); s != "" {
			blocks = append(blocks, s)
		}
	}
	return strings.Join(blocks, "\n\n")
}

// block renders one block node; depth is its list nesting.
func block(n *Node, depth int) string {
	switch n.Type {
	case "paragraph":
		return inline(n)
	case "heading":
		level := 1
		if v, ok := n.Attrs["level"].(json.Number); ok {
			if i, err := v.Int64(); err == nil && i >= 1 && i <= 6 {
				level = int(i)
			}
		}
		text := inline(n)
		if text == "" {
			return ""
		}
		return strings.Repeat("#", level) + " " + text
	case "bulletList", "orderedList", "taskList":
		return list(n, depth)
	case "blockquote":
		inner := children(n, depth)
		if inner == "" {
			return ""
		}
		lines := strings.Split(inner, "\n")
		for i, l := range lines {
			if l == "" {
				lines[i] = ">"
			} else {
				lines[i] = "> " + l
			}
		}
		return strings.Join(lines, "\n")
	case "codeBlock":
		lang, _ := n.Attrs["language"].(string)
		return "```" + lang + "\n" + inline(n) + "\n```"
	case "horizontalRule":
		return "---"
	case "listItem", "taskItem":
		return children(n, depth)
	}
	return inline(n)
}

// children renders n's block children, separated by a blank line.
func children(n *Node, depth int) string {
	var parts []string
	for i := range n.Content {
		if s := block(&n.Content[i], depth); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// list renders a list, one item to a line, a nested list indented under its
// item by two spaces.
func list(n *Node, depth int) string {
	number := int64(1)
	if v, ok := n.Attrs["start"].(json.Number); ok {
		if i, err := v.Int64(); err == nil {
			number = i
		}
	}
	indent := strings.Repeat("  ", depth)
	var lines []string
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
		var first []string
		var nested []string
		for j := range item.Content {
			child := &item.Content[j]
			switch child.Type {
			case "bulletList", "orderedList", "taskList":
				if s := list(child, depth+1); s != "" {
					nested = append(nested, s)
				}
			default:
				if s := block(child, depth); s != "" {
					first = append(first, strings.ReplaceAll(s, "\n", "\n"+indent+"  "))
				}
			}
		}
		lines = append(lines, indent+marker+strings.Join(first, " "))
		lines = append(lines, nested...)
	}
	return strings.Join(lines, "\n")
}

// inline renders n's inline content: its text, a hard break as a newline.
func inline(n *Node) string {
	var b strings.Builder
	var walk func(*Node)
	walk = func(c *Node) {
		switch c.Type {
		case "text":
			b.WriteString(c.Text)
		case "hardBreak":
			b.WriteByte('\n')
		default:
			for i := range c.Content {
				walk(&c.Content[i])
			}
		}
	}
	for i := range n.Content {
		walk(&n.Content[i])
	}
	return b.String()
}
