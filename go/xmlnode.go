package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

// XMLNode is a generic XML element tree, standing in for the REXML documents
// the Ruby version traverses.
type XMLNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Text     string     `xml:",chardata"`
	Children []XMLNode  `xml:",any"`
}

func parseXML(data []byte) (*XMLNode, error) {
	var root XMLNode
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	return &root, nil
}

func (n *XMLNode) child(name string) *XMLNode {
	if n == nil {
		return nil
	}
	for i := range n.Children {
		if n.Children[i].XMLName.Local == name {
			return &n.Children[i]
		}
	}
	return nil
}

func (n *XMLNode) childText(name string) string {
	c := n.child(name)
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.Text)
}

func (n *XMLNode) find(path ...string) *XMLNode {
	cur := n
	for _, name := range path {
		cur = cur.child(name)
		if cur == nil {
			return nil
		}
	}
	return cur
}

func (n *XMLNode) childrenNamed(name string) []*XMLNode {
	if n == nil {
		return nil
	}
	var out []*XMLNode
	for i := range n.Children {
		if n.Children[i].XMLName.Local == name {
			out = append(out, &n.Children[i])
		}
	}
	return out
}

// descendants finds elements at any depth below n, like REXML's //name
func (n *XMLNode) descendants(name string) []*XMLNode {
	if n == nil {
		return nil
	}
	var out []*XMLNode
	for i := range n.Children {
		c := &n.Children[i]
		if c.XMLName.Local == name {
			out = append(out, c)
		}
		out = append(out, c.descendants(name)...)
	}
	return out
}

// canonical is a stable serialization used for hashing and sorting, filling
// the role of REXML's element.to_s / canonicalize_xml in the Ruby version.
func (n *XMLNode) canonical() string {
	var b strings.Builder
	n.writeCanonical(&b)
	return b.String()
}

func (n *XMLNode) writeCanonical(b *strings.Builder) {
	b.WriteString("<")
	b.WriteString(n.XMLName.Local)
	attrs := make([]xml.Attr, len(n.Attrs))
	copy(attrs, n.Attrs)
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].Name.Local < attrs[j].Name.Local })
	for _, a := range attrs {
		fmt.Fprintf(b, " %s=%q", a.Name.Local, a.Value)
	}
	b.WriteString(">")
	b.WriteString(strings.TrimSpace(n.Text))
	for i := range n.Children {
		n.Children[i].writeCanonical(b)
	}
	b.WriteString("</")
	b.WriteString(n.XMLName.Local)
	b.WriteString(">")
}

// canonicalizeXML re-emits an XML document with normalized indentation and
// trimmed text so formatting-only differences disappear before diffing. Both
// sides of a comparison go through this same function, so only its internal
// consistency matters, not its exact output format.
func canonicalizeXML(data []byte) (string, error) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	var buf strings.Builder
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if cd, ok := tok.(xml.CharData); ok {
			trimmed := strings.TrimSpace(string(cd))
			if trimmed == "" {
				continue
			}
			tok = xml.CharData(trimmed)
		}
		if err := enc.EncodeToken(tok); err != nil {
			return "", err
		}
	}
	if err := enc.Flush(); err != nil {
		return "", err
	}
	return buf.String(), nil
}
