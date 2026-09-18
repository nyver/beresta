import { Delta, type Op } from "quill";

// Exactly the formatting keys core/sync/yjsadapter's canonical Markdown
// projection understands (see Attr* in core/sync/yjsadapter/document.go),
// mirroring TOOLBAR_FORMATS in NoteEditor.tsx. Anything else a paste
// source might bring in (underline, color, background, font, size,
// alignment, subscript/superscript, ...) round-trips through the CRDT
// harmlessly but would otherwise vanish silently, only at Markdown export -
// this keeps that degradation visible and predictable at paste time
// instead.
const CANONICAL_FORMAT_KEYS = new Set([
  "header",
  "bold",
  "italic",
  "strike",
  "code",
  "list",
  "blockquote",
  "code-block",
  "link",
]);

const MIN_HEADER_LEVEL = 1;
const MAX_HEADER_LEVEL = 3;

// The only two "list" values core/sync/yjsadapter's canonical Markdown
// projection understands (ListBullet/ListOrdered in document.go). Quill's
// List format also accepts "checked"/"unchecked" for a checklist item
// (formats/list.js), which a paste source (another Quill-based app,
// Notion, Google Docs, ...) can produce even though no toolbar button
// offers it - markdown.go's renderLine has no case for it and silently
// drops the list marker on export, so it must degrade at paste time too,
// exactly like an out-of-range header level.
const CANONICAL_LIST_VALUES = new Set(["bullet", "ordered"]);

/**
 * stripUnsupportedFormats is a Quill clipboard matcher (registered after
 * Quill's own built-in matchers, so it sees their fully-attributed output)
 * that degrades pasted content to the canonical semantic format set: every
 * attribute outside CANONICAL_FORMAT_KEYS is dropped while the underlying
 * text is kept, and a heading level outside 1-3 (Quill's Header blot
 * accepts source HTML h1-h6) is dropped entirely rather than clamped, so a
 * pasted <h4>-<h6> becomes a plain paragraph - a visible, predictable
 * degradation at paste time instead of a silent one, later, at export.
 */
export function stripUnsupportedFormats(_node: Node, delta: Delta): Delta {
  return new Delta(delta.ops.map(stripOpFormats));
}

function stripOpFormats(op: Op): Op {
  if (!op.attributes) return op;

  const attributes: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(op.attributes)) {
    if (!CANONICAL_FORMAT_KEYS.has(key)) continue;
    if (key === "header" && (typeof value !== "number" || value < MIN_HEADER_LEVEL || value > MAX_HEADER_LEVEL)) {
      continue;
    }
    if (key === "list" && !CANONICAL_LIST_VALUES.has(value as string)) {
      continue;
    }
    attributes[key] = value;
  }

  const cleaned: Op = { ...op };
  if (Object.keys(attributes).length > 0) {
    cleaned.attributes = attributes;
  } else {
    delete cleaned.attributes;
  }
  return cleaned;
}
