import "package:flutter/material.dart";

import "../strings.dart";
import "language_control.dart";

/// The note list screen's navigation drawer: the notebook tree (with
/// add/rename/delete per row and a "new note"/"new notebook" menu), the tag
/// list, and the language toggle. Extracted from `NotesShell.build` and its
/// `notebookTree()` helper (task 9.1) as a pure presentational region - all
/// notebook/tag/selection state stays owned by `_NotesShellState`, matching
/// desktop's `ShellNavigation` split from task 8.1. Each callback mutates
/// state and this widget closes the drawer afterward via `Navigator.pop`,
/// preserving the original inline behavior exactly.
class NotesNavigationDrawer extends StatelessWidget {
  const NotesNavigationDrawer({
    required this.strings,
    required this.notebooks,
    required this.tags,
    required this.selectedNotebook,
    required this.selectedTag,
    required this.language,
    required this.onLanguageChanged,
    required this.onSelectAllNotes,
    required this.onSelectNotebook,
    required this.onToggleTag,
    required this.onNewNote,
    required this.onNewNotebook,
    required this.onAddSubnotebook,
    required this.onRenameNotebook,
    required this.onDeleteNotebook,
    required this.onCreateTag,
    required this.onDeleteTag,
    super.key,
  });

  final Strings strings;
  final List<Map<String, dynamic>> notebooks;
  final List<Map<String, dynamic>> tags;
  final String? selectedNotebook;
  final String? selectedTag;
  final String language;
  final ValueChanged<String> onLanguageChanged;
  final VoidCallback onSelectAllNotes;
  final ValueChanged<String> onSelectNotebook;
  final ValueChanged<String> onToggleTag;
  final VoidCallback onNewNote;
  final VoidCallback onNewNotebook;
  final ValueChanged<String> onAddSubnotebook;
  final void Function(String id, String currentName) onRenameNotebook;
  final ValueChanged<String> onDeleteNotebook;
  final VoidCallback onCreateTag;
  final ValueChanged<String> onDeleteTag;

  List<Widget> _notebookTree(BuildContext context) {
    final visibleNotebooks = notebooks.where((item) => item["deleted"] != true);
    final byParent = <String, List<Map<String, dynamic>>>{};
    for (final item in visibleNotebooks) {
      final parent = (item["parent_id"] as String?) ?? "";
      byParent.putIfAbsent(parent, () => []).add(item);
    }
    List<Widget> render(String parentId, int depth) {
      final children = byParent[parentId] ?? const [];
      return [
        for (final item in children) ...[
          ListTile(
            contentPadding: EdgeInsets.only(left: 16.0 + depth * 20, right: 4),
            leading: const Icon(Icons.book_outlined),
            title: Text(item["name"] as String),
            selected: selectedNotebook == item["id"],
            onTap: () {
              onSelectNotebook(item["id"] as String);
              Navigator.pop(context);
            },
            trailing: PopupMenuButton<String>(
              icon: const Icon(Icons.more_vert),
              onSelected: (action) {
                switch (action) {
                  case "add":
                    onAddSubnotebook(item["id"] as String);
                  case "rename":
                    onRenameNotebook(
                      item["id"] as String,
                      item["name"] as String,
                    );
                  default:
                    onDeleteNotebook(item["id"] as String);
                }
              },
              itemBuilder:
                  (context) => [
                    PopupMenuItem(
                      value: "add",
                      child: Text(strings("new_notebook")),
                    ),
                    PopupMenuItem(
                      value: "rename",
                      child: Text(strings("rename")),
                    ),
                    PopupMenuItem(
                      value: "delete",
                      child: Text(strings("delete")),
                    ),
                  ],
            ),
          ),
          ...render(item["id"] as String, depth + 1),
        ],
      ];
    }

    return render("", 0);
  }

  @override
  Widget build(BuildContext context) {
    return NavigationDrawer(
      onDestinationSelected: (index) {
        // The only NavigationDrawerDestination below is "Notes" (index 0);
        // every other drawer row is a plain ListTile and does not count
        // toward this index.
        if (index == 0) {
          onSelectAllNotes();
          Navigator.pop(context);
        }
      },
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 8, 16),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              // Expanded, with an ellipsis, so a long localized title at a
              // large accessibility text scale shrinks to fit instead of
              // pushing the menu button off the drawer's fixed width
              // (task 9.7).
              Expanded(
                child: Text(
                  strings("notebooks"),
                  overflow: TextOverflow.ellipsis,
                  style: Theme.of(context).textTheme.titleMedium,
                ),
              ),
              PopupMenuButton<String>(
                icon: const Icon(Icons.more_vert),
                tooltip: strings("more_actions"),
                onSelected: (action) {
                  if (action == "new_note") {
                    onNewNote();
                  } else {
                    onNewNotebook();
                  }
                },
                itemBuilder:
                    (context) => [
                      PopupMenuItem(
                        value: "new_note",
                        child: Text(strings("new_note")),
                      ),
                      PopupMenuItem(
                        value: "new_notebook",
                        child: Text(strings("new_notebook")),
                      ),
                    ],
              ),
            ],
          ),
        ),
        NavigationDrawerDestination(
          icon: const Icon(Icons.notes),
          label: Text(strings("notes")),
        ),
        ..._notebookTree(context),
        const Divider(),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 0, 8, 0),
          child: Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Expanded(
                child: Text(strings("tags"), overflow: TextOverflow.ellipsis),
              ),
              IconButton(
                tooltip: strings("new_tag"),
                onPressed: onCreateTag,
                icon: const Icon(Icons.add, size: 20),
              ),
            ],
          ),
        ),
        ...tags
            .where((item) => item["deleted"] != true)
            .map(
              (item) => ListTile(
                leading: const Icon(Icons.tag),
                title: Text(item["name"] as String),
                selected: selectedTag == item["id"],
                onTap: () {
                  onToggleTag(item["id"] as String);
                  Navigator.pop(context);
                },
                trailing: IconButton(
                  tooltip: strings("delete"),
                  icon: const Icon(Icons.close, size: 18),
                  onPressed: () => onDeleteTag(item["id"] as String),
                ),
              ),
            ),
        ListTile(
          title: LanguageControl(
            language: language,
            onChanged: onLanguageChanged,
          ),
        ),
      ],
    );
  }
}
