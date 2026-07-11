package main

import (
	"strings"
	"testing"
)

func TestParseXMLTraversal(t *testing.T) {
	root, err := parseXML([]byte(`<project>
		<name>app</name>
		<build>
			<resources>
				<resource><directory>src</directory></resource>
				<resource><directory>conf</directory></resource>
			</resources>
		</build>
		<dependencies>
			<dependency><artifactId>x</artifactId></dependency>
			<dependency><artifactId>y</artifactId></dependency>
		</dependencies>
	</project>`))
	if err != nil {
		t.Fatal(err)
	}
	if root.childText("name") != "app" {
		t.Errorf("childText(name) = %q", root.childText("name"))
	}
	if root.find("build", "resources") == nil {
		t.Error("find(build, resources) should exist")
	}
	if root.find("build", "nope") != nil {
		t.Error("find of missing path should be nil")
	}
	if n := len(root.find("build", "resources").childrenNamed("resource")); n != 2 {
		t.Errorf("childrenNamed(resource) = %d, want 2", n)
	}
	if n := len(root.descendants("dependency")); n != 2 {
		t.Errorf("descendants(dependency) = %d, want 2", n)
	}
	// nil-safety
	var nilNode *XMLNode
	if nilNode.child("x") != nil || nilNode.childText("x") != "" || nilNode.childrenNamed("x") != nil {
		t.Error("nil node accessors must be safe")
	}
}

func TestParseXMLLatin1Charset(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="ISO-8859-1"?><doc><name>caf` + "\xe9" + `</name></doc>`)
	root, err := parseXML(data)
	if err != nil {
		t.Fatalf("ISO-8859-1 document should parse: %v", err)
	}
	if got := root.childText("name"); got != "café" {
		t.Errorf("childText(name) = %q, want café", got)
	}
}

func TestParseXMLUnsupportedCharset(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="EBCDIC-INTL"?><doc/>`)
	if _, err := parseXML(data); err == nil {
		t.Error("unsupported charset should error, not silently misparse")
	}
}

func TestCanonicalAttributeOrder(t *testing.T) {
	a, err := parseXML([]byte(`<e b="2" a="1">text</e>`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseXML([]byte(`<e a="1" b="2">text</e>`))
	if err != nil {
		t.Fatal(err)
	}
	if a.canonical() != b.canonical() {
		t.Errorf("canonical must be attribute-order independent:\n%s\n%s", a.canonical(), b.canonical())
	}
}

func TestCanonicalizeXML(t *testing.T) {
	compact := `<mule><flow name="a"><logger message="hi"/></flow></mule>`
	pretty := "<mule>\n    <flow name=\"a\">\n        <logger message=\"hi\"/>\n    </flow>\n</mule>\n"
	c1, err := canonicalizeXML([]byte(compact))
	if err != nil {
		t.Fatal(err)
	}
	c2, err := canonicalizeXML([]byte(pretty))
	if err != nil {
		t.Fatal(err)
	}
	if c1 != c2 {
		t.Errorf("formatting-only variants must canonicalize identically:\n%q\n%q", c1, c2)
	}

	changed, err := canonicalizeXML([]byte(`<mule><flow name="b"><logger message="hi"/></flow></mule>`))
	if err != nil {
		t.Fatal(err)
	}
	if changed == c1 {
		t.Error("real changes must survive canonicalization")
	}

	if _, err := canonicalizeXML([]byte("<broken")); err == nil {
		t.Error("invalid XML must error")
	}
}

func TestCanonicalizeXMLKeepsTextWhitespace(t *testing.T) {
	// whitespace INSIDE text content is meaningful (e.g. inline DataWeave)
	a, _ := canonicalizeXML([]byte(`<e>a b</e>`))
	b, _ := canonicalizeXML([]byte(`<e>a  b</e>`))
	if a == b {
		t.Error("whitespace inside text content must remain significant")
	}
	if !strings.Contains(a, "a b") {
		t.Errorf("canonical form should keep the text, got %q", a)
	}
}
