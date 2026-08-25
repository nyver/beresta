import "package:dart_quill_delta/dart_quill_delta.dart";

/// Converts between Beresta's canonical Markdown projection and a Quill
/// [Delta], so the mobile WYSIWYG editor (flutter_quill) can round-trip
/// through the same Markdown-string wire format the gomobile `GetNote`/
/// `SaveNote` calls already use (see core/mobileapi/service.go), without
/// flattening bold/italic/strike/code/link/header/blockquote/list/
/// code-block formatting to literal syntax.
///
/// This is a line-for-line port of core/sync/yjsadapter/markdown.go's
/// renderMarkdown/parseMarkdownLines: the attribute keys and Markdown syntax
/// here must stay byte-identical to that file, since the Go core is the
/// authority on the wire format and is what desktop's Quill editor renders
/// from after a sync. Deliberately not using a general-purpose Markdown
/// package: a generic converter's syntax choices (nesting order, list/
/// header conventions, ...) are not guaranteed to match the Go parser
/// exactly, which would silently reintroduce the same class of formatting
/// loss this pairing was written to fix.

/// One inline text span with its own formatting attributes.
class _Run {
  const _Run(this.text, this.attrs);

  final String text;
  final Map<String, Object?> attrs;
}

/// One paragraph: its inline runs plus the block-level attributes carried
/// by the newline that ends it (Quill/Markdown-projection convention).
class _Line {
  const _Line(this.runs, this.block);

  final List<_Run> runs;
  final Map<String, Object?> block;
}

const _emptyAttrs = <String, Object?>{};

/// Converts a Delta (as read from `QuillController.document.toDelta()`)
/// into Beresta's canonical Markdown text, mirroring
/// core/sync/yjsadapter/markdown.go's renderMarkdown exactly.
String deltaToMarkdown(Delta delta) {
  final lines = _splitLines(delta);
  final out = StringBuffer();
  var orderedIndex = 0;
  var inFence = false;
  for (var i = 0; i < lines.length; i++) {
    final ln = lines[i];
    if (i > 0) out.write("\n");
    final fenced = ln.block["code-block"] == true;
    if (fenced != inFence) {
      out.write("```\n");
      inFence = fenced;
    }
    if (fenced) {
      orderedIndex = 0;
      out.write(_plainText(ln.runs));
      continue;
    }
    orderedIndex = _renderLine(out, ln, orderedIndex);
  }
  if (inFence) out.write("\n```");
  return out.toString();
}

List<_Line> _splitLines(Delta delta) {
  final lines = <_Line>[];
  var current = <_Run>[];
  for (final op in delta.toList()) {
    if (!op.isInsert) continue;
    final data = op.data;
    if (data is! String) continue;
    final attrs = op.attributes ?? _emptyAttrs;
    var text = data;
    while (true) {
      final i = text.indexOf("\n");
      if (i < 0) {
        if (text.isNotEmpty) current.add(_Run(text, attrs));
        break;
      }
      if (i > 0) current.add(_Run(text.substring(0, i), attrs));
      lines.add(_Line(current, attrs));
      current = <_Run>[];
      text = text.substring(i + 1);
    }
  }
  if (current.isNotEmpty) {
    lines.add(_Line(current, _emptyAttrs));
  }
  return lines;
}

int _renderLine(StringBuffer out, _Line ln, int orderedIndex) {
  final content = _renderRuns(_mergeRuns(ln.runs));
  final header = ln.block["header"];
  if (header is num) {
    final level = header.toInt();
    if (level >= 1 && level <= 6) {
      out.write("#" * level);
      out.write(" ");
    }
    orderedIndex = 0;
  } else if (ln.block["blockquote"] == true) {
    out.write("> ");
    orderedIndex = 0;
  } else if (ln.block["list"] == "bullet") {
    out.write("- ");
    orderedIndex = 0;
  } else if (ln.block["list"] == "ordered") {
    orderedIndex++;
    out.write("$orderedIndex. ");
  } else {
    orderedIndex = 0;
  }
  out.write(content);
  return orderedIndex;
}

