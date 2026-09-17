import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";

import {
  createNote,
  createTag,
  deleteNote,
  ensureDailyBackup,
  getSettings,
  listNotebooks,
  listNotes,
  listTags,
  lockAccount,
  noteTagsByWorkspace,
  restoreNote,
  searchByTag,
  setNoteTag,
  syncNow,
  syncSummary,
  updateSettings,
  unwrapError,
  verifyAllBackups,
  type SyncState,
} from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { EventsOn, EventsOff } from "../../wailsjs/runtime/runtime";
import { BackupsPanel } from "../shell/BackupsPanel";
import { ImportExportPanel } from "../shell/ImportExportPanel";
import { Modal } from "../shell/Modal";
import { NotebookTree } from "../shell/NotebookTree";
import { NoteEditorPane, type NoteEditorPaneHandle } from "../shell/NoteEditorPane";
import { NoteList, type NoteListMeta } from "../shell/NoteList";
import { QuickNotePanel } from "../shell/QuickNotePanel";
import { SearchBar, type SearchBarHandle } from "../shell/SearchBar";
import { DiagnosticsPanel } from "../shell/DiagnosticsPanel";
import { ShellIntegrationPanel } from "../shell/ShellIntegrationPanel";
import { SyncPanel } from "../shell/SyncPanel";
import { UndoSnackbar } from "../shell/UndoSnackbar";
import { TagList } from "../shell/TagList";

// Matches desktop/events.go's EventQuickNoteOpen.
const EVENT_QUICK_NOTE_OPEN = "quicknote:open";
// Matches desktop/events.go's EventSyncSummary (see also SyncPanel.tsx's own
// subscription - this one feeds the topbar's compact status pill and the
// open note's footer status line instead of the full Sync modal). The event
// itself carries no payload; it signals this effect to re-fetch the summary
// via syncSummary() instead of racing a value embedded in the event.
const EVENT_SYNC_SUMMARY = "sync:summary";

// Persisted across launches (task: collapsible sidebar/focus mode for
// smaller windows) so the user's chosen layout survives a restart.
const SIDEBAR_COLLAPSED_KEY = "beresta.sidebar-collapsed";
const FOCUS_MODE_KEY = "beresta.focus-mode";

export interface ShellProps {
  account: main.AccountInfo;
  onLocked: () => void;
}

type Selection = { kind: "all" } | { kind: "notebook"; id: string } | { kind: "tag"; id: string };

function sortNotesByLastModified(notes: main.NoteDTO[]): main.NoteDTO[] {
  return [...notes].sort((left, right) => {
    if (left.updated_unix_ms === right.updated_unix_ms) return left.id.localeCompare(right.id);
    return right.updated_unix_ms - left.updated_unix_ms;
  });
}

/**
 * Shell is the desktop application's main screen: notebook tree, tag
 * navigation, a note list, and the Yjs-backed body editor, all wired to
 * real account data.
 */
