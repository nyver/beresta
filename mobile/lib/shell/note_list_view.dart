import "package:flutter/material.dart";

import "../strings.dart";

/// The note list screen's body: the search field, an inline error banner,
/// and the loading/empty/list states for the currently visible notes.
/// Extracted from `NotesShell.build` (task 9.1) as a pure presentational
/// region - `notes` is the already-filtered (notebook/tag/search) list, and
/// all other note-list state stays owned by `_NotesShellState`, matching
/// desktop's `ShellNoteListRegion` split from task 8.1.
class NoteListBody extends StatelessWidget {
  const NoteListBody({
    required this.strings,
    required this.notes,
    required this.loading,
    required this.error,
    required this.onSearch,
    required this.onDismissError,
    required this.onOpenNote,
    required this.onMoveNote,
    super.key,
  });

  final Strings strings;
  final List<Map<String, dynamic>> notes;
  final bool loading;
  final String? error;
  final ValueChanged<String> onSearch;
  final VoidCallback onDismissError;
  final ValueChanged<String> onOpenNote;
  final ValueChanged<String> onMoveNote;

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.all(12),
          child: SearchBar(
            hintText: strings("search"),
            leading: const Icon(Icons.search),
            onChanged: onSearch,
          ),
        ),
        if (error != null)
          MaterialBanner(
            content: Text(error!),
            actions: [
              TextButton(onPressed: onDismissError, child: const Text("OK")),
            ],
          ),
        Expanded(
          child:
              loading
                  ? const Center(child: CircularProgressIndicator())
                  : notes.isEmpty
                  ? Center(child: Text(strings("empty")))
                  : ListView.builder(
                    itemCount: notes.length,
                    itemBuilder: (context, index) {
                      final note = notes[index];
                      final title = note["title"] as String;
                      return ListTile(
                        leading: Icon(
                          note["pinned"] == true
                              ? Icons.push_pin
                              : Icons.description_outlined,
                        ),
                        title: Text(
                          title.isEmpty ? strings("untitled") : title,
                        ),
                        subtitle: Text(
                          DateTime.fromMillisecondsSinceEpoch(
                            (note["updated_unix_ms"] as int?) ??
                                (note["created_unix_ms"] as int),
                          ).toLocal().toString(),
                        ),
                        trailing: IconButton(
                          tooltip: strings("move_to_notebook"),
                          icon: const Icon(Icons.drive_file_move_outline),
                          onPressed: () => onMoveNote(note["id"] as String),
                        ),
                        onTap: () => onOpenNote(note["id"] as String),
                      );
                    },
                  ),
        ),
      ],
    );
  }
}
