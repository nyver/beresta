package yjsadapter

import (
	"errors"
	"testing"

	"github.com/reearth/ygo/crdt"
)

func TestMarkdownInlineFormatting(t *testing.T) {
	doc := New()
	defer doc.Close()

	// Each insert explicitly cancels the previous run's attributes instead of
	// relying on Insert's cursor-inheritance behavior (attrs nil/empty
	// continues the preceding format, matching Yjs JS insertText semantics),
	// so the resulting formatting is unambiguous regardless of typing order.
	if err := doc.Insert("body", 0, "bold", Attributes{AttrBold: true}); err != nil {
		t.Fatalf("insert bold: %v", err)
	}
	if err := doc.Insert("body", 4, " plain ", Attributes{AttrBold: nil}); err != nil {
		t.Fatalf("insert plain: %v", err)
	}
	if err := doc.Insert("body", 11, "code", Attributes{AttrCode: true}); err != nil {
		t.Fatalf("insert code: %v", err)
	}
	if err := doc.Insert("body", 15, " link", Attributes{AttrCode: nil, AttrLink: "https://example.com"}); err != nil {
		t.Fatalf("insert link: %v", err)
	}

	got, err := doc.Markdown("body")
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}
	want := "**bold** plain `code`[ link](https://example.com)"
	if got != want {
		t.Fatalf("markdown = %q, want %q", got, want)
	}
}

func TestMarkdownNestedInlineOrderIsDeterministic(t *testing.T) {
	doc := New()
	defer doc.Close()

	attrs := Attributes{AttrBold: true, AttrItalic: true, AttrStrike: true}
	if err := doc.Insert("body", 0, "text", attrs); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := doc.Markdown("body")
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}
	// renderInline applies markers in a fixed order (strike, then italic, then
	// bold, then link) regardless of iteration order over the attrs map, so
	// the same formatting always yields the same Markdown text.
	want := "***~~text~~***"
	if got != want {
		t.Fatalf("markdown = %q, want %q", got, want)
	}
}

func TestMarkdownBlockFormatting(t *testing.T) {
	doc := New()
	defer doc.Close()

	if err := doc.Insert("body", 0, "Title", nil); err != nil {
		t.Fatalf("insert title text: %v", err)
	}
	if err := doc.Insert("body", 5, "\n", Attributes{AttrHeader: 2}); err != nil {
		t.Fatalf("insert header newline: %v", err)
	}
	if err := doc.Insert("body", 6, "quoted", nil); err != nil {
		t.Fatalf("insert quote text: %v", err)
	}
	if err := doc.Insert("body", 12, "\n", Attributes{AttrHeader: nil, AttrBlockquote: true}); err != nil {
		t.Fatalf("insert quote newline: %v", err)
	}
	if err := doc.Insert("body", 13, "one", nil); err != nil {
		t.Fatalf("insert list item 1: %v", err)
	}
	if err := doc.Insert("body", 16, "\n", Attributes{AttrBlockquote: nil, AttrList: ListOrdered}); err != nil {
		t.Fatalf("insert list newline 1: %v", err)
	}
	if err := doc.Insert("body", 17, "two", nil); err != nil {
		t.Fatalf("insert list item 2: %v", err)
	}
	if err := doc.Insert("body", 20, "\n", Attributes{AttrList: ListOrdered}); err != nil {
		t.Fatalf("insert list newline 2: %v", err)
	}

	got, err := doc.Markdown("body")
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}
	want := "## Title\n> quoted\n1. one\n2. two"
	if got != want {
		t.Fatalf("markdown = %q, want %q", got, want)
	}
}

func TestMarkdownCodeBlockFencesContiguousLines(t *testing.T) {
	doc := New()
	defer doc.Close()

	if err := doc.Insert("body", 0, "line one", nil); err != nil {
		t.Fatalf("insert code line 1: %v", err)
	}
	if err := doc.Insert("body", 8, "\n", Attributes{AttrCodeBlock: true}); err != nil {
		t.Fatalf("insert code newline 1: %v", err)
	}
	if err := doc.Insert("body", 9, "line two", nil); err != nil {
		t.Fatalf("insert code line 2: %v", err)
	}
	if err := doc.Insert("body", 17, "\n", Attributes{AttrCodeBlock: true}); err != nil {
		t.Fatalf("insert code newline 2: %v", err)
	}
	if err := doc.Insert("body", 18, "after", nil); err != nil {
		t.Fatalf("insert trailing text: %v", err)
	}

	got, err := doc.Markdown("body")
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}
	want := "```\nline one\nline two\n```\nafter"
	if got != want {
		t.Fatalf("markdown = %q, want %q", got, want)
	}
}

func TestMarkdownRejectsEmbeddedContent(t *testing.T) {
	_, err := renderMarkdown([]crdt.Delta{{Op: crdt.DeltaOpInsert, Insert: 42}})
	if !errors.Is(err, ErrUnsupportedContent) {
		t.Fatalf("embed error = %v, want ErrUnsupportedContent", err)
	}
}

func TestMarkdownEmptyDocument(t *testing.T) {
	doc := New()
	defer doc.Close()

	got, err := doc.Markdown("body")
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}
	if got != "" {
		t.Fatalf("markdown = %q, want empty", got)
	}
}
