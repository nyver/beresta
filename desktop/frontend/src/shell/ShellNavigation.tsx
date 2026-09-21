import { main } from "../../wailsjs/go/models";
import { NotebookTree, type NotebookTreeProps } from "./NotebookTree";
import { TagList, type TagListProps } from "./TagList";

export interface ShellNavigationProps {
  notebooks: main.NotebookDTO[];
  /** "" selects "All Notes"; null means a tag is selected instead. */
  selectedNotebookId: string | null;
  onSelectNotebook: NotebookTreeProps["onSelect"];
  onCreateNoteInNotebook: NotebookTreeProps["onCreateNote"];
  onNotebookCreated: NotebookTreeProps["onCreated"];
  onNotebookRenamed: NotebookTreeProps["onRenamed"];
  onNotebookDeleted: NotebookTreeProps["onDeleted"];
  onNotebookMoved: NotebookTreeProps["onMoved"];
  onNoteMoved: NotebookTreeProps["onNoteMoved"];
  tags: main.TagDTO[];
  /** "" means no tag is selected. */
  selectedTagId: string;
  onSelectTag: TagListProps["onSelect"];
  onTagCreated: TagListProps["onCreated"];
  onTagDeleted: TagListProps["onDeleted"];
}

/**
 * ShellNavigation is the desktop shell's persistent navigation region
 * (task 8.1, specs/windows-desktop-client's "Stable three-pane desktop
 * workspace" requirement): the notebook tree and tag list that stay visible
 * beside the note list and editor regions.
 */
export function ShellNavigation({
  notebooks,
  selectedNotebookId,
  onSelectNotebook,
  onCreateNoteInNotebook,
  onNotebookCreated,
  onNotebookRenamed,
  onNotebookDeleted,
  onNotebookMoved,
  onNoteMoved,
  tags,
  selectedTagId,
  onSelectTag,
  onTagCreated,
  onTagDeleted,
}: ShellNavigationProps) {
  return (
    <aside className="shell-sidebar">
      <NotebookTree
        notebooks={notebooks}
        selectedId={selectedNotebookId}
        onSelect={onSelectNotebook}
        onCreateNote={onCreateNoteInNotebook}
        onCreated={onNotebookCreated}
        onRenamed={onNotebookRenamed}
        onDeleted={onNotebookDeleted}
        onMoved={onNotebookMoved}
        onNoteMoved={onNoteMoved}
      />
      <TagList
        tags={tags}
        selectedId={selectedTagId}
        onSelect={onSelectTag}
        onCreated={onTagCreated}
        onDeleted={onTagDeleted}
      />
    </aside>
  );
}
