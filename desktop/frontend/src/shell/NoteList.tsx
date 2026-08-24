import { useVirtualizer } from "@tanstack/react-virtual";
import { Fragment, useRef, type KeyboardEvent, type ReactNode } from "react";

import { formatNoteTimestamp, stripMarkdown } from "../format";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

/** One note's list-row display metadata, looked up by note ID rather than
 * carried on each `notes` entry directly: `notes` here can come from three
 * different IPC calls (ListNotes/SearchByTag/Search), only one of which
 * (ListNotes, workspace-wide) is enriched with accurate UpdatedMS/Preview -
 * see Shell.tsx's noteMetaById. */
export interface NoteListMeta {
  updatedMs: number;
  preview: string;
}

export interface NoteListProps {
  notes: main.NoteDTO[];
  loading: boolean;
  selectedNoteId: string;
  onSelect: (noteId: string) => void;
  /** Preview snippet + last-modified time for every note in the workspace,
   * keyed by note ID; a note absent here (should not normally happen -
   * every note surfaced by any selection is also in the workspace-wide
   * list this is built from) just renders no preview/date. */
  noteMetaById: Map<string, NoteListMeta>;
  /** Free-text words to highlight (case-insensitive) within each note's
   * title, e.g. the terms an active search matched on. Empty/omitted
   * renders titles plainly. */
  highlightTerms?: string[];
  /** Overrides the "no notes" message shown when notes is empty, e.g. to
   * distinguish an empty search result from an empty notebook. */
  emptyMessage?: string;
}

// Taller than a title-only row (task: note list shows a preview + date) so
// the virtualizer's initial layout guess is close before it measures actual
// rendered rows.
const ESTIMATED_ROW_HEIGHT = 72;

// Matches NotebookTree.tsx's DRAG_TYPE_NOTE: a note row is a drag source
// for refiling it into a different notebook by dropping it on that
// notebook's row.
const DRAG_TYPE_NOTE = "application/x-beresta-note-id";

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** highlightTitle wraps every case-insensitive match of any term in
 * `terms` inside a <mark>. Matching is plain substring, not word-bounded,
 * mirroring FTS5's own unstemmed token matching closely enough for a
 * result-list highlight without needing the search engine's own match
 * offsets (SearchResultDTO carries none). */
function highlightTitle(title: string, terms: string[]): ReactNode {
  const cleanTerms = [...new Set(terms.map((term) => term.trim()).filter(Boolean))];
  if (cleanTerms.length === 0) return title;
  const pattern = new RegExp(`(${cleanTerms.map(escapeRegExp).join("|")})`, "gi");
  const parts = title.split(pattern).filter((part) => part !== "");
  const lowerTerms = new Set(cleanTerms.map((term) => term.toLowerCase()));
  return parts.map((part, index) =>
    lowerTerms.has(part.toLowerCase()) ? (
      <mark key={index}>{part}</mark>
    ) : (
      <Fragment key={index}>{part}</Fragment>
    ),
  );
}

/**
 * NoteList only renders the rows currently scrolled into view
 * (@tanstack/react-virtual): the account ceiling is 20,000 notes
 * (design.md), and mounting that many DOM rows at once would make the
 * list itself the slow part of "instant local search" (task 5.6's 150 ms
 * budget covers the query, not a naive render of every result).
 */
export function NoteList({
  notes,
  loading,
  selectedNoteId,
  onSelect,
  noteMetaById,
  highlightTerms = [],
  emptyMessage,
}: NoteListProps) {
  const { t } = useI18n();
  const scrollRef = useRef<HTMLDivElement>(null);

  const virtualizer = useVirtualizer({
    count: notes.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ESTIMATED_ROW_HEIGHT,
    overscan: 8,
  });

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (notes.length === 0) return;
    let nextIndex: number | null = null;
    const currentIndex = notes.findIndex((note) => note.id === selectedNoteId);

    if (event.key === "ArrowDown") {
      nextIndex = currentIndex < 0 ? 0 : Math.min(currentIndex + 1, notes.length - 1);
    } else if (event.key === "ArrowUp") {
      nextIndex = currentIndex < 0 ? notes.length - 1 : Math.max(currentIndex - 1, 0);
    } else {
      return;
    }

    event.preventDefault();
    onSelect(notes[nextIndex].id);
    virtualizer.scrollToIndex(nextIndex, { align: "auto" });
  }

  if (loading) {
    return <p className="note-list-status">{t("common.loading")}</p>;
  }
  if (notes.length === 0) {
    return <p className="note-list-status">{emptyMessage ?? t("shell.notelist_empty")}</p>;
  }

  return (
    <div
      ref={scrollRef}
      className="note-list"
      role="listbox"
      aria-label={t("shell.title")}
      tabIndex={0}
      onKeyDown={handleKeyDown}
    >
      <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
        {virtualizer.getVirtualItems().map((virtualRow) => {
          const note = notes[virtualRow.index];
          const meta = noteMetaById.get(note.id);
          return (
            <button
              key={note.id}
              type="button"
              role="option"
              aria-selected={note.id === selectedNoteId}
              ref={virtualizer.measureElement}
              data-index={virtualRow.index}
              className={`note-row${note.id === selectedNoteId ? " selected" : ""}`}
              style={{
                position: "absolute",
                top: 0,
                left: 0,
                width: "100%",
                transform: `translateY(${virtualRow.start}px)`,
              }}
              onClick={() => onSelect(note.id)}
              draggable
              onDragStart={(event) => {
                event.dataTransfer.setData(DRAG_TYPE_NOTE, note.id);
                event.dataTransfer.effectAllowed = "move";
              }}
            >
              <span className="note-row-top">
                <span className="note-row-title">
                  {note.title ? highlightTitle(note.title, highlightTerms) : t("shell.untitled_note")}
                </span>
                {note.pinned ? (
                  <span aria-label={t("shell.pinned_note")} className="note-row-flag">
                    ★
                  </span>
                ) : null}
                {meta ? <span className="note-row-date">{formatNoteTimestamp(meta.updatedMs)}</span> : null}
              </span>
              <span className="note-row-bottom">
                {meta?.preview ? <span className="note-row-preview">{stripMarkdown(meta.preview)}</span> : null}
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}
