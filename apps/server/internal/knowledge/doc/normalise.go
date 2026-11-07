package doc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Normalise rewrites a validated document into its stored form, in place:
// an article link's label is dropped (null), since a label written by one
// reader could carry a title another may not see (K8, K21), and every id a
// node holds — an image's file, a file attachment's, a mention's person, an
// article link's target — is lower-case, so the ids compare as text.
func Normalise(n *Node) {
	if n == nil {
		return
	}
	lower := func(key string) {
		if s, ok := n.Attrs[key].(string); ok {
			n.Attrs[key] = strings.ToLower(s)
		}
	}
	switch n.Type {
	case "articleLink":
		lower("id")
		if _, ok := n.Attrs["label"]; ok {
			n.Attrs["label"] = nil
		}
	case "mention":
		lower("id")
	case "fileAttachment":
		lower("fileId")
	case "image":
		if src, ok := n.Attrs["src"].(string); ok {
			if id, ok := FileIDOfSrc(src); ok {
				n.Attrs["src"] = FilePathPrefix + id
			}
		}
	}
	for i := range n.Content {
		Normalise(&n.Content[i])
	}
}

// Encode is n as JSON, the form a document is stored in.
func Encode(n *Node) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(toJSON(n))
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func toJSON(n *Node) map[string]any {
	out := map[string]any{"type": n.Type}
	if len(n.Attrs) > 0 {
		out["attrs"] = n.Attrs
	}
	if n.Text != "" {
		out["text"] = n.Text
	}
	if len(n.Marks) > 0 {
		marks := make([]any, 0, len(n.Marks))
		for _, m := range n.Marks {
			mm := map[string]any{"type": m.Type}
			if len(m.Attrs) > 0 {
				mm["attrs"] = m.Attrs
			}
			marks = append(marks, mm)
		}
		out["marks"] = marks
	}
	if len(n.Content) > 0 {
		content := make([]any, 0, len(n.Content))
		for i := range n.Content {
			content = append(content, toJSON(&n.Content[i]))
		}
		out["content"] = content
	}
	return out
}

// Canonical validates raw and answers it in its stored form (Normalise),
// with the document.
func Canonical(raw []byte) ([]byte, *Node, error) {
	n, err := Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	Normalise(n)
	return Encode(n), n, nil
}

// pathStep is one step of an InvalidError's path.
var pathStep = regexp.MustCompile(`\.([A-Za-z]+)(?:\[([0-9]+)\])?`)

// Repair validates body, an imported document, dropping what the allowlist
// refuses — the attribute, the mark or the node its path names — one at a
// time, each with a warning, so one bad attribute never empties an article
// (K23). It answers the document in its stored form; ErrTooLarge stays an
// error.
func Repair(body map[string]any, warn func(string)) ([]byte, *Node, error) {
	for range 200 {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("knowledge/doc: encode a document: %w", err)
		}
		out, n, err := Canonical(raw)
		var bad *InvalidError
		if !errors.As(err, &bad) {
			return out, n, err
		}
		if !drop(body, bad.Path) {
			return nil, nil, err
		}
		if warn != nil {
			warn("Something the editor does not have was left out: " + bad.Reason + ".")
		}
	}
	return nil, nil, errors.New("knowledge/doc: the document could not be repaired")
}

// drop removes what path names from body: an attribute, a mark, or the
// node a refused type, field or text belongs to. It answers false when the
// path names nothing it can remove.
func drop(body map[string]any, path string) bool {
	steps := pathStep.FindAllStringSubmatch(strings.TrimPrefix(path, "$"), -1)
	cur := body
	var (
		listOwner map[string]any
		listKey   string
		listIndex = -1
	)
	dropItem := func() bool {
		if listOwner == nil {
			return false
		}
		list, _ := listOwner[listKey].([]any)
		listOwner[listKey] = append(list[:listIndex:listIndex], list[listIndex+1:]...)
		return true
	}
	for k, st := range steps {
		key := st[1]
		if st[2] != "" {
			list, ok := cur[key].([]any)
			i, _ := strconv.Atoi(st[2])
			if !ok || i >= len(list) {
				return false
			}
			next, ok := list[i].(map[string]any)
			listOwner, listKey, listIndex = cur, key, i
			if !ok {
				return dropItem()
			}
			cur = next
			continue
		}
		if key == "attrs" && k+1 < len(steps) {
			attrs, ok := cur["attrs"].(map[string]any)
			name := steps[k+1][1]
			if _, has := attrs[name]; !ok || !has {
				return dropItem()
			}
			delete(attrs, name)
			return true
		}
		if key == "attrs" || key == "content" || key == "marks" {
			if _, has := cur[key]; has && listOwner == nil {
				delete(cur, key)
				return true
			}
		}
		return dropItem()
	}
	return dropItem()
}