export function Shell({ account, onLocked }: ShellProps) {
  const { t, errorMessage, ready } = useI18n();
  const shellTitleId = useId();
  const [locking, setLocking] = useState(false);
  const editorPaneRef = useRef<NoteEditorPaneHandle>(null);
  const searchBarRef = useRef<SearchBarHandle>(null);
  // null means "not yet loaded"; the auto-lock idle timer stays disarmed
  // until it knows the real value, so it can never fire early using a
  // guessed default.
  const [autoLockMinutes, setAutoLockMinutes] = useState<number | null>(null);

  const [notebooks, setNotebooks] = useState<main.NotebookDTO[]>([]);
  const [tags, setTags] = useState<main.TagDTO[]>([]);
  const [notes, setNotes] = useState<main.NoteDTO[]>([]);
  // Every note's current tag membership, note id -> tag ids, loaded once
  // up front (App.NoteTagsByWorkspace) alongside notebooks/tags/notes so
  // NoteEditorPane's tag editor never needs a per-note fetch.
  const [noteTagIds, setNoteTagIds] = useState<Record<string, string[]>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [selection, setSelection] = useState<Selection>({ kind: "all" });
  const [selectedNoteId, setSelectedNoteId] = useState("");
  // The id of a note created this session that the user has not yet
  // edited (title, body, tags, or attachments) - eligible for silent
  // removal if abandoned by navigating away without touching it (see the
  // effect below and handleCreateNote). "" means no such draft exists.
  // A ref, not state: it must never itself trigger a render, and the
  // navigate-away effect needs its value at the instant selection
  // changes, not a stale render-time snapshot.
  const draftNoteIdRef = useRef("");
  const [autoFocusNoteId, setAutoFocusNoteId] = useState("");
  const markDraftTouched = useCallback(() => {
    draftNoteIdRef.current = "";
  }, []);
  const [tagNotes, setTagNotes] = useState<main.NoteDTO[]>([]);
  const [tagLoading, setTagLoading] = useState(false);
  // null means no search is active, so the sidebar's notebook/tag/all
  // selection drives the list below as usual; a search box query (task
  // 5.6) overrides that selection whenever it is non-null, cleared again
  // by SearchBar.clear() or by picking a notebook/tag from the sidebar.
  const [searchResults, setSearchResults] = useState<main.SearchResultDTO[] | null>(null);
  const [highlightTerms, setHighlightTerms] = useState<string[]>([]);
  const [dataModalOpen, setDataModalOpen] = useState(false);
  const [syncModalOpen, setSyncModalOpen] = useState(false);
  // Workspace-wide synchronization state, shared by the topbar's compact
  // pill and the open note's footer status line (SaveStatusLine) so the two
  // never disagree; loaded once and kept live via the same "sync:summary"
  // event SyncPanel itself listens for.
  const [syncStatusValue, setSyncStatusValue] = useState<SyncState | null>(null);
  const [forcingSync, setForcingSync] = useState(false);
  // Avoid refetching the entire workspace for every status poll that keeps
  // reporting "current". It is reset while a new cycle is active so the
  // next successful cycle reloads incoming remote changes exactly once.
  const syncWasCurrentRef = useRef(false);
  // Set only when syncStatusValue transitions to "current", so the status
  // line can show a static "synced at HH:MM" - see SaveStatusLine's doc
  // comment on why this is not a live-ticking relative time.
  const [syncedAt, setSyncedAt] = useState<number | null>(null);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(
    () => window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === "1",
  );
  const [focusMode, setFocusMode] = useState(() => window.localStorage.getItem(FOCUS_MODE_KEY) === "1");
  // Bumped on every quicknote:open so QuickNotePanel remounts (and thus
  // creates a fresh note) for each capture session instead of reusing
  // whatever note the previous session left mounted.
  const [quickNoteSession, setQuickNoteSession] = useState(0);
  const [quickNoteOpen, setQuickNoteOpen] = useState(false);
  const [creatingNote, setCreatingNote] = useState(false);
  // Shared by note creation and delete/undo failures: a non-blocking
  // inline banner above the note list, distinct from the full-screen
  // `error` state below (which replaces the whole shell for a load
  // failure) - a failed note action should never do that.
  const [noteActionError, setNoteActionError] = useState<string | null>(null);
  const [deletedNoteId, setDeletedNoteId] = useState<string | null>(null);
  const deletedNoteTimerRef = useRef<number | undefined>(undefined);
  // The selection-change effect normally closes the open note. Creating a
  // note in another notebook is the exception: after navigating to that
  // notebook, keep the just-created note selected and open.
  const preserveCreatedNoteSelectionRef = useRef(false);

  const loadAll = useCallback(() => {
    setLoading(true);
    setError(null);
    Promise.all([listNotebooks(), listTags(), listNotes(), noteTagsByWorkspace()])
      .then(([loadedNotebooks, loadedTags, loadedNotes, loadedNoteTagIds]) => {
        setNotebooks(loadedNotebooks);
        setTags(loadedTags);
        setNotes(loadedNotes);
        setNoteTagIds(loadedNoteTagIds);
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
        setAutoLockMinutes(settings.auto_lock_minutes);
        if (!settings.backup_directory) return;
        return ensureDailyBackup(settings.backup_directory).then(() => verifyAllBackups());
      })
      .catch(() => {});
  }, [ready]);

  useEffect(() => {
    // Fires when the global quick-note hotkey is pressed or the tray
    // menu's "Quick Note" item is selected (desktop/shell.go's
    // handleQuickNoteTrigger already brought the main window to the
    // front before emitting this). EventOff (not EventsOn's own return
    // value) is the established unsubscribe pattern in this codebase -
    // see AttachmentPanel's OnFileDrop/OnFileDropOff.
    if (!ready) return;
    EventsOn(EVENT_QUICK_NOTE_OPEN, () => {
      setQuickNoteSession((session) => session + 1);
      setQuickNoteOpen(true);
    });
    return () => EventsOff(EVENT_QUICK_NOTE_OPEN);
  }, [ready]);

  useEffect(() => {
    if (!ready) return;
    let disposed = false;
    function applyState(next: SyncState) {
      if (disposed) return;
      setSyncStatusValue(next);
      if (next === "current") {
        const becameCurrent = !syncWasCurrentRef.current;
        syncWasCurrentRef.current = true;
        setSyncedAt(Date.now());
        // The sync worker applies remote operations to the local database,
        // but React state is independent of that database. Reload the
        // workspace once the cycle has applied successfully so, for example,
        // a title changed on mobile appears in the desktop note list.
        if (becameCurrent) loadAll();
      } else {
        syncWasCurrentRef.current = false;
      }
    }
    const refreshSummary = () => {
      syncSummary()
        .then((summary) => applyState(summary.state))
        .catch(() => {});
    };
    refreshSummary();
    // EventSyncSummary normally triggers a refetch as phases advance.
    // Polling as well prevents the visual state from staying on
    // "Synchronizing" if an event is missed while the worker completes.
    const interval = window.setInterval(refreshSummary, 5_000);
    EventsOn(EVENT_SYNC_SUMMARY, refreshSummary);
    return () => {
      disposed = true;
      window.clearInterval(interval);
      EventsOff(EVENT_SYNC_SUMMARY);
    };
  }, [ready, loadAll]);

  useEffect(() => {
    window.localStorage.setItem(SIDEBAR_COLLAPSED_KEY, sidebarCollapsed ? "1" : "0");
  }, [sidebarCollapsed]);

  useEffect(() => {
    window.localStorage.setItem(FOCUS_MODE_KEY, focusMode ? "1" : "0");
  }, [focusMode]);

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if ((event.ctrlKey || event.metaKey) && event.shiftKey && event.key.toLowerCase() === "s") {
        event.preventDefault();
        setSidebarCollapsed((current) => !current);
      }
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, []);

  useEffect(() => {
    if (preserveCreatedNoteSelectionRef.current) {
      preserveCreatedNoteSelectionRef.current = false;
    } else {
      setSelectedNoteId("");
    }
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
        return sortNotesByLastModified(live);
      case "notebook":
        // Exact match only: a selected notebook does not (yet) include
        // its descendants' notes. A future revision may want to, but
        // that is a product decision, not implied by this task.
        return sortNotesByLastModified(live.filter((note) => note.notebook_id === selection.id));
      case "tag":
        return sortNotesByLastModified(tagNotes);
    }
  }, [notes, tagNotes, selection, searchResults]);

  // Built from `notes` alone (the workspace-wide ListNotes fetch) rather
  // than from visibleNotes itself: tagNotes/searchResults come from their
  // own separate IPC calls (SearchByTag/Search) that are not enriched with
  // preview/last-modified metadata, since re-running that workspace-wide
  // join on every keystroke of an interactive search would risk the
  // search box's own latency budget for no benefit - every note surfaced
  // by any selection is also present in `notes`, so looking it up here by
  // ID covers all four selection kinds without duplicating the join.
  const noteMetaById = useMemo(() => {
    const map = new Map<string, NoteListMeta>();
    for (const note of notes) {
      map.set(note.id, { updatedMs: note.updated_unix_ms, preview: note.preview });
    }
    return map;
  }, [notes]);

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

  async function handleCreateNote(
    notebookId = selection.kind === "notebook" ? selection.id : "",
  ) {
    if (creatingNote) return;
    setCreatingNote(true);
    setNoteActionError(null);
    try {
      const created = await createNote(notebookId, "");
      setNotes((current) => [created, ...current]);
      searchBarRef.current?.clear();
      const targetSelection: Selection = notebookId ? { kind: "notebook", id: notebookId } : { kind: "all" };
      const alreadyInTarget =
        (targetSelection.kind === "all" && selection.kind === "all") ||
        (targetSelection.kind === "notebook" &&
          selection.kind === "notebook" &&
          selection.id === targetSelection.id);
      if (!alreadyInTarget) {
        // A note created from another notebook's menu must be visible right
        // away, rather than remaining hidden by the prior notebook, tag, or
        // search selection.
        preserveCreatedNoteSelectionRef.current = true;
        setSelection(targetSelection);
      }
      draftNoteIdRef.current = created.id;
      setAutoFocusNoteId(created.id);
      setSelectedNoteId(created.id);
    } catch (thrown: unknown) {
      setNoteActionError(errorMessage(unwrapError(thrown)));
    } finally {
      setCreatingNote(false);
    }
  }

  // Instant creation and durable automatic save (specs/notes-management):
  // "the client may remove [a newly created note with no edited title,
  // body, attachments, or tags] without creating a visible note or
  // synchronization error" once the user leaves it. Runs whenever the
  // selection moves away from the tracked draft; a best-effort local
  // cleanup, never surfaced as an error if it fails (the note simply
  // remains, exactly like today without this behavior).
  useEffect(() => {
    const abandoned = draftNoteIdRef.current;
    if (abandoned && abandoned !== selectedNoteId) {
      draftNoteIdRef.current = "";
      void deleteNote(abandoned)
        .then(() => {
          const dropStray = (note: main.NoteDTO) => note.id !== abandoned;
          setNotes((current) => current.filter(dropStray));
          setTagNotes((current) => current.filter(dropStray));
        })
        .catch(() => {
          // Best-effort: an untouched draft that fails to clean up just
          // stays visible, same as before this behavior existed.
        });
    }
  }, [selectedNoteId]);

  // How long the undo snackbar stays up before the deletion becomes
  // final in the UI's eyes (the tombstone itself was already committed
  // durably the moment this fired - undo just clears it again).
  const UNDO_DELETE_DURATION_MS = 8000;

  // Recoverable ordinary note deletion (specs/notes-management): removes
  // the note from the active list immediately and offers undo, with no
  // confirmation prompt - unlike notebook/tag deletion or a whole backup
  // restore, which stay behind their own explicit confirmations since
  // this task's recovery policy does not cover them.
  async function handleDeleteNote(noteId: string) {
    // Mirrors handleTitleCommitted's optimistic patch: `notes` drives the
    // "all"/notebook filters directly, while tagNotes and searchResults
    // are separate fetches that would otherwise keep showing the
    // just-deleted note until the next full reload or re-search. `notes`
    // itself keeps the row (soft-marked) rather than dropping it, so
    // noteMetaById and a same-session undo can still find it.
    setNotes((current) => current.map((note) => (note.id === noteId ? { ...note, deleted: true } : note)));
    setTagNotes((current) => current.filter((note) => note.id !== noteId));
    setSearchResults((current) =>
      current === null ? current : current.filter((result) => result.note.id !== noteId),
    );
    setSelectedNoteId("");

    try {
      await deleteNote(noteId);
    } catch (thrown: unknown) {
      // The tombstone commit itself failed: nothing was actually
      // deleted, so put the optimistic removal back rather than leave
      // the list disagreeing with local state.
      setNotes((current) => current.map((note) => (note.id === noteId ? { ...note, deleted: false } : note)));
      setNoteActionError(errorMessage(unwrapError(thrown)));
      return;
    }

    window.clearTimeout(deletedNoteTimerRef.current);
    setDeletedNoteId(noteId);
    deletedNoteTimerRef.current = window.setTimeout(() => setDeletedNoteId(null), UNDO_DELETE_DURATION_MS);
  }

  async function handleUndoDelete() {
    const noteId = deletedNoteId;
    if (!noteId) return;
    window.clearTimeout(deletedNoteTimerRef.current);
    setDeletedNoteId(null);
    try {
      // Offline-capable: this is the same signed local commit path as
      // any other edit, so it durably un-tombstones the note now and
      // synchronizes later, with no network required (specs/notes-
      // management's "note returns from local state and the resulting
      // operations synchronize later" scenario).
      await restoreNote(noteId);
    } catch (thrown: unknown) {
      setNoteActionError(errorMessage(unwrapError(thrown)));
      return;
    }
    setNotes((current) => current.map((note) => (note.id === noteId ? { ...note, deleted: false } : note)));
    setSelectedNoteId(noteId);
  }

  function handleNotebookMoved(notebookId: string, newParentId: string) {
    setNotebooks((current) =>
      current.map((notebook) => (notebook.id === notebookId ? { ...notebook, parent_id: newParentId } : notebook)),
    );
  }

  function handleNotebookRenamed(notebookId: string, name: string) {
    setNotebooks((current) =>
      current.map((notebook) => (notebook.id === notebookId ? { ...notebook, name } : notebook)),
    );
  }

  function handleNoteMoved(noteId: string, notebookId: string) {
    // Same three-state patch as handleTitleCommitted/handleDeleteNote: a
    // note dragged onto a different notebook needs its notebook_id updated
    // everywhere it might currently be displayed from.
    const refile = (note: main.NoteDTO) => (note.id === noteId ? { ...note, notebook_id: notebookId } : note);
    setNotes((current) => current.map(refile));
    setTagNotes((current) => current.map(refile));
    setSearchResults((current) =>
      current === null
        ? current
        : current.map((result) => main.SearchResultDTO.createFrom({ ...result, note: refile(result.note) })),
    );
    // Dragging the currently open note out of the notebook currently being
    // browsed would otherwise leave it "open" in the editor pane while
    // having just vanished from the note list beside it - clearing the
    // selection here instead falls back to the same empty-state placeholder
    // handleDeleteNote already uses for an equivalent disappearance.
    if (selectedNoteId === noteId && selection.kind === "notebook" && selection.id !== notebookId) {
      setSelectedNoteId("");
    }
  }

  async function handleToggleNoteTag(noteId: string, tagId: string, present: boolean) {
    await setNoteTag(noteId, tagId, present);
    setNoteTagIds((current) => {
      const existing = current[noteId] ?? [];
      const next = present ? [...new Set([...existing, tagId])] : existing.filter((id) => id !== tagId);
      return { ...current, [noteId]: next };
    });
  }

  async function handleCreateAndAssignTag(noteId: string, name: string) {
    const tag = await createTag(name);
    setTags((current) => [...current, tag]);
    await handleToggleNoteTag(noteId, tag.id, true);
  }

  function handleTagCreated(tag: main.TagDTO) {
    setTags((current) => [...current, tag]);
  }

  function handleTagDeleted(tagId: string) {
    setTags((current) => current.filter((tag) => tag.id !== tagId));
    // A deleted tag can no longer be a valid sidebar selection; its notes
    // are still reachable via "All Notes" or a notebook, just no longer
    // filterable by this tag (mirrors NotebookTree.onDeleted's fallback).
    if (selection.kind === "tag" && selection.id === tagId) {
      searchBarRef.current?.clear();
      setSelection({ kind: "all" });
    }
  }

  async function handleLock() {
    setLocking(true);
    try {
      await editorPaneRef.current?.flush();
      await lockAccount();
      onLocked();
      // Deliberately not reset to false here: the parent (App.tsx)
      // re-resolves its screen right after onLocked and unmounts Shell,
      // but that happens on a later render, not synchronously. Resetting
      // `locking` now would flash Shell's still-loaded note content back
      // into view for that one render - exactly what the locking overlay
      // below exists to prevent (task 5.8's "secure content hiding while
      // locked").
    } catch (thrown) {
      setLocking(false);
      throw thrown;
    }
  }

  async function handleSyncNow() {
    if (forcingSync) return;
    setForcingSync(true);
    try {
      await syncNow();
      // EventSyncSummary (emitted by SyncNow in the desktop runtime) will
      // refresh this from the real backend state shortly. Set it locally
      // as well so the control immediately reflects the requested cycle
      // even if event delivery is delayed.
      setSyncStatusValue("active");
    } catch {
      setSyncModalOpen(true);
    } finally {
      setForcingSync(false);
    }
  }

  // Configurable automatic lock (task 5.8): resets on any user activity
  // and locks after autoLockMinutes of none. 0/null disarms it. handleLock
  // is read through a ref rather than listed as a dependency so this
  // effect (and its event-listener churn) does not re-run on every Shell
  // render - only when the configured duration itself changes.
  const handleLockRef = useRef(handleLock);
  handleLockRef.current = handleLock;

  useEffect(() => {
    if (!autoLockMinutes) return;
    let timer: number;
    const resetTimer = () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        void handleLockRef.current();
      }, autoLockMinutes * 60 * 1000);
    };
    const activityEvents = ["mousedown", "mousemove", "keydown", "wheel", "touchstart"] as const;
    for (const event of activityEvents) {
      window.addEventListener(event, resetTimer);
    }
    resetTimer();
    return () => {
      window.clearTimeout(timer);
      for (const event of activityEvents) {
        window.removeEventListener(event, resetTimer);
      }
    };
  }, [autoLockMinutes]);

  async function handleAutoLockChange(minutes: number) {
    const current = await getSettings();
    const updated = await updateSettings({ ...current, auto_lock_minutes: minutes });
    setAutoLockMinutes(updated.auto_lock_minutes);
  }

  // Ctrl+N/Cmd+N creates a note from anywhere in the shell. Read through a
  // ref (same pattern as handleLockRef above) so this listener is only ever
  // attached once, instead of being torn down and reattached on every
  // keystroke-driven Shell render.
  const handleCreateNoteRef = useRef(handleCreateNote);
  handleCreateNoteRef.current = handleCreateNote;

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "n") {
        event.preventDefault();
        void handleCreateNoteRef.current();
      }
    }
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, []);

  return (
    <main className="screen shell" aria-labelledby={shellTitleId}>
      <header className="shell-topbar">
        <div className="shell-topbar-lead">
          <button
            type="button"
            className="icon-button"
            aria-label={sidebarCollapsed ? t("shell.expand_sidebar") : t("shell.collapse_sidebar")}
            title={sidebarCollapsed ? t("shell.expand_sidebar") : t("shell.collapse_sidebar")}
            // Focus mode already hides the sidebar regardless of
            // sidebarCollapsed's own value (see shell-body's class list
            // below); disabling this button while it's active avoids a
            // click here silently changing state with no visible effect.
            disabled={focusMode}
            onClick={() => setSidebarCollapsed((current) => !current)}
          >
            ☰
          </button>
          <button
            type="button"
            className={`icon-button${focusMode ? " active" : ""}`}
            aria-label={focusMode ? t("shell.exit_focus_mode") : t("shell.enter_focus_mode")}
            title={focusMode ? t("shell.exit_focus_mode") : t("shell.enter_focus_mode")}
            aria-pressed={focusMode}
            onClick={() => setFocusMode((current) => !current)}
          >
            ⛶
          </button>
          <h1 id={shellTitleId}>{t("shell.title")}</h1>
        </div>
        <div className="shell-topbar-actions">
          {account.key_protection ? (
            <span className="key-protection-hint">
              <span aria-hidden="true">🔒</span>{" "}
              <span>
                {account.key_protection === "windows-hello"
                  ? t("shell.key_protection_hello")
                  : t("shell.key_protection_dpapi")}
              </span>
            </span>
          ) : null}
          <button
            type="button"
            className={`sync-status-pill sync-status-${syncStatusValue ?? "local_only"}`}
            aria-label={t("sync.open_button")}
            title={t("sync.open_button")}
            onClick={() => setSyncModalOpen(true)}
          >
            <span className="sync-status-dot" aria-hidden="true" />
            {syncStatusValue ? t(`sync.status_${syncStatusValue}`) : t("sync.open_button")}
          </button>
          <button
            type="button"
            className="icon-button sync-now-button"
            aria-label={t("sync.force_button")}
            title={t("sync.force_button")}
            aria-busy={forcingSync}
            disabled={forcingSync || syncStatusValue === null || syncStatusValue === "local_only"}
            onClick={() => void handleSyncNow()}
          >
            <span aria-hidden="true">↻</span>
          </button>
          <button
            type="button"
            className="icon-button"
            aria-label={t("settings.title")}
            title={t("settings.title")}
            onClick={() => setDataModalOpen(true)}
          >
            ⚙
          </button>
          <button
            type="button"
            className="icon-button"
            aria-label={t("shell.lock_button")}
            title={t("shell.lock_button")}
            onClick={() => void handleLock()}
            disabled={locking}
          >
            <span aria-hidden="true">🔒</span>
          </button>
        </div>
      </header>

      {dataModalOpen ? (
        <Modal title={t("settings.title")} onClose={() => setDataModalOpen(false)}>
          <label className="auto-lock-control">
            <span>{t("shell.auto_lock_label")}</span>
            <select
              value={autoLockMinutes ?? ""}
              disabled={autoLockMinutes === null}
              onChange={(event) => void handleAutoLockChange(Number(event.target.value))}
            >
              <option value={0}>{t("shell.auto_lock_never")}</option>
              <option value={5}>{t("shell.auto_lock_5min")}</option>
              <option value={15}>{t("shell.auto_lock_15min")}</option>
              <option value={30}>{t("shell.auto_lock_30min")}</option>
              <option value={60}>{t("shell.auto_lock_60min")}</option>
            </select>
          </label>
          <BackupsPanel onRestored={loadAll} />
          <ImportExportPanel onImported={loadAll} />
          <ShellIntegrationPanel />
          <DiagnosticsPanel />
        </Modal>
      ) : null}

      {syncModalOpen ? (
        <Modal title={t("sync.title")} onClose={() => setSyncModalOpen(false)}>
          <SyncPanel
            deviceId={account.device_id}
            onWorkspaceChanged={loadAll}
            onBeforeWorkspaceSwitch={async () => {
              await editorPaneRef.current?.flush();
            }}
          />
        </Modal>
      ) : null}

      {quickNoteOpen ? (
        <QuickNotePanel
          key={quickNoteSession}
          onClosed={() => {
            setQuickNoteOpen(false);
            loadAll();
          }}
        />
      ) : null}

      {locking ? (
        // Replaces every note-bearing element immediately, before the
        // flush/lock IPC calls above even resolve: task 5.8's "secure
        // content hiding while locked" covers this transition, not just
        // the already-swapped-out Unlock screen the parent shows once
        // onLocked() actually fires.
        <div className="shell-locking-overlay">
          <p>{t("shell.locking_message")}</p>
        </div>
      ) : error ? (
        <div className="shell-error">
          <p role="alert">{error}</p>
          <button type="button" onClick={loadAll}>
            {t("common.retry")}
          </button>
        </div>
      ) : (
        <div
          className={`shell-body${sidebarCollapsed || focusMode ? " sidebar-collapsed" : ""}${focusMode ? " focus-mode" : ""}`}
        >
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
              onCreateNote={(notebookId) => void handleCreateNote(notebookId)}
              onCreated={(notebook) => setNotebooks((current) => [...current, notebook])}
              onRenamed={handleNotebookRenamed}
              onDeleted={(notebookId) => {
                setNotebooks((current) => current.filter((notebook) => notebook.id !== notebookId));
                // The deleted notebook can no longer be a valid selection;
                // its notes are still reachable, just no longer filed
                // under it, so falling back to "All Notes" keeps them
                // visible instead of showing an empty, unselectable filter.
                if (selection.kind === "notebook" && selection.id === notebookId) {
                  searchBarRef.current?.clear();
                  setSelection({ kind: "all" });
                }
              }}
              onMoved={handleNotebookMoved}
              onNoteMoved={handleNoteMoved}
            />
            <TagList
              tags={tags}
              selectedId={selection.kind === "tag" ? selection.id : ""}
              onSelect={(id) => {
                searchBarRef.current?.clear();
                setSelection({ kind: "tag", id });
              }}
              onCreated={handleTagCreated}
              onDeleted={handleTagDeleted}
            />
          </aside>
          <section className="shell-notes">
            {noteActionError ? (
              <p className="error" role="alert">
                {noteActionError}
              </p>
            ) : null}
            <SearchBar
              ref={searchBarRef}
              tags={tags}
              notes={notes}
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
              noteMetaById={noteMetaById}
              highlightTerms={highlightTerms}
              emptyMessage={searchResults !== null ? t("search.no_results") : undefined}
            />
          </section>
          <section className="shell-detail">
            <NoteEditorPane
              ref={editorPaneRef}
              note={selectedNote}
              tags={tags}
              assignedTagIds={selectedNote ? (noteTagIds[selectedNote.id] ?? []) : []}
              onTitleCommitted={handleTitleCommitted}
              onDelete={(noteId) => void handleDeleteNote(noteId)}
              onToggleTag={handleToggleNoteTag}
              onCreateTag={handleCreateAndAssignTag}
              autoFocusNoteId={autoFocusNoteId}
              onAutoFocusConsumed={() => setAutoFocusNoteId("")}
              onDraftTouched={markDraftTouched}
              syncStatus={syncStatusValue}
              syncedAt={syncedAt}
              onOpenSync={() => setSyncModalOpen(true)}
            />
          </section>
        </div>
      )}
      {deletedNoteId ? (
        <UndoSnackbar
          message={t("shell.note_deleted")}
          onUndo={() => void handleUndoDelete()}
          onDismiss={() => {
            window.clearTimeout(deletedNoteTimerRef.current);
            setDeletedNoteId(null);
          }}
        />
      ) : null}
    </main>
  );
}