List<_Run> _mergeRuns(List<_Run> runs) {
  final merged = <_Run>[];
  for (final r in runs) {
    if (merged.isNotEmpty && _attrsEqual(merged.last.attrs, r.attrs)) {
      final last = merged.removeLast();
      merged.add(_Run(last.text + r.text, last.attrs));
    } else {
      merged.add(r);
    }
  }
  return merged;
}

bool _attrsEqual(Map<String, Object?> a, Map<String, Object?> b) {
  if (a.length != b.length) return false;
  for (final entry in a.entries) {
    if (b[entry.key] != entry.value) return false;
  }
  return true;
}

String _plainText(List<_Run> runs) => runs.map((r) => r.text).join();

String _renderRuns(List<_Run> runs) =>
    runs.map((r) => _renderInline(r.text, r.attrs)).join();

String _renderInline(String text, Map<String, Object?> attrs) {
  if (text.isEmpty) return "";
  if (attrs["code"] == true) return "`$text`";
  if (attrs["strike"] == true) text = "~~$text~~";
  if (attrs["italic"] == true) text = "*$text*";
  if (attrs["bold"] == true) text = "**$text**";
  final href = attrs["link"];
  if (href is String && href.isNotEmpty) text = "[$text]($href)";
  return text;
}

/// Converts Beresta's canonical Markdown text into a [Delta] suitable for
/// `Document.fromDelta`, mirroring
/// core/sync/yjsadapter/markdown.go's parseMarkdownLines exactly. Every
/// line (including the last) is terminated with an explicit "\n", matching
/// Quill's own document invariant that content always ends in a newline.
Delta markdownToDelta(String markdown) {
  final lines = _parseMarkdownLines(markdown);
  final delta = Delta();
  for (final ln in lines) {
    for (final r in ln.runs) {
      if (r.text.isEmpty) continue;
      delta.insert(r.text, r.attrs.isEmpty ? null : r.attrs);
    }
    delta.insert("\n", ln.block.isEmpty ? null : ln.block);
  }
  if (lines.isEmpty) {
    delta.insert("\n");
  }
  return delta;
}

List<_Line> _parseMarkdownLines(String markdown) {
  final rawLines = markdown.split("\n");
  final lines = <_Line>[];
  var inFence = false;
  for (final raw in rawLines) {
    if (raw == "```") {
      inFence = !inFence;
      continue;
    }
    if (inFence) {
      lines.add(_Line([_Run(raw, _emptyAttrs)], const {"code-block": true}));
      continue;
    }
    lines.add(_parseBlockLine(raw));
  }
  return lines;
}

final _orderedListMarker = RegExp(r"^\d+\. ");

_Line _parseBlockLine(String raw) {
  final block = <String, Object?>{};
  var content = raw;
  final level = _headerLevel(raw);
  if (level > 0) {
    block["header"] = level;
    content = raw.substring(level + 1);
  } else if (raw.startsWith("> ")) {
    block["blockquote"] = true;
    content = raw.substring(2);
  } else if (raw.startsWith("- ")) {
    block["list"] = "bullet";
    content = raw.substring(2);
  } else {
    final match = _orderedListMarker.firstMatch(raw);
    if (match != null) {
      block["list"] = "ordered";
      content = raw.substring(match.end);
    }
  }
  return _Line(_parseInline(content), block);
}

/// Returns the ATX header level (1-6) raw starts with, or 0 if raw is not a
/// header line (a "#" run immediately followed by a space).
int _headerLevel(String raw) {
  var level = 0;
  while (level < raw.length && level < 6 && raw.codeUnitAt(level) == 0x23) {
    level++;
  }
  if (level == 0 || level >= raw.length || raw.codeUnitAt(level) != 0x20) {
    return 0;
  }
  return level;
}

enum _Marker { none, code, link, boldItalic, bold, italic, strike }

/// Returns the offset and kind of the earliest inline delimiter in s, or
/// (-1, none) if s has none.
(int, _Marker) _nextInlineMarker(String s) {
  for (var i = 0; i < s.length; i++) {
    final unit = s.codeUnitAt(i);
    switch (unit) {
      case 0x60: // `
        return (i, _Marker.code);
      case 0x5B: // [
        return (i, _Marker.link);
      case 0x2A: // *
        if (s.startsWith("***", i)) return (i, _Marker.boldItalic);
        if (s.startsWith("**", i)) return (i, _Marker.bold);
        return (i, _Marker.italic);
      case 0x7E: // ~
        if (s.startsWith("~~", i)) return (i, _Marker.strike);
    }
  }
  return (-1, _Marker.none);
}

