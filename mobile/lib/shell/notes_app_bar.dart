import "package:flutter/material.dart";

import "../strings.dart";
import "sync_status.dart";

/// The note list screen's app bar: sync-now, synchronization status (which
/// also opens the Synchronization settings group), lock, and settings
/// actions. Extracted from `NotesShell.build` (task 9.1) as a pure
/// presentational region - all state (sync status value, in-flight sync)
/// stays owned by `_NotesShellState`, matching desktop's `ShellTopBar` split
/// from task 8.1.
class NotesAppBar extends StatelessWidget implements PreferredSizeWidget {
  const NotesAppBar({
    required this.strings,
    required this.syncingWorkspace,
    required this.syncStatusValue,
    required this.onSync,
    required this.onShowSyncSettings,
    required this.onLock,
    required this.onShowSettings,
    super.key,
  });

  final Strings strings;
  final bool syncingWorkspace;
  final String syncStatusValue;
  final VoidCallback onSync;
  final VoidCallback onShowSyncSettings;
  final VoidCallback onLock;
  final VoidCallback onShowSettings;

  @override
  Size get preferredSize => const Size.fromHeight(kToolbarHeight);

  @override
  Widget build(BuildContext context) {
    return AppBar(
      title: Text(strings("notes")),
      actions: [
        IconButton(
          tooltip: strings("sync"),
          onPressed: syncingWorkspace ? null : onSync,
          icon:
              syncingWorkspace
                  ? const SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                  : const Icon(Icons.sync),
        ),
        IconButton(
          tooltip:
              "${strings("server")}: "
              "${strings("sync_status_$syncStatusValue")}",
          onPressed: onShowSyncSettings,
          icon: Icon(
            syncStatusIcon(syncStatusValue),
            color: syncStatusColor(context, syncStatusValue),
          ),
        ),
        IconButton(
          tooltip: strings("lock"),
          onPressed: onLock,
          icon: const Icon(Icons.lock_outline),
        ),
        IconButton(
          tooltip: strings("settings"),
          onPressed: onShowSettings,
          icon: const Icon(Icons.settings_outlined),
        ),
      ],
    );
  }
}
