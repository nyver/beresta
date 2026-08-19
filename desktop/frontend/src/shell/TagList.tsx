import { useMemo } from "react";

import { useI18n } from "../i18n";
import { main } from "../../wailsjs/go/models";

export interface TagListProps {
  tags: main.TagDTO[];
  /** "" means no tag is selected. */
  selectedId: string;
  onSelect: (tagId: string) => void;
}

export function TagList({ tags, selectedId, onSelect }: TagListProps) {
  const { t } = useI18n();
  const visible = useMemo(
    () => tags.filter((tag) => !tag.deleted).sort((a, b) => a.name.localeCompare(b.name)),
    [tags],
  );

  if (visible.length === 0) return null;

  return (
    <nav aria-label={t("shell.tags_section")} className="tag-list">
      <h2>{t("shell.tags_section")}</h2>
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
          </li>
        ))}
      </ul>
    </nav>
  );
}
