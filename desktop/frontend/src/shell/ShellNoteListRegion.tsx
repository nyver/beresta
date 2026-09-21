import type { Ref } from "react";

import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { NoteList, type NoteListMeta } from "./NoteList";
import { SearchBar, type SearchBarHandle, type SearchBarProps } from "./SearchBar";
import { ErrorState } from "./StatusState";

export interface ShellNoteListRegionProps {
  searchBarRef: Ref<SearchBarHandle>;
  tags: main.TagDTO[];
  allNotes: main.NoteDTO[];
  onSearchResultsChange: SearchBarProps["onResultsChange"];
  noteActionError: string | null;
  visibleNotes: main.NoteDTO[];
  loading: boolean;
  selectedNoteId: string;
  onSelectNote: (noteId: string) => void;
  noteMetaById: Map<string, NoteListMeta>;
  highlightTerms: string[];
  /** Whether an active search produced these visibleNotes, so an empty
   * result gets the "clear search" empty state instead of the plain
   * "no notes" one. */
  searchActive: boolean;
  onClearSearch: () => void;
  /** Creates a note in whichever notebook is currently selected (see
   * Shell.tsx's handleCreateNote), giving the new-note command a
   * pointer-accessible entry point independent of the notebook tree's own
   * per-row menu and the Ctrl+N shortcut (task 8.3). */
  onCreateNote: () => void;
  creatingNote: boolean;
}

/**
 * ShellNoteListRegion is the desktop shell's persistent note-list region
 * (task 8.1, specs/windows-desktop-client's "Stable three-pane desktop
 * workspace" requirement): the local search box above the virtualized note
 * list, plus any non-blocking note-action error banner.
 */
export function ShellNoteListRegion({
  searchBarRef,
  tags,
  allNotes,
  onSearchResultsChange,
  noteActionError,
  visibleNotes,
  loading,
  selectedNoteId,
  onSelectNote,
  noteMetaById,
  highlightTerms,
  searchActive,
  onClearSearch,
  onCreateNote,
  creatingNote,
}: ShellNoteListRegionProps) {
  const { t } = useI18n();

  return (
    <section className="shell-notes">
      <div className="shell-notes-toolbar">
        <button
          type="button"
          className="tree-section-add-button"
          aria-label={t("shell.new_note_button")}
          title={t("shell.new_note_button")}
          disabled={creatingNote}
          onClick={onCreateNote}
        >
          +
        </button>
      </div>
      {noteActionError ? <ErrorState message={noteActionError} /> : null}
      <SearchBar ref={searchBarRef} tags={tags} notes={allNotes} onResultsChange={onSearchResultsChange} />
      <NoteList
        notes={visibleNotes}
        loading={loading}
        selectedNoteId={selectedNoteId}
        onSelect={onSelectNote}
        noteMetaById={noteMetaById}
        highlightTerms={highlightTerms}
        emptyMessage={searchActive ? t("search.no_results") : undefined}
        emptyAction={searchActive ? { label: t("search.clear_button"), onClick: onClearSearch } : undefined}
      />
    </section>
  );
}
