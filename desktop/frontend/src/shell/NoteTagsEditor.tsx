import { useEffect, useRef, useState } from "react";

import { unwrapError } from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

export interface NoteTagsEditorProps {
  tags: main.TagDTO[];
  /** Tag ids currently assigned to the open note. */
  assignedTagIds: string[];
  /** Assigns or unassigns one tag on the open note (App.SetNoteTag). */
  onToggle: (tagId: string, present: boolean) => Promise<void>;
  /** Creates a new workspace tag and immediately assigns it to the open
   * note. */
  onCreateAndAssign: (name: string) => Promise<void>;
}

/**
 * NoteTagsEditor shows the open note's assigned tags as removable chips,
 * plus a popover (existing tags as checkboxes, and a name field to create a
 * new one) for adding more. This is the only place in the app that actually
 * assigns a tag to a note - TagList/SearchBar only browse and filter by
 * tags that already exist.
 */
export function NoteTagsEditor({ tags, assignedTagIds, onToggle, onCreateAndAssign }: NoteTagsEditorProps) {
  const { t, errorMessage } = useI18n();
  const [open, setOpen] = useState(false);
  const [newName, setNewName] = useState("");
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function handlePointerDown(event: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [open]);

  const visibleTags = tags.filter((tag) => !tag.deleted);
  const assigned = visibleTags.filter((tag) => assignedTagIds.includes(tag.id));
  const assignedSet = new Set(assignedTagIds);

  async function handleToggle(tagId: string, present: boolean) {
    setError(null);
    try {
      await onToggle(tagId, present);
    } catch (thrown: unknown) {
      setError(errorMessage(unwrapError(thrown)));
    }
  }

  async function handleCreate() {
    const name = newName.trim();
    if (!name || creating) return;
    setCreating(true);
    setError(null);
    try {
      await onCreateAndAssign(name);
      setNewName("");
    } catch (thrown: unknown) {
      setError(errorMessage(unwrapError(thrown)));
    } finally {
      setCreating(false);
    }
  }

  return (
    <div className="note-tags-editor" ref={containerRef}>
      {assigned.map((tag) => (
        <span key={tag.id} className="note-tag-chip">
          {tag.name}
          <button
            type="button"
            aria-label={t("shell.remove_tag")}
            onClick={() => void handleToggle(tag.id, false)}
          >
            ×
          </button>
        </span>
      ))}
      <button
        type="button"
        className="note-tags-add-button"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
      >
        + {t("shell.tags_add_button")}
      </button>
      {open ? (
        <div className="note-tags-popover" role="menu">
          {visibleTags.length > 0 ? (
            <ul className="note-tags-popover-list">
              {visibleTags.map((tag) => (
                <li key={tag.id}>
                  <label>
                    <input
                      type="checkbox"
                      checked={assignedSet.has(tag.id)}
                      onChange={(event) => void handleToggle(tag.id, event.target.checked)}
                    />
                    {tag.name}
                  </label>
                </li>
              ))}
            </ul>
          ) : (
            <p className="hint">{t("shell.tags_none")}</p>
          )}
          <form
            className="note-tags-popover-create"
            onSubmit={(event) => {
              event.preventDefault();
              void handleCreate();
            }}
          >
            <input
              type="text"
              value={newName}
              onChange={(event) => setNewName(event.target.value)}
              placeholder={t("shell.new_tag_placeholder")}
              aria-label={t("shell.new_tag_placeholder")}
            />
            <button type="submit" disabled={creating || !newName.trim()}>
              {t("shell.new_tag_button")}
            </button>
          </form>
          {error ? (
            <p className="error" role="alert">
              {error}
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