/// Reports the content between a leading open delimiter and the next
/// occurrence of close, plus the text remaining after it, or null if s does
/// not start with open, or close never appears.
(String, String)? _extractDelims(String s, String open, String close) {
  if (!s.startsWith(open)) return null;
  final rest = s.substring(open.length);
  final idx = rest.indexOf(close);
  if (idx < 0) return null;
  return (rest.substring(0, idx), rest.substring(idx + close.length));
}

/// Parses a "[text](url)" span starting at s[0], recursively parsing the
/// link text for further inline formatting and attaching the link's URL to
/// every run it produces. Null when s does not start a well-formed link.
(List<_Run>, String)? _parseLink(String s) {
  final closeBracket = s.indexOf("](");
  if (closeBracket < 0) return null;
  final rest = s.substring(closeBracket + 2);
  final endParen = rest.indexOf(")");
  if (endParen < 0) return null;
  final linkText = s.substring(1, closeBracket);
  final url = rest.substring(0, endParen);
  var inner = _parseInline(linkText);
  if (url.isNotEmpty) {
    inner = _attachAttr(inner, "link", url);
  }
  return (inner, rest.substring(endParen + 1));
}

/// Adds one attribute to every run, merging it into that run's existing
/// attributes. Runs with no text (an empty formatted span, e.g. "****") are
/// left alone.
List<_Run> _attachAttr(List<_Run> runs, String key, Object value) {
  return [
    for (final r in runs)
      if (r.text.isEmpty) r else _Run(r.text, {...r.attrs, key: value}),
  ];
}

/// Tokenizes one line's content into runs, recognizing the same markers
/// deltaToMarkdown emits: code spans (highest precedence, non-nesting),
/// links, and bold/italic/strike (applied inside-out, matching
/// _renderInline's fixed strike/italic/bold/link nesting order). An
/// unmatched opening delimiter is emitted as literal text.
List<_Run> _parseInline(String s) {
  final out = <_Run>[];
  while (s.isNotEmpty) {
    final (idx, kind) = _nextInlineMarker(s);
    if (idx < 0) {
      out.add(_Run(s, _emptyAttrs));
      break;
    }
    if (idx > 0) {
      out.add(_Run(s.substring(0, idx), _emptyAttrs));
      s = s.substring(idx);
    }

    var consumed = false;
    switch (kind) {
      case _Marker.code:
        final d = _extractDelims(s, "`", "`");
        if (d != null) {
          if (d.$1.isNotEmpty) out.add(_Run(d.$1, const {"code": true}));
          s = d.$2;
          consumed = true;
        }
      case _Marker.link:
        final r = _parseLink(s);
        if (r != null) {
          out.addAll(r.$1);
          s = r.$2;
          consumed = true;
        }
      case _Marker.boldItalic:
        final d = _extractDelims(s, "***", "***");
        if (d != null) {
          out.addAll(
            _attachAttr(
              _attachAttr(_parseInline(d.$1), "bold", true),
              "italic",
              true,
            ),
          );
          s = d.$2;
          consumed = true;
        }
      case _Marker.bold:
        final d = _extractDelims(s, "**", "**");
        if (d != null) {
          out.addAll(_attachAttr(_parseInline(d.$1), "bold", true));
          s = d.$2;
          consumed = true;
        }
      case _Marker.italic:
        final d = _extractDelims(s, "*", "*");
        if (d != null) {
          out.addAll(_attachAttr(_parseInline(d.$1), "italic", true));
          s = d.$2;
          consumed = true;
        }
      case _Marker.strike:
        final d = _extractDelims(s, "~~", "~~");
        if (d != null) {
          out.addAll(_attachAttr(_parseInline(d.$1), "strike", true));
          s = d.$2;
          consumed = true;
        }
      case _Marker.none:
        break;
    }
    if (!consumed) {
      out.add(_Run(s.substring(0, 1), _emptyAttrs));
      s = s.substring(1);
    }
  }
  return out;
}
