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
