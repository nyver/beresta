import { useMemo, useState } from "react";

import { createTag, deleteTag, unwrapError } from "../api";
import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";
import { KebabMenu } from "./KebabMenu";

export interface TagListProps {
  tags: main.TagDTO[];
  /** "" means no tag is selected. */
  selectedId: string;
  onSelect: (tagId: string) => void;
  /** Called after a new tag has been durably created, so the caller
   * (Shell) can add it to its own tags state. */
  onCreated: (tag: main.TagDTO) => void;
  /** Called after a tag has been durably tombstoned, so the caller (Shell)
   * can drop it from its own tags state. */
  onDeleted: (tagId: string) => void;
}

export function TagList({ tags, selectedId, onSelect, onCreated, onDeleted }: TagListProps) {
  const { t, errorMessage } = useI18n();
  const [newName, setNewName] = useState("");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  // Only one row's delete confirmation is shown at a time, mirroring
  // NotebookTree's confirmingDeleteId.
  const [confirmingDeleteId, setConfirmingDeleteId] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const visible = useMemo(
    () => tags.filter((tag) => !tag.deleted).sort((a, b) => a.name.localeCompare(b.name)),
    [tags],
  );

  async function handleCreate() {
    const name = newName.trim();
    if (!name || creating) return;
    setCreating(true);
    setCreateError(null);
    try {
      const tag = await createTag(name);
      onCreated(tag);
      setNewName("");
    } catch (thrown: unknown) {
      setCreateError(errorMessage(unwrapError(thrown)));
    } finally {
      setCreating(false);
    }
  }

  async function handleDelete(id: string) {
    setDeleting(true);
    setDeleteError(null);
    try {
      await deleteTag(id);
      onDeleted(id);
      setConfirmingDeleteId(null);
    } catch (thrown: unknown) {
      setDeleteError(errorMessage(unwrapError(thrown)));
    } finally {
      setDeleting(false);
    }
  }

  return (
    <nav aria-label={t("shell.tags_section")} className="tag-list">
      <h2>{t("shell.tags_section")}</h2>
      {visible.length > 0 ? (
        <ul>
          {visible.map((tag) => (
            <li key={tag.id}>
              <button
                type="button"
                className={`tree-row${tag.id === selectedId ? " selected" : ""}`}
                onClick={() => onSelect(tag.id)}
              >
                {tag.name}
              </button>
              {confirmingDeleteId === tag.id ? (
                <span className="tree-row-delete-confirm">
                  <button type="button" disabled={deleting} onClick={() => void handleDelete(tag.id)}>
                    {deleting ? t("shell.deleting") : t("shell.delete_confirm_button")}
                  </button>
                  <button
                    type="button"
                    className="link-button"
                    disabled={deleting}
                    onClick={() => setConfirmingDeleteId(null)}
                  >
                    {t("common.cancel")}
                  </button>
                </span>
              ) : (
                <KebabMenu
                  label={`${t("shell.tag_actions")}: ${tag.name}`}
                  items={[
                    {
                      label: t("shell.delete_tag"),
                      onSelect: () => {
                        setDeleteError(null);
                        setConfirmingDeleteId(tag.id);
                      },
                      destructive: true,
                    },
                  ]}
                />
              )}
            </li>
          ))}
        </ul>
      ) : null}
      {deleteError ? (
        <p className="error" role="alert">
          {deleteError}
        </p>
      ) : null}
      <form
        className="tag-create"
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
      {createError ? (
        <p className="error" role="alert">
          {createError}
        </p>
      ) : null}
    </nav>
  );
}
