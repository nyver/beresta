import "package:flutter/material.dart";

/// Maps a core/presentation.SyncState value ("local_only", "current",
/// "active", "offline", "pending", "retrying", "action_required") - the
/// same seven-state model desktop renders (see core/syncsummary.Summarize)
/// - to the icon shown next to it in the sync UI.
IconData syncStatusIcon(String status) {
  switch (status) {
    case "current":
      return Icons.cloud_done_outlined;
    case "active":
      return Icons.cloud_sync_outlined;
    case "offline":
    case "retrying":
      return Icons.cloud_off_outlined;
    case "pending":
      return Icons.cloud_upload_outlined;
    case "action_required":
      return Icons.error_outline;
    default:
      return Icons.cloud_outlined;
  }
}

Color syncStatusColor(BuildContext context, String status) {
  final scheme = Theme.of(context).colorScheme;
  switch (status) {
    case "current":
      return Colors.green;
    case "active":
      return scheme.primary;
    case "offline":
    case "retrying":
    case "pending":
      return Colors.orange;
    case "action_required":
      return scheme.error;
    default:
      return scheme.onSurfaceVariant;
  }
}
