import { useVirtualizer } from "@tanstack/react-virtual";
import { useRef, type KeyboardEvent } from "react";

import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

export interface NoteListProps {
  notes: main.NoteDTO[];
  loading: boolean;
  selectedNoteId: string;
  onSelect: (noteId: string) => void;
}

const ESTIMATED_ROW_HEIGHT = 56;

/**
 * NoteList only renders the rows currently scrolled into view
 * (@tanstack/react-virtual): the account ceiling is 20,000 notes
 * (design.md), and mounting that many DOM rows at once would make the
 * list itself the slow part of "instant local search" (task 5.6's 150 ms
 * budget covers the query, not a naive render of every result).
 */
export function NoteList({ notes, loading, selectedNoteId, onSelect }: NoteListProps) {
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
    return <p className="note-list-status">{t("shell.notelist_empty")}</p>;
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
            >
              <span className="note-row-title">{note.title || t("shell.untitled_note")}</span>
              {note.pinned ? (
                <span aria-label={t("shell.pinned_note")} className="note-row-flag">
                  ★
                </span>
              ) : null}
            </button>
          );
        })}
      </div>
    </div>
  );
}
