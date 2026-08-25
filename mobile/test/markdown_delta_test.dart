import "package:beresta/markdown_delta.dart";
import "package:dart_quill_delta/dart_quill_delta.dart";
import "package:flutter_test/flutter_test.dart";

/// Mirrors core/sync/yjsadapter/markdown_test.go: markdownToDelta/
/// deltaToMarkdown must stay byte-identical to the Go core's parser/
/// renderer, since GetNote/SaveNote round-trip the same Markdown string
/// through both.
void main() {
  test(
    "inline formatting renders in the fixed strike/italic/bold/link order",
    () {
      final delta =
          Delta()
            ..insert("bold", {"bold": true})
            ..insert(" plain ")
            ..insert("code", {"code": true})
            ..insert(" link", {"link": "https://example.com"});

      expect(
        deltaToMarkdown(delta),
        "**bold** plain `code`[ link](https://example.com)",
      );
    },
  );

  test("nested inline order is deterministic", () {
    final delta =
        Delta()..insert("text", {"bold": true, "italic": true, "strike": true});

    expect(deltaToMarkdown(delta), "***~~text~~***");
  });

  test("block formatting renders headers, quotes, and ordered lists", () {
    final delta =
        Delta()
          ..insert("Title")
          ..insert("\n", {"header": 2})
          ..insert("quoted")
          ..insert("\n", {"blockquote": true})
          ..insert("one")
          ..insert("\n", {"list": "ordered"})
          ..insert("two")
          ..insert("\n", {"list": "ordered"});

    expect(deltaToMarkdown(delta), "## Title\n> quoted\n1. one\n2. two");
  });

  test("code block fences contiguous lines", () {
    final delta =
        Delta()
          ..insert("line one")
          ..insert("\n", {"code-block": true})
          ..insert("line two")
          ..insert("\n", {"code-block": true})
          ..insert("after");

    expect(deltaToMarkdown(delta), "```\nline one\nline two\n```\nafter");
  });

  test("empty delta renders as empty markdown", () {
    expect(deltaToMarkdown(Delta()), "");
  });

  group("markdownToDelta round-trips through deltaToMarkdown", () {
    void roundTrips(String markdown) {
      expect(deltaToMarkdown(markdownToDelta(markdown)), markdown);
    }

    test("plain text", () => roundTrips("hello from the mobile editor"));

    test("inline formatting", () {
      roundTrips("**bold** plain `code`[ link](https://example.com)");
    });

    test("nested inline formatting", () => roundTrips("***~~text~~***"));

    test("block formatting", () {
      roundTrips("## Title\n> quoted\n1. one\n2. two");
    });

    test("code fence", () {
      roundTrips("```\nline one\nline two\n```\nafter");
    });

    test("bullet list", () => roundTrips("- one\n- two"));
  });

  test("markdownToDelta applies formatting instead of literal syntax", () {
    // The regression this guards: if the mobile editor round-tripped its
    // Markdown body as plain text instead of parsing it, "**bold**" would
    // stay eight literal characters with no bold attribute - exactly what a
    // desktop Quill editor bound to the same underlying rich-text root
    // would then render as raw, unformatted Markdown syntax (see
    // core/sync/yjsadapter's ReplaceMarkdown, its Go-side counterpart).
    final delta = markdownToDelta("**bold** plain `code`[ link](url)");
    final plainText =
        delta
            .toList()
            .where((op) => op.isInsert && op.data is String)
            .map((op) => op.data as String)
            .join();

    expect(plainText, "bold plain code link\n");
  });

  test("markdownToDelta always ends in a newline", () {
    // Quill's Document requires its content to end in "\n"; adjacent
    // same-attribute inserts (here, the trailing line and its terminating
    // newline) get compacted into one Operation by Delta.push, so this
    // checks the tail of the concatenated text rather than assuming the
    // final "\n" stays its own operation.
    bool endsWithNewline(String markdown) {
      final data = markdownToDelta(markdown).toList().last.data;
      return data is String && data.endsWith("\n");
    }

    expect(endsWithNewline(""), isTrue);
    expect(endsWithNewline("no trailing newline"), isTrue);
  });

  test(
    "markdownToDelta handles an unterminated code fence without crashing",
    () {
      expect(() => markdownToDelta("```"), returnsNormally);
    },
  );
}
