import Quill from "quill";
import "quill/dist/quill.snow.css";
import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";
import { QuillBinding } from "y-quill";

import { useI18n } from "../i18n";
import { useNoteDocument } from "./useNoteDocument";

export interface NoteEditorHandle {
  /** See useNoteDocument's flush doc comment. */
  flush: (title?: string) => Promise<boolean>;
}

export interface NoteEditorProps {
  noteId: string;
  /** Called with one or more pasted image files when the user pastes an
   * image from the clipboard. The paste is intercepted before Quill's own
   * clipboard module ever sees it: inline images have no representation in
   * the canonical Markdown projection (see TOOLBAR_FORMATS above), so a
   * pasted image becomes a note attachment instead of an inline blot. */
  onAttachFiles?: (files: File[]) => void;
}

// The Yjs root name for a note's body, matching core/account's
// noteBodyRoot ("body" in notecommands.go). The two are not type-checked
// against each other across the Go/TS boundary, so a typo here would
// silently create/read the wrong Y.Text root instead of failing loudly.
const NOTE_BODY_ROOT = "body";

// Restricted to exactly the formatting keys core/sync/yjsadapter's
// canonical Markdown projection understands (see Attr* in
// core/sync/yjsadapter/document.go). Any other Quill format (underline,
// color, font, alignment, images, checklists, ...) would round-trip
// through the CRDT harmlessly but silently vanish from Markdown export,
// so it is deliberately not offered in the toolbar.
const TOOLBAR_FORMATS = [
  [{ header: [1, 2, 3, false] }],
  ["bold", "italic", "strike", "code"],
  [{ list: "ordered" }, { list: "bullet" }],
  ["blockquote", "code-block", "link"],
  ["clean"],
];

/**
 * NoteEditor is the Yjs-backed WYSIWYG body editor: y-quill binds a Quill
 * instance directly to the note's Y.Text (see useNoteDocument), so typing
 * updates the CRDT and CRDT changes update the editor. Undo/redo is
 * Quill's own history module in `userOnly` mode (Collaboration extensions
 * like TipTap's need a separate Y.UndoManager because remote peers'
 * changes share the undo stack; this document has exactly one local
 * writer, so Quill's built-in per-user history is sufficient and needs no
 * extra wiring).
 */
export const NoteEditor = forwardRef<NoteEditorHandle, NoteEditorProps>(function NoteEditor(
  { noteId, onAttachFiles },
  ref,
) {
  const { t, errorMessage } = useI18n();
  const { ydoc, ready, error, flush } = useNoteDocument(noteId);
  const containerRef = useRef<HTMLDivElement>(null);
  const onAttachFilesRef = useRef(onAttachFiles);
  onAttachFilesRef.current = onAttachFiles;

  useImperativeHandle(ref, () => ({ flush }), [flush]);

  useEffect(() => {
    if (!ready || !ydoc || !containerRef.current) return;
    const container = containerRef.current;

    // Capture-phase, so this runs before Quill's own bubble-phase paste
    // listener on its editable root ever sees the event: preventDefault
    // here stops Quill's clipboard module from turning a pasted image into
    // an inline blot it cannot export.
    const interceptImagePaste = (event: ClipboardEvent) => {
      const files = Array.from(event.clipboardData?.files ?? []).filter((file) =>
        file.type.startsWith("image/"),
      );
      if (files.length === 0) return;
      event.preventDefault();
      event.stopPropagation();
      onAttachFilesRef.current?.(files);
    };
    container.addEventListener("paste", interceptImagePaste, true);

    const quill = new Quill(container, {
      theme: "snow",
      placeholder: t("editor.placeholder"),
      modules: {
        toolbar: TOOLBAR_FORMATS,
        history: { userOnly: true },
      },
    });
    const binding = new QuillBinding(ydoc.getText(NOTE_BODY_ROOT), quill);

    return () => {
      binding.destroy();
      container.removeEventListener("paste", interceptImagePaste, true);
    };
    // t is stable within one language session; re-creating Quill on every
    // locale switch is unnecessary churn and would drop the user's
    // selection/undo stack mid-edit.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, ydoc]);

  // Only a load failure (before the editor ever mounted) blocks rendering
  // entirely. A *later* error - a debounced autosave that failed after
  // the user was already typing - must never replace the live editor
  // with this message: that would tear down the mounted Quill instance
  // (and, with it, visibility into whatever the user just typed) over a
  // transient save hiccup. That case is surfaced as a non-blocking banner
  // below instead, alongside the still-editable document.
  if (!ready && error) {
    return (
      <p className="error" role="alert">
        {errorMessage(error)}
      </p>
    );
  }
  if (!ready) {
    return <p>{t("common.loading")}</p>;
  }
  return (
    <div className="note-editor">
      {error ? (
        <p className="error note-editor-save-error" role="alert">
          {errorMessage(error)}
        </p>
      ) : null}
      {/* Keyed by noteId: Quill takes over its container's DOM on
          construction and has no supported way to be re-pointed at a
          different Y.Text in place, so switching notes must remount a
          fresh container rather than reuse one a previous Quill instance
          already took over. */}
      <div key={noteId} ref={containerRef} className="note-editor-quill" />
    </div>
  );
});
