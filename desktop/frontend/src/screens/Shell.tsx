import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  ensureDailyBackup,
  getSettings,
  listNotebooks,
  listNotes,
  listTags,
  lockAccount,
  searchByTag,
  unwrapError,
  verifyAllBackups,
} from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { BackupsPanel } from "../shell/BackupsPanel";
import { ImportExportPanel } from "../shell/ImportExportPanel";
import { Modal } from "../shell/Modal";
import { NotebookTree } from "../shell/NotebookTree";
import { NoteEditorPane, type NoteEditorPaneHandle } from "../shell/NoteEditorPane";
import { NoteList } from "../shell/NoteList";
import { SearchBar, type SearchBarHandle } from "../shell/SearchBar";
import { TagList } from "../shell/TagList";

export interface ShellProps {
  account: main.AccountInfo;
  onLocked: () => void;
}

type Selection = { kind: "all" } | { kind: "notebook"; id: string } | { kind: "tag"; id: string };

/**
 * Shell is the desktop application's main screen: notebook tree, tag
 * navigation, a note list, and the Yjs-backed body editor, all wired to
 * real account data.
 */
export function Shell({ onLocked }: ShellProps) {
  const { t, errorMessage, ready } = useI18n();
  const [locking, setLocking] = useState(false);
  const editorPaneRef = useRef<NoteEditorPaneHandle>(null);
  const searchBarRef = useRef<SearchBarHandle>(null);

  const [notebooks, setNotebooks] = useState<main.NotebookDTO[]>([]);
  const [tags, setTags] = useState<main.TagDTO[]>([]);
  const [notes, setNotes] = useState<main.NoteDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [selection, setSelection] = useState<Selection>({ kind: "all" });
  const [selectedNoteId, setSelectedNoteId] = useState("");
  const [tagNotes, setTagNotes] = useState<main.NoteDTO[]>([]);
  const [tagLoading, setTagLoading] = useState(false);
  // null means no search is active, so the sidebar's notebook/tag/all
  // selection drives the list below as usual; a search box query (task
  // 5.6) overrides that selection whenever it is non-null, cleared again
  // by SearchBar.clear() or by picking a notebook/tag from the sidebar.
  const [searchResults, setSearchResults] = useState<main.SearchResultDTO[] | null>(null);
  const [highlightTerms, setHighlightTerms] = useState<string[]>([]);
  const [dataModalOpen, setDataModalOpen] = useState(false);

  const loadAll = useCallback(() => {
    setLoading(true);
    setError(null);
    Promise.all([listNotebooks(), listTags(), listNotes()])
      .then(([loadedNotebooks, loadedTags, loadedNotes]) => {
        setNotebooks(loadedNotebooks);
        setTags(loadedTags);
        setNotes(loadedNotes);
      })
      .catch((thrown: unknown) => setError(errorMessage(unwrapError(thrown))))
      .finally(() => setLoading(false));
  }, [errorMessage]);

  useEffect(() => {
    // Gated on ready for the same reason as App.tsx's AppShell: an error
    // here needs the locale catalog already loaded so errorMessage can
    // localize it instead of falling back to raw backend text. In
    // production this is always already true (App only mounts Shell
    // after ready), but Shell does not otherwise depend on that caller
    // discipline to behave correctly.
    if (!ready) return;
    loadAll();
  }, [ready, loadAll]);

  useEffect(() => {
    // Runs once per unlock, matching desktop/backup.go's own doc comments
    // ("The desktop shell calls this once at every startup") for both
    // EnsureDailyBackup and VerifyAllBackups. Best-effort: a failure here
    // (for example, an external backup drive that is currently
    // disconnected) must not block using the app - BackupsPanel's own
    // actions surface any real, persistent problem when the user opens
    // it, so this silently retries at the next unlock instead of erroring
    // the whole shell.
    if (!ready) return;
    getSettings()
      .then((settings) => {
        if (!settings.backup_directory) return;
        return ensureDailyBackup(settings.backup_directory).then(() => verifyAllBackups());
      })
      .catch(() => {});
  }, [ready]);

  useEffect(() => {
    setSelectedNoteId("");
    if (selection.kind !== "tag") return;
    let canceled = false;
    setTagLoading(true);
    searchByTag(selection.id)
      .then((results) => {
        if (!canceled) setTagNotes(results.map((result) => result.note));
      })
      .catch((thrown: unknown) => {
        if (!canceled) setError(errorMessage(unwrapError(thrown)));
      })
      .finally(() => {
        if (!canceled) setTagLoading(false);
      });
    return () => {
      canceled = true;
    };
  }, [selection, errorMessage]);

  const visibleNotes = useMemo(() => {
    // An active search overrides the sidebar's notebook/tag/all selection
    // entirely, matching the note list's own deleted-inclusion choice
    // (SearchBar's "include deleted" checkbox), not the other branches'
    // blanket exclusion below.
    if (searchResults !== null) return searchResults.map((result) => result.note);
    const live = notes.filter((note) => !note.deleted);
    switch (selection.kind) {
      case "all":
        return live;
      case "notebook":
        // Exact match only: a selected notebook does not (yet) include
        // its descendants' notes. A future revision may want to, but
        // that is a product decision, not implied by this task.
        return live.filter((note) => note.notebook_id === selection.id);
      case "tag":
        return tagNotes;
    }
  }, [notes, tagNotes, selection, searchResults]);

  const selectedNote = visibleNotes.find((note) => note.id === selectedNoteId) ?? null;

  function handleTitleCommitted(noteId: string, title: string) {
    // Optimistic local patch: the editor pane already persisted this
    // through CommitNoteBody, but Shell's own `notes` state (used by the
    // "all"/notebook filters) is only refreshed by loadAll, so the note
    // list would otherwise keep showing the pre-rename title until the
    // next full reload. tagNotes and searchResults need the same patch:
    // they are separate fetches (searchByTag / search), not derived from
    // `notes`, so visibleNotes' "tag" and active-search branches would
    // miss a rename made while that filter is active.
    const rename = (note: main.NoteDTO) => (note.id === noteId ? { ...note, title } : note);
    setNotes((current) => current.map(rename));
    setTagNotes((current) => current.map(rename));
    setSearchResults((current) =>
      current === null
        ? current
        : current.map((result) => main.SearchResultDTO.createFrom({ ...result, note: rename(result.note) })),
    );
  }

  async function handleLock() {
    setLocking(true);
    try {
      await editorPaneRef.current?.flush();
      await lockAccount();
      onLocked();
    } finally {
      setLocking(false);
    }
  }

  return (
    <div className="screen shell">
      <header className="shell-topbar">
        <h1>{t("shell.title")}</h1>
        <div className="shell-topbar-actions">
          <button type="button" onClick={() => setDataModalOpen(true)}>
            {t("data.title")}
          </button>
          <button type="button" onClick={() => void handleLock()} disabled={locking}>
            {t("shell.lock_button")}
          </button>
        </div>
      </header>

      {dataModalOpen ? (
        <Modal title={t("data.title")} onClose={() => setDataModalOpen(false)}>
          <BackupsPanel onRestored={loadAll} />
          <ImportExportPanel onImported={loadAll} />
        </Modal>
      ) : null}

      {error ? (
        <div className="shell-error">
          <p role="alert">{error}</p>
          <button type="button" onClick={loadAll}>
            {t("common.retry")}
          </button>
        </div>
      ) : (
        <div className="shell-body">
          <aside className="shell-sidebar">
            <NotebookTree
              notebooks={notebooks}
              selectedId={
                selection.kind === "all" ? "" : selection.kind === "notebook" ? selection.id : null
              }
              onSelect={(id) => {
                searchBarRef.current?.clear();
                setSelection(id === "" ? { kind: "all" } : { kind: "notebook", id });
              }}
            />
            <TagList
              tags={tags}
              selectedId={selection.kind === "tag" ? selection.id : ""}
              onSelect={(id) => {
                searchBarRef.current?.clear();
                setSelection({ kind: "tag", id });
              }}
            />
          </aside>
          <section className="shell-notes">
            <SearchBar
              ref={searchBarRef}
              tags={tags}
              onResultsChange={(results, terms) => {
                setSearchResults(results);
                setHighlightTerms(terms);
              }}
            />
            <NoteList
              notes={visibleNotes}
              loading={
                searchResults === null && (loading || (selection.kind === "tag" && tagLoading))
              }
              selectedNoteId={selectedNoteId}
              onSelect={setSelectedNoteId}
              highlightTerms={highlightTerms}
              emptyMessage={searchResults !== null ? t("search.no_results") : undefined}
            />
          </section>
          <section className="shell-detail">
            <NoteEditorPane ref={editorPaneRef} note={selectedNote} onTitleCommitted={handleTitleCommitted} />
          </section>
        </div>
      )}
    </div>
  );
}
