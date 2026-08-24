/** formatNoteTimestamp renders a note's last-modified time as a short
 * locale-aware date (e.g. "Aug 23"), falling back to include the year when
 * it differs from the current one. Deliberately a static absolute date, not
 * a live-updating "2 min ago" relative time: the note list already
 * re-renders lazily (see Shell's noteMetaById), so a relative label would
 * just go stale-looking without a ticking timer to keep it honest. */
export function formatNoteTimestamp(unixMS: number): string {
  const date = new Date(unixMS);
  const sameYear = date.getFullYear() === new Date().getFullYear();
  return date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: sameYear ? undefined : "numeric",
  });
}

/** formatClockTime renders a timestamp as a locale-aware time-of-day (e.g.
 * "14:32"), for a static "last synced at" label - deliberately not a
 * live-updating "2 min ago" relative time, for the same reason
 * formatNoteTimestamp above gives: nothing here re-renders on a ticking
 * timer, so a relative label would just go stale-looking. */
export function formatClockTime(unixMS: number): string {
  return new Date(unixMS).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

/** stripMarkdown renders a note body's canonical Markdown source (see
 * core/sync/yjsadapter's doc comments) as plain display text for the note
 * list's preview snippet: headings/emphasis/list/quote markers, links, and
 * code fences read as literal punctuation clutter in a one-line preview,
 * where none of that formatting can render anyway. This is a display-only,
 * best-effort transform - it does not need to be a full Markdown parser,
 * just to strip the syntax the toolbar's own TOOLBAR_FORMATS can produce
 * (see editor/NoteEditor.tsx). */
export function stripMarkdown(text: string): string {
  return text
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/^\s{0,3}#{1,6}\s+/gm, "")
    .replace(/^\s{0,3}>\s?/gm, "")
    .replace(/^\s*([-*+]|\d+[.)])\s+/gm, "")
    .replace(/(\*\*\*|___)(.*?)\1/g, "$2")
    .replace(/(\*\*|__)(.*?)\1/g, "$2")
    .replace(/(\*|_)(.*?)\1/g, "$2")
    .replace(/\s+/g, " ")
    .trim();
}

/** formatBytes renders a byte count as a human-readable size, e.g. "2.0 KB". */
export function formatBytes(size: number): string {
  if (size < 1024) return `${size} B`;
  const units = ["KB", "MB", "GB"];
  let value = size / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[unitIndex]}`;
}
