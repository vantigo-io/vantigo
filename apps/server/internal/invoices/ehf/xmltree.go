package ehf

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// The three namespaces of a UBL 2.1 document.
const (
	nsInvoice    = "urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
	nsCreditNote = "urn:oasis:names:specification:ubl:schema:xsd:CreditNote-2"
	nsCAC        = "urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2"
	nsCBC        = "urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2"
)

// Node is an element of a parsed document, its names resolved to their
// namespaces by encoding/xml's decoder — what the pre-check, the invariants
// and the tests read, so nothing ever matches on a prefix or a substring.
type Node struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Children []*Node
	Text     string // the element's own character data, trimmed
}

// Parse reads a document into its element tree.
func Parse(doc []byte) (*Node, error) {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var stack []*Node
	var root *Node
	var text []*strings.Builder
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("ehf: parse the document: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &Node{Name: t.Name, Attrs: append([]xml.Attr(nil), t.Attr...)}
			if len(stack) == 0 {
				if root != nil {
					return nil, errors.New("ehf: parse the document: more than one root element")
				}
				root = n
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, n)
			}
			stack = append(stack, n)
			text = append(text, &strings.Builder{})
		case xml.CharData:
			if len(text) > 0 {
				text[len(text)-1].Write(t)
			}
		case xml.EndElement:
			n := stack[len(stack)-1]
			n.Text = strings.TrimSpace(text[len(text)-1].String())
			stack, text = stack[:len(stack)-1], text[:len(text)-1]
		}
	}
	if root == nil {
		return nil, errors.New("ehf: parse the document: no root element")
	}
	return root, nil
}

// qname is a "cac:Name" or "cbc:Name" step as the namespace it stands for;
// a step without a prefix is in the root's own namespace and matches by its
// local name only.
func qname(step string) (space, local string) {
	prefix, name, ok := strings.Cut(step, ":")
	if !ok {
		return "", step
	}
	switch prefix {
	case "cac":
		return nsCAC, name
	case "cbc":
		return nsCBC, name
	}
	return prefix, name
}

func (n *Node) is(step string) bool {
	space, local := qname(step)
	return n.Name.Local == local && (space == "" || n.Name.Space == space)
}

// All is every descendant reached by the path's steps, each a direct child
// of the one before ("cac:TaxTotal", "cac:TaxSubtotal"), in document order.
func (n *Node) All(path ...string) []*Node {
	if n == nil {
		return nil
	}
	current := []*Node{n}
	for _, step := range path {
		var next []*Node
		for _, c := range current {
			for _, child := range c.Children {
				if child.is(step) {
					next = append(next, child)
				}
			}
		}
		current = next
	}
	return current
}

// First is the first node the path reaches, or nil.
func (n *Node) First(path ...string) *Node {
	if all := n.All(path...); len(all) > 0 {
		return all[0]
	}
	return nil
}

// Value is the text of the first node the path reaches; empty when none.
func (n *Node) Value(path ...string) string {
	if f := n.First(path...); f != nil {
		return f.Text
	}
	return ""
}

// Has reports whether the path reaches any node.
func (n *Node) Has(path ...string) bool { return n.First(path...) != nil }

// Attr is the value of the unqualified attribute local, or empty.
func (n *Node) Attr(local string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attrs {
		if a.Name.Space == "" && a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// Descendants is every node below n, in document order, whose name is the
// step's.
func (n *Node) Descendants(step string) []*Node {
	var out []*Node
	var walk func(*Node)
	walk = func(m *Node) {
		for _, c := range m.Children {
			if c.is(step) {
				out = append(out, c)
			}
			walk(c)
		}
	}
	if n != nil {
		walk(n)
	}
	return out
}
