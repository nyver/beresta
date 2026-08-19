import { useCallback, useEffect, useMemo, useState } from "react";

import { listNotebooks, listNotes, listTags, lockAccount, searchByTag, unwrapError } from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { NotebookTree } from "../shell/NotebookTree";
import { NoteDetail } from "../shell/NoteDetail";
import { NoteList } from "../shell/NoteList";
import { TagList } from "../shell/TagList";

export interface ShellProps {
  account: main.AccountInfo;
  onLocked: () => void;
}

type Selection = { kind: "all" } | { kind: "notebook"; id: string } | { kind: "tag"; id: string };

/**
 * Shell is the desktop application's main screen: notebook tree, tag
 * navigation, and a note list, wired to real account data. Selecting a
 * note only routes to NoteDetail's placeholder pane - the rich-text
 * editor is task 5.4.
 */
export function Shell({ onLocked }: ShellProps) {
  const { t, errorMessage, ready } = useI18n();
  const [locking, setLocking] = useState(false);

  const [notebooks, setNotebooks] = useState<main.NotebookDTO[]>([]);
  const [tags, setTags] = useState<main.TagDTO[]>([]);
  const [notes, setNotes] = useState<main.NoteDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [selection, setSelection] = useState<Selection>({ kind: "all" });
  const [selectedNoteId, setSelectedNoteId] = useState("");
  const [tagNotes, setTagNotes] = useState<main.NoteDTO[]>([]);
  const [tagLoading, setTagLoading] = useState(false);

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
  }, [notes, tagNotes, selection]);

  const selectedNote = visibleNotes.find((note) => note.id === selectedNoteId) ?? null;

  async function handleLock() {
    setLocking(true);
    try {
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
        <button type="button" onClick={() => void handleLock()} disabled={locking}>
          {t("shell.lock_button")}
        </button>
      </header>

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
              onSelect={(id) => setSelection(id === "" ? { kind: "all" } : { kind: "notebook", id })}
            />
            <TagList
              tags={tags}
              selectedId={selection.kind === "tag" ? selection.id : ""}
              onSelect={(id) => setSelection({ kind: "tag", id })}
            />
          </aside>
          <section className="shell-notes">
            <NoteList
              notes={visibleNotes}
              loading={loading || (selection.kind === "tag" && tagLoading)}
              selectedNoteId={selectedNoteId}
              onSelect={setSelectedNoteId}
            />
          </section>
          <section className="shell-detail">
            <NoteDetail note={selectedNote} />
          </section>
        </div>
      )}
    </div>
  );
}
