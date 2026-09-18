import "package:dart_quill_delta/dart_quill_delta.dart";

/// Exactly the formatting keys core/sync/yjsadapter's canonical Markdown
/// projection understands (see Attr* in core/sync/yjsadapter/document.go
/// and markdown_delta.dart's port of it), matching desktop's own
/// TOOLBAR_FORMATS restriction in
/// desktop/frontend/src/editor/NoteEditor.tsx. Anything else a paste
/// source might bring in (underline, color, background, font, size,
/// alignment, subscript/superscript, ...) round-trips through the
/// editor's own Document harmlessly but would otherwise vanish silently,
/// only the next time this note's body is saved as Markdown - this keeps
/// that degradation visible and predictable at paste time instead.
const _canonicalFormatKeys = {
  "header",
  "bold",
  "italic",
  "strike",
  "code",
  "list",
  "blockquote",
  "code-block",
  "link",
};

const _minHeaderLevel = 1;
const _maxHeaderLevel = 3;

/// The only two "list" values core/sync/yjsadapter's canonical Markdown
/// projection understands (ListBullet/ListOrdered in document.go).
/// flutter_quill's list format also accepts "checked"/"unchecked" for a
/// checklist item (Attribute.checked/Attribute.unchecked in attribute.dart),
/// which a paste source can produce even though no toolbar button offers
/// it - markdown_delta.dart's _renderLine has no case for it and silently
/// drops the list marker on export, so it must degrade at paste time too,
/// exactly like an out-of-range header level.
const _canonicalListValues = {"bullet", "ordered"};

/// Degrades a pasted [Delta] (from flutter_quill's rich-text HTML/Markdown
/// clipboard conversion) to the canonical semantic format set: every
/// attribute outside [_canonicalFormatKeys] is dropped while the
/// underlying text is kept, and a heading level outside 1-3
/// (flutter_quill's HTML conversion accepts source h1-h6) is dropped
/// entirely rather than clamped, so a pasted `<h4>`-`<h6>` becomes a plain
/// paragraph - a visible, predictable degradation at paste time instead of
/// a silent one, later, when this note's body is next saved as Markdown.
/// Mirrors desktop's stripUnsupportedFormats
/// (desktop/frontend/src/editor/pasteFormat.ts).
Delta stripUnsupportedFormats(Delta delta) {
  final result = Delta();
  for (final op in delta.toList()) {
    if (!op.isInsert) {
      result.push(op);
      continue;
    }
    final attrs = op.attributes;
    if (attrs == null || attrs.isEmpty) {
      result.insert(op.data);
      continue;
    }
    final cleaned = <String, dynamic>{};
    for (final entry in attrs.entries) {
      if (!_canonicalFormatKeys.contains(entry.key)) continue;
      if (entry.key == "header") {
        final level = entry.value;
        if (level is! num || level < _minHeaderLevel || level > _maxHeaderLevel) {
          continue;
        }
      }
      if (entry.key == "list" && !_canonicalListValues.contains(entry.value)) {
        continue;
      }
      cleaned[entry.key] = entry.value;
    }
    result.insert(op.data, cleaned.isEmpty ? null : cleaned);
  }
  return result;
}
