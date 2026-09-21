import "package:flutter/material.dart";

import "design_tokens.dart";

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

/// Colors mirror desktop's `.sync-status-*` rules (styles.css) via
/// design/tokens.json's `color.status.*` roles, rather than Material's
/// generic red/green/orange, so the same sync state reads as the same color
/// on both clients. Desktop treats "pending" as neutral (waiting, not yet a
/// problem) and reserves the warning color for "offline"/"retrying"
/// specifically - kept distinct here too instead of grouping all three.
Color syncStatusColor(BuildContext context, String status) {
  switch (status) {
    case "current":
      return AppColors.statusSuccess;
    case "active":
      return AppColors.statusInfo;
    case "offline":
    case "retrying":
      return AppColors.statusWarningStrong;
    case "pending":
      return AppColors.statusNeutral;
    case "action_required":
      return AppColors.statusDanger;
    default:
      return AppColors.statusNeutral;
  }
}
