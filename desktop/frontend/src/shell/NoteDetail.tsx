import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

export interface NoteDetailProps {
  note: main.NoteDTO | null;
}

/**
 * NoteDetail is a placeholder for the real editor pane (task 5.4): it
 * proves selection/routing reaches a concrete note without yet rendering
 * or editing its rich-text body.
 */
export function NoteDetail({ note }: NoteDetailProps) {
  const { t } = useI18n();

  if (!note) {
    return (
      <div className="note-detail note-detail-empty">
        <p>{t("shell.detail_placeholder")}</p>
      </div>
    );
  }

  return (
    <div className="note-detail">
      <h2>{note.title || t("shell.untitled_note")}</h2>
      <dl className="account-info">
        <dt>{t("shell.detail_created_label")}</dt>
        <dd>{new Date(note.created_unix_ms).toLocaleString()}</dd>
      </dl>
      <p className="hint">{t("shell.detail_placeholder")}</p>
    </div>
  );
}
