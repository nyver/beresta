import "dart:async";
import "dart:typed_data";

import "package:flutter/foundation.dart" show kDebugMode;
import "package:flutter/material.dart";
import "package:flutter/services.dart"
    show Clipboard, ClipboardData, PlatformException;
import "package:flutter_localizations/flutter_localizations.dart";
import "package:flutter_quill/flutter_quill.dart";

import "core_gateway.dart";
import "markdown_delta.dart";
import "strings.dart";

/// Renders a localized error with the underlying platform failure appended
/// in debug builds, so a real device can be diagnosed without attaching a
/// debugger or reading logcat.
String describeFailure(Strings strings, Object failure) {
  final base = strings("error");
  if (!kDebugMode) return base;
  if (failure is PlatformException) {
    final message = failure.message;
    final detail =
        message == null || message.isEmpty
            ? failure.code
            : "${failure.code}: $message";
    return "$base ($detail)";
  }
  return "$base ($failure)";
}

// Sync requests are intentionally best-effort at lifecycle boundaries and
// after local writes: the coordinator coalesces concurrent triggers and
// retries transport failures in the background.
void requestCurrentWorkspaceSync(CoreGateway gateway) {
  unawaited(gateway.syncNow().catchError((_) {}));
}

/// Reports whether the free-text search box already contains one of the
/// backend filter language's special tokens (tag:/after:/before:/
/// deleted:true), mirroring desktop's identical check in SearchBar.tsx.
/// Without this, such a query would be misread as a literal, tokens-and-all
/// substring to match against note titles instead of being sent to the
/// backend to parse as a filter.
bool containsQueryToken(String text) {
  return text.trim().split(RegExp(r"\s+")).any(
    (word) =>
        word.startsWith("tag:") ||
        word.startsWith("after:") ||
        word.startsWith("before:") ||
        word == "deleted:true",
  );
}

/// Maps a core/transport.Status value ("disabled", "offline", "active",
/// "current", "failed") to the icon shown next to it in the sync UI.
IconData syncStatusIcon(String status) {
  switch (status) {
    case "current":
      return Icons.cloud_done_outlined;
    case "active":
      return Icons.cloud_sync_outlined;
    case "offline":
      return Icons.cloud_off_outlined;
    case "failed":
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
      return Colors.orange;
    case "failed":
      return scheme.error;
    default:
      return scheme.onSurfaceVariant;
  }
}

class BerestaApp extends StatefulWidget {
  const BerestaApp({super.key, this.gateway});

  final CoreGateway? gateway;

  @override
  State<BerestaApp> createState() => _BerestaAppState();
}

class _BerestaAppState extends State<BerestaApp> {
  late final CoreGateway gateway = widget.gateway ?? MethodChannelCore();
  String language = "en";
  int sessionGeneration = 0;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      debugShowCheckedModeBanner: false,
      title: "Beresta",
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(seedColor: const Color(0xFF754C29)),
        useMaterial3: true,
      ),
      // The note editor's flutter_quill toolbar reads its tooltip strings
      // from FlutterQuillLocalizations.of(context), which throws if no
      // delegate is registered - unrelated to this app's own hand-rolled
      // Strings lookup (see strings.dart), but required for the toolbar to
      // work at all.
      locale: Locale(language),
      supportedLocales: const [Locale("en"), Locale("ru")],
      localizationsDelegates: const [
        FlutterQuillLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      home: AppLifecycleLock(
        gateway: gateway,
        onSessionLocked: () => setState(() => sessionGeneration += 1),
        child: SessionRoot(
          key: ValueKey(sessionGeneration),
          gateway: gateway,
          language: language,
          onLanguageChanged: (value) => setState(() => language = value),
        ),
      ),
    );
  }
}

class AppLifecycleLock extends StatefulWidget {
  const AppLifecycleLock({
    required this.gateway,
    required this.onSessionLocked,
    required this.child,
    super.key,
  });

  final CoreGateway gateway;
  final VoidCallback onSessionLocked;
  final Widget child;

  @override
  State<AppLifecycleLock> createState() => _AppLifecycleLockState();
}

class _AppLifecycleLockState extends State<AppLifecycleLock>
    with WidgetsBindingObserver {
  Timer? lockTimer;
  bool obscured = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.inactive ||
        state == AppLifecycleState.paused ||
        state == AppLifecycleState.hidden ||
        state == AppLifecycleState.detached) {
      requestCurrentWorkspaceSync(widget.gateway);
    }
    if (state == AppLifecycleState.paused ||
        state == AppLifecycleState.hidden) {
      if (!obscured) setState(() => obscured = true);
      if (lockTimer?.isActive != true) {
        lockTimer = Timer(const Duration(minutes: 5), () async {
          await widget.gateway.lock();
          if (mounted) widget.onSessionLocked();
        });
      }
    } else if (state == AppLifecycleState.resumed) {
      lockTimer?.cancel();
      unawaited(_resume());
    } else if (!obscured) {
      setState(() => obscured = true);
    }
  }

  Future<void> _resume() async {
    try {
      final status = await widget.gateway.status();
      if (status["unlocked"] == true) {
        requestCurrentWorkspaceSync(widget.gateway);
      } else {
        widget.onSessionLocked();
      }
    } catch (_) {
      widget.onSessionLocked();
    }
    if (mounted) setState(() => obscured = false);
  }

  @override
  void dispose() {
    requestCurrentWorkspaceSync(widget.gateway);
    WidgetsBinding.instance.removeObserver(this);
    lockTimer?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (obscured) {
      return const Scaffold(
        backgroundColor: Color(0xFF2B2118),
        body: SizedBox.expand(),
      );
    }
    return widget.child;
  }
}

class SessionRoot extends StatefulWidget {
  const SessionRoot({
    required this.gateway,
    required this.language,
    required this.onLanguageChanged,
    super.key,
  });

  final CoreGateway gateway;
  final String language;
  final ValueChanged<String> onLanguageChanged;

  @override
  State<SessionRoot> createState() => _SessionRootState();
}

class _SessionRootState extends State<SessionRoot> {
  bool? unlocked;
  bool accountExists = false;
  bool deviceUnlockAvailable = false;

  @override
  void initState() {
    super.initState();
    loadSession();
  }

  Future<void> loadSession() async {
    try {
      final status = await widget.gateway.status();
      final isUnlocked = status["unlocked"] == true;
      final hasAccount = status["account_exists"] == true;
      final canUseDeviceUnlock = status["device_unlock_available"] == true;
      if (!isUnlocked && hasAccount && canUseDeviceUnlock) {
        try {
          await widget.gateway.unlockWithDeviceAuthentication();
          if (mounted) {
            setState(() {
              unlocked = true;
              accountExists = true;
              deviceUnlockAvailable = true;
            });
          }
          return;
        } catch (_) {
          // The user may cancel the platform prompt; passphrase unlock stays
          // available and the device-authentication action can be retried.
        }
      }
      if (mounted) {
        setState(() {
          unlocked = isUnlocked;
          accountExists = hasAccount;
          deviceUnlockAvailable = canUseDeviceUnlock;
        });
      }
    } catch (_) {
      if (mounted) setState(() => unlocked = false);
    }
  }

  Future<void> unlockWithDeviceAuthentication() async {
    await widget.gateway.unlockWithDeviceAuthentication();
    if (mounted) setState(() => unlocked = true);
  }

  @override
  Widget build(BuildContext context) {
    final strings = Strings(widget.language);
    if (unlocked == null) {
      return const Scaffold(body: Center(child: CircularProgressIndicator()));
    }
    if (!unlocked!) {
      return OnboardingScreen(
        gateway: widget.gateway,
        strings: strings,
        language: widget.language,
        onLanguageChanged: widget.onLanguageChanged,
        accountExists: accountExists,
        deviceUnlockAvailable: deviceUnlockAvailable,
        onDeviceUnlock: unlockWithDeviceAuthentication,
        onUnlocked: () => setState(() => unlocked = true),
      );
    }
    return NotesShell(
      gateway: widget.gateway,
      strings: strings,
      language: widget.language,
      onLanguageChanged: widget.onLanguageChanged,
      onLocked: () => setState(() => unlocked = false),
    );
  }
}

class OnboardingScreen extends StatefulWidget {
  const OnboardingScreen({
    required this.gateway,
    required this.strings,
    required this.language,
    required this.onLanguageChanged,
    required this.onUnlocked,
    this.accountExists = false,
    this.deviceUnlockAvailable = false,
    this.onDeviceUnlock,
    super.key,
  });

  final CoreGateway gateway;
  final Strings strings;
  final String language;
  final ValueChanged<String> onLanguageChanged;
  final VoidCallback onUnlocked;
  final bool accountExists;
  final bool deviceUnlockAvailable;
  final Future<void> Function()? onDeviceUnlock;

  @override
  State<OnboardingScreen> createState() => _OnboardingScreenState();
}

class _OnboardingScreenState extends State<OnboardingScreen> {
  final passphrase = TextEditingController();
  bool busy = false;
  String? error;

  Future<void> submit(bool create) async {
    if (passphrase.text.isEmpty) return;
    setState(() {
      busy = true;
      error = null;
    });
    try {
      if (create) {
        await widget.gateway.createAccount(passphrase.text);
      } else {
        await widget.gateway.unlockAccount(passphrase.text);
      }
      widget.onUnlocked();
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    } finally {
      passphrase.clear();
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> unlockWithDeviceAuthentication() async {
    if (busy || widget.onDeviceUnlock == null) return;
    setState(() {
      busy = true;
      error = null;
    });
    try {
      await widget.onDeviceUnlock!();
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  void dispose() {
    passphrase.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 480),
            child: ListView(
              padding: const EdgeInsets.all(24),
              shrinkWrap: true,
              children: [
                const Icon(Icons.eco_outlined, size: 64),
                const SizedBox(height: 12),
                Text(
                  "Beresta",
                  textAlign: TextAlign.center,
                  style: Theme.of(context).textTheme.headlineLarge,
                ),
                Text(widget.strings("tagline"), textAlign: TextAlign.center),
                const SizedBox(height: 24),
                TextField(
                  controller: passphrase,
                  obscureText: true,
                  enableSuggestions: false,
                  autocorrect: false,
                  decoration: InputDecoration(
                    labelText: widget.strings("passphrase"),
                    border: const OutlineInputBorder(),
                  ),
                ),
                const SizedBox(height: 12),
                if (widget.accountExists)
                  FilledButton(
                    onPressed: busy ? null : () => submit(false),
                    child: Text(widget.strings("unlock")),
                  )
                else ...[
                  FilledButton(
                    onPressed: busy ? null : () => submit(true),
                    child: Text(widget.strings("create")),
                  ),
                  TextButton(
                    onPressed: busy ? null : () => submit(false),
                    child: Text(widget.strings("unlock")),
                  ),
                ],
                if (widget.accountExists && widget.deviceUnlockAvailable) ...[
                  const SizedBox(height: 12),
                  OutlinedButton.icon(
                    onPressed: busy ? null : unlockWithDeviceAuthentication,
                    icon: const Icon(Icons.fingerprint),
                    label: Text(widget.strings("device_unlock")),
                  ),
                ],
                Text(
                  widget.accountExists
                      ? widget.strings("returning_hint")
                      : widget.strings("local_hint"),
                  textAlign: TextAlign.center,
                ),
                if (error != null)
                  Semantics(
                    liveRegion: true,
                    child: Text(
                      error!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                  ),
                _LanguageControl(
                  language: widget.language,
                  onChanged: widget.onLanguageChanged,
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _LanguageControl extends StatelessWidget {
  const _LanguageControl({required this.language, required this.onChanged});

  final String language;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context) {
    return Align(
      child: SegmentedButton<String>(
        segments: const [
          ButtonSegment(value: "en", label: Text("EN")),
          ButtonSegment(value: "ru", label: Text("RU")),
        ],
        selected: {language},
        onSelectionChanged: (value) => onChanged(value.first),
      ),
    );
  }
}

class NotesShell extends StatefulWidget {
  const NotesShell({
    required this.gateway,
    required this.strings,
    required this.language,
    required this.onLanguageChanged,
    required this.onLocked,
    super.key,
  });

  final CoreGateway gateway;
  final Strings strings;
  final String language;
  final ValueChanged<String> onLanguageChanged;
  final VoidCallback onLocked;

  @override
  State<NotesShell> createState() => _NotesShellState();
}

class _NotesShellState extends State<NotesShell> {
  List<Map<String, dynamic>> notes = [];
  // The complete workspace collection, kept stable across an in-progress
  // client-side title search (see runSearch) even though notes itself is
  // temporarily replaced by that search's - or the backend search's -
  // narrowed results.
  List<Map<String, dynamic>> allNotes = [];
  List<Map<String, dynamic>> notebooks = [];
  List<Map<String, dynamic>> tags = [];
  bool loading = true;
  String? selectedNotebook;
  String? selectedTag;
  String? error;
  Timer? searchDebounce;
  String syncStatusValue = "disabled";
  Timer? syncStatusTimer;
  Timer? syncEventsTimer;
  int eventCursor = 0;
  bool pollingEvents = false;
  bool syncingWorkspace = false;

  @override
  void initState() {
    super.initState();
    refresh();
    requestCurrentWorkspaceSync(widget.gateway);
    refreshSyncStatus();
    syncStatusTimer = Timer.periodic(
      const Duration(seconds: 5),
      (_) => refreshSyncStatus(),
    );
    pollEvents();
    syncEventsTimer = Timer.periodic(
      const Duration(seconds: 1),
      (_) => pollEvents(),
    );
  }

  Future<void> refreshSyncStatus() async {
    try {
      final value = await widget.gateway.syncStatus();
      if (mounted) setState(() => syncStatusValue = value);
    } catch (_) {
      // Sync may be disabled or the account context not ready yet; the
      // indicator simply keeps its last known value.
    }
  }

  Future<void> refresh() async {
    try {
      final values = await Future.wait([
        widget.gateway.listNotes(),
        widget.gateway.listNotebooks(),
        widget.gateway.listTags(),
      ]);
      if (mounted) {
        setState(() {
          notes = values[0];
          allNotes = values[0];
          notebooks = values[1];
          tags = values[2];
          loading = false;
          error = null;
        });
      }
    } catch (_) {
      if (mounted) {
        setState(() {
          loading = false;
          error = widget.strings("offline");
        });
      }
    }
  }

  Future<void> syncCurrentWorkspace() async {
    if (syncingWorkspace) return;
    setState(() {
      syncingWorkspace = true;
      error = null;
    });
    try {
      await widget.gateway.syncNow();
      var synchronized = false;
      for (var attempt = 0; attempt < 30; attempt++) {
        final status = await widget.gateway.syncStatus();
        if (status == "current") {
          synchronized = true;
          break;
        }
        if (status == "offline" || status == "failed" || status == "disabled") {
          break;
        }
        await Future<void>.delayed(const Duration(seconds: 1));
      }
      if (!mounted) return;
      if (synchronized) {
        await refresh();
      } else {
        final detail = await widget.gateway.syncError();
        setState(
          () => error = detail.isEmpty
              ? widget.strings("workspace_sync_pending")
              : "${widget.strings("sync_error_details")}: $detail",
        );
      }
      await refreshSyncStatus();
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    } finally {
      if (mounted) setState(() => syncingWorkspace = false);
    }
  }

  Future<void> pollEvents() async {
    if (pollingEvents) return;
    pollingEvents = true;
    try {
      final events = await widget.gateway.pollEvents(eventCursor);
      var refreshCollection = false;
      for (final event in events) {
        final sequence = event["sequence"];
        if (sequence is num && sequence.toInt() > eventCursor) {
          eventCursor = sequence.toInt();
        }
        switch (event["type"]) {
          case "workspace_changed":
          case "workspace_synced":
            refreshCollection = true;
        }
      }
      if (refreshCollection && mounted) await refresh();
    } catch (_) {
      // The next poll retries after transient method-channel or lock errors.
    } finally {
      pollingEvents = false;
    }
  }

  void runSearch(String value) {
    searchDebounce?.cancel();
    final trimmed = value.trim();
    if (trimmed.isNotEmpty && !containsQueryToken(trimmed)) {
      // Partial title match, entirely client-side: the full collection is
      // already loaded in allNotes, so there is no need to debounce or hit
      // the backend for the common case of typing a few letters of a
      // title. The backend's FTS index only matches whole-word prefixes
      // (see core/store/search.go's ftsMatchQuery), so this also covers
      // mid-word substrings FTS alone would miss.
      final needle = trimmed.toLowerCase();
      setState(() {
        notes =
            allNotes
                .where(
                  (note) =>
                      note["deleted"] != true &&
                      (note["title"] as String).toLowerCase().contains(
                        needle,
                      ),
                )
                .toList();
      });
      return;
    }
    searchDebounce = Timer(const Duration(milliseconds: 250), () async {
      if (trimmed.isEmpty) {
        await refresh();
        return;
      }
      try {
        final result = await widget.gateway.search(value);
        if (mounted) setState(() => notes = result);
      } catch (failure) {
        if (mounted) {
          setState(() => error = describeFailure(widget.strings, failure));
        }
      }
    });
  }

  @override
  void dispose() {
    searchDebounce?.cancel();
    syncStatusTimer?.cancel();
    syncEventsTimer?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final visible =
        notes
            .where(
              (note) =>
                  note["deleted"] != true &&
                  (selectedNotebook == null ||
                      note["notebook_id"] == selectedNotebook) &&
                  (selectedTag == null ||
                      (note["tag_ids"] as List<dynamic>? ?? const []).contains(
                        selectedTag,
                      )),
            )
            .toList();
    return Scaffold(
      appBar: AppBar(
        title: Text(widget.strings("notes")),
        actions: [
          IconButton(
            tooltip: widget.strings("sync"),
            onPressed: syncingWorkspace ? null : syncCurrentWorkspace,
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
                "${widget.strings("server")}: "
                "${widget.strings("sync_status_$syncStatusValue")}",
            onPressed: showServer,
            icon: Icon(
              syncStatusIcon(syncStatusValue),
              color: syncStatusColor(context, syncStatusValue),
            ),
          ),
          IconButton(
            tooltip: widget.strings("lock"),
            onPressed: () async {
              await widget.gateway.lock();
              widget.onLocked();
            },
            icon: const Icon(Icons.lock_outline),
          ),
          IconButton(
            tooltip: widget.strings("backup"),
            onPressed: showBackups,
            icon: const Icon(Icons.backup_outlined),
          ),
          IconButton(
            tooltip: widget.strings("settings"),
            onPressed: showSettings,
            icon: const Icon(Icons.settings_outlined),
          ),
        ],
      ),
      drawer: NavigationDrawer(
        onDestinationSelected: (index) {
          // The only NavigationDrawerDestination below is "Notes" (index 0);
          // every other drawer row is a plain ListTile and does not count
          // toward this index.
          if (index == 0) {
            setState(() {
              selectedNotebook = null;
              selectedTag = null;
            });
            Navigator.pop(context);
          }
        },
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 16, 8, 16),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  widget.strings("notebooks"),
                  style: Theme.of(context).textTheme.titleMedium,
                ),
                PopupMenuButton<String>(
                  icon: const Icon(Icons.more_vert),
                  tooltip: widget.strings("more_actions"),
                  onSelected: (action) {
                    if (action == "new_note") {
                      createNote(notebookId: "", closeDrawer: true);
                    } else {
                      createNotebook();
                    }
                  },
                  itemBuilder: (context) => [
                    PopupMenuItem(
                      value: "new_note",
                      child: Text(widget.strings("new_note")),
                    ),
                    PopupMenuItem(
                      value: "new_notebook",
                      child: Text(widget.strings("new_notebook")),
                    ),
                  ],
                ),
              ],
            ),
          ),
          NavigationDrawerDestination(
            icon: const Icon(Icons.notes),
            label: Text(widget.strings("notes")),
          ),
          ...notebookTree(),
          const Divider(),
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 8, 0),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(widget.strings("tags")),
                IconButton(
                  tooltip: widget.strings("new_tag"),
                  onPressed: createTag,
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
                    setState(() {
                      selectedTag =
                          selectedTag == item["id"]
                              ? null
                              : item["id"] as String;
                    });
                    Navigator.pop(context);
                  },
                  trailing: IconButton(
                    tooltip: widget.strings("delete"),
                    icon: const Icon(Icons.close, size: 18),
                    onPressed: () => deleteTag(item["id"] as String),
                  ),
                ),
              ),
          ListTile(
            title: _LanguageControl(
              language: widget.language,
              onChanged: widget.onLanguageChanged,
            ),
          ),
        ],
      ),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(12),
            child: SearchBar(
              hintText: widget.strings("search"),
              leading: const Icon(Icons.search),
              onChanged: runSearch,
            ),
          ),
          if (error != null)
            MaterialBanner(
              content: Text(error!),
              actions: [
                TextButton(onPressed: refresh, child: const Text("OK")),
              ],
            ),
          Expanded(
            child:
                loading
                    ? const Center(child: CircularProgressIndicator())
                    : visible.isEmpty
                    ? Center(child: Text(widget.strings("empty")))
                    : ListView.builder(
                      itemCount: visible.length,
                      itemBuilder: (context, index) {
                        final note = visible[index];
                        final title = note["title"] as String;
                        return ListTile(
                          leading: Icon(
                            note["pinned"] == true
                                ? Icons.push_pin
                                : Icons.description_outlined,
                          ),
                          title: Text(
                            title.isEmpty ? widget.strings("untitled") : title,
                          ),
                          subtitle: Text(
                            DateTime.fromMillisecondsSinceEpoch(
                              (note["updated_unix_ms"] as int?) ??
                                  (note["created_unix_ms"] as int),
                            ).toLocal().toString(),
                          ),
                          trailing: IconButton(
                            tooltip: widget.strings("move_to_notebook"),
                            icon: const Icon(Icons.drive_file_move_outline),
                            onPressed: () => moveNote(note["id"] as String),
                          ),
                          onTap: () => openEditor(note["id"] as String),
                        );
                      },
                    ),
          ),
        ],
      ),
      floatingActionButton: FloatingActionButton.extended(
        tooltip: widget.strings("new_note"),
        onPressed: () => createNote(notebookId: selectedNotebook ?? ""),
        icon: const Icon(Icons.add),
        label: Text(widget.strings("new_note")),
      ),
    );
  }

  Future<void> createNote({
    required String notebookId,
    bool closeDrawer = false,
  }) async {
    try {
      final created = await widget.gateway.createNote(
        widget.strings("untitled"),
        notebookId: notebookId,
      );
      requestCurrentWorkspaceSync(widget.gateway);
      if (!mounted) return;
      if (closeDrawer) Navigator.pop(context);
      await openEditor(created["id"] as String, clearDefaultTitleOnFocus: true);
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    }
  }

  Future<void> openEditor(
    String noteId, {
    bool clearDefaultTitleOnFocus = false,
  }) async {
    await Navigator.push(
      context,
      MaterialPageRoute(
        builder:
            (_) => EditorScreen(
              gateway: widget.gateway,
              strings: widget.strings,
              noteId: noteId,
              clearDefaultTitleOnFocus: clearDefaultTitleOnFocus,
            ),
      ),
    );
    await refresh();
  }

  Future<void> showBackups() async {
    await showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder:
          (_) => BackupSheet(gateway: widget.gateway, strings: widget.strings),
    );
    await refresh();
  }

  Future<void> showSettings() async {
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder:
          (_) => SettingsSheet(
            gateway: widget.gateway,
            strings: widget.strings,
            notebooks: notebooks,
          ),
    );
  }

  Future<void> showServer() async {
    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder:
          (_) => ServerSheet(gateway: widget.gateway, strings: widget.strings),
    );
    // Joining or switching a shared workspace (below, inside ServerSheet)
    // changes which notes/notebooks are visible, so refresh unconditionally
    // rather than trying to track whether that actually happened.
    await refresh();
    await refreshSyncStatus();
  }

  List<Widget> notebookTree() {
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
              setState(() {
                selectedNotebook = item["id"] as String;
                selectedTag = null;
              });
              Navigator.pop(context);
            },
            trailing: PopupMenuButton<String>(
              icon: const Icon(Icons.more_vert),
              onSelected: (action) {
                switch (action) {
                  case "add":
                    createNotebook(parentId: item["id"] as String);
                  case "rename":
                    renameNotebook(
                      item["id"] as String,
                      item["name"] as String,
                    );
                  default:
                    deleteNotebook(item["id"] as String);
                }
              },
              itemBuilder:
                  (context) => [
                    PopupMenuItem(
                      value: "add",
                      child: Text(widget.strings("new_notebook")),
                    ),
                    PopupMenuItem(
                      value: "rename",
                      child: Text(widget.strings("rename")),
                    ),
                    PopupMenuItem(
                      value: "delete",
                      child: Text(widget.strings("delete")),
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

  Future<void> createNotebook({String parentId = ""}) async {
    final controller = TextEditingController();
    final name = await showDialog<String>(
      context: context,
      builder:
          (dialogContext) => AlertDialog(
            title: Text(widget.strings("new_notebook")),
            content: TextField(
              controller: controller,
              autofocus: true,
              decoration: InputDecoration(
                labelText: widget.strings("notebook_name"),
              ),
              onSubmitted:
                  (value) => Navigator.pop(dialogContext, value.trim()),
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(dialogContext),
                child: Text(widget.strings("cancel")),
              ),
              FilledButton(
                onPressed:
                    () => Navigator.pop(dialogContext, controller.text.trim()),
                child: Text(widget.strings("create_action")),
              ),
            ],
          ),
    );
    if (name == null || name.isEmpty) return;
    try {
      await widget.gateway.createNotebook(name, parentId: parentId);
      requestCurrentWorkspaceSync(widget.gateway);
      await refresh();
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    }
  }

  Future<void> renameNotebook(String id, String currentName) async {
    final controller = TextEditingController(text: currentName);
    final name = await showDialog<String>(
      context: context,
      builder:
          (dialogContext) => AlertDialog(
            title: Text(widget.strings("rename_notebook")),
            content: TextField(
              controller: controller,
              autofocus: true,
              decoration: InputDecoration(
                labelText: widget.strings("notebook_name"),
              ),
              onSubmitted:
                  (value) => Navigator.pop(dialogContext, value.trim()),
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(dialogContext),
                child: Text(widget.strings("cancel")),
              ),
              FilledButton(
                onPressed:
                    () => Navigator.pop(dialogContext, controller.text.trim()),
                child: Text(widget.strings("rename")),
              ),
            ],
          ),
    );
    if (name == null || name.isEmpty || name == currentName) return;
    try {
      await widget.gateway.renameNotebook(id, name);
      requestCurrentWorkspaceSync(widget.gateway);
      await refresh();
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    }
  }

  Future<void> deleteNotebook(String id) async {
    final hasChildren = notebooks.any(
      (item) => item["deleted"] != true && item["parent_id"] == id,
    );
    if (hasChildren) {
      if (mounted) {
        setState(() => error = widget.strings("notebook_has_children"));
      }
      return;
    }
    final confirmed = await showDialog<bool>(
      context: context,
      builder:
          (dialogContext) => AlertDialog(
            title: Text(widget.strings("delete_notebook")),
            content: Text(widget.strings("delete_notebook_warning")),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(dialogContext, false),
                child: Text(widget.strings("cancel")),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(dialogContext, true),
                child: Text(widget.strings("delete")),
              ),
            ],
          ),
    );
    if (confirmed != true) return;
    try {
      await widget.gateway.deleteNotebook(id, true);
      requestCurrentWorkspaceSync(widget.gateway);
      if (selectedNotebook == id) selectedNotebook = null;
      await refresh();
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    }
  }

  Future<void> createTag() async {
    final controller = TextEditingController();
    final name = await showDialog<String>(
      context: context,
      builder:
          (dialogContext) => AlertDialog(
            title: Text(widget.strings("new_tag")),
            content: TextField(
              controller: controller,
              autofocus: true,
              decoration: InputDecoration(
                labelText: widget.strings("tag_name"),
              ),
              onSubmitted:
                  (value) => Navigator.pop(dialogContext, value.trim()),
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(dialogContext),
                child: Text(widget.strings("cancel")),
              ),
              FilledButton(
                onPressed:
                    () => Navigator.pop(dialogContext, controller.text.trim()),
                child: Text(widget.strings("create_action")),
              ),
            ],
          ),
    );
    if (name == null || name.isEmpty) return;
    try {
      await widget.gateway.createTag(name);
      await refresh();
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    }
  }

  Future<void> deleteTag(String id) async {
    try {
      await widget.gateway.deleteTag(id, true);
      if (selectedTag == id) selectedTag = null;
      await refresh();
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    }
  }

  Future<void> moveNote(String noteId) async {
    final target = await showDialog<String>(
      context: context,
      builder:
          (dialogContext) => SimpleDialog(
            title: Text(widget.strings("move_to_notebook")),
            children: [
              SimpleDialogOption(
                onPressed: () => Navigator.pop(dialogContext, ""),
                child: Text(widget.strings("no_notebook")),
              ),
              for (final item in notebooks.where(
                (item) => item["deleted"] != true,
              ))
                SimpleDialogOption(
                  onPressed:
                      () => Navigator.pop(dialogContext, item["id"] as String),
                  child: Text(item["name"] as String),
                ),
            ],
          ),
    );
    if (target == null) return;
    try {
      await widget.gateway.moveNote(noteId, target);
      requestCurrentWorkspaceSync(widget.gateway);
      await refresh();
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    }
  }
}

class ServerSheet extends StatefulWidget {
  const ServerSheet({required this.gateway, required this.strings, super.key});

  final CoreGateway gateway;
  final Strings strings;

  @override
  State<ServerSheet> createState() => _ServerSheetState();
}

class _ServerSheetState extends State<ServerSheet> {
  final url = TextEditingController();
  final invite = TextEditingController();
  final fingerprint = TextEditingController();
  final peerIdentity = TextEditingController();
  final peerGrant = TextEditingController();
  String securityMode = "pinned";
  String? error;
  bool busy = false;
  String identityCode = "";
  String grantCode = "";
  String? copied;
  bool sharingBusy = false;
  String syncStatusValue = "disabled";
  String syncErrorDetail = "";
  List<Map<String, dynamic>> workspaces = const [];
  Timer? syncStatusTimer;

  @override
  void initState() {
    super.initState();
    widget.gateway
        .exportIdentity()
        .then((code) {
          if (mounted) setState(() => identityCode = code);
        })
        .catchError((_) {
          // No account/server context yet - the identity section simply
          // stays empty until this sheet is reopened after connecting.
        });
    widget.gateway
        .syncConnectionInfo()
        .then((info) {
          if (!mounted) return;
          setState(() {
            url.text = info["url"] as String? ?? "";
            fingerprint.text = info["fingerprint"] as String? ?? "";
            securityMode = info["security_mode"] as String? ?? "pinned";
          });
        })
        .catchError((_) {
          // No account context yet - the form simply stays empty.
        });
    refreshSyncStatus();
    loadWorkspaces();
    syncStatusTimer = Timer.periodic(
      const Duration(seconds: 3),
      (_) => refreshSyncStatus(),
    );
  }

  Future<void> refreshSyncStatus() async {
    try {
      final values = await Future.wait([
        widget.gateway.syncStatus(),
        widget.gateway.syncError(),
      ]);
      if (mounted) {
        setState(() {
          syncStatusValue = values[0];
          syncErrorDetail = values[1];
        });
      }
    } catch (_) {
      // Keep showing the last known status; the periodic timer retries.
    }
  }

  Future<void> loadWorkspaces() async {
    try {
      final values = await widget.gateway.listWorkspaces();
      if (mounted) setState(() => workspaces = values);
    } catch (_) {
      // The account may not be unlocked yet. Keep any already loaded list
      // visible and retry the next time this sheet is opened.
    }
  }

  Future<bool> waitForInitialWorkspaceSync() async {
    for (var attempt = 0; attempt < 30; attempt++) {
      final status = await widget.gateway.syncStatus();
      if (status == "current") return true;
      if (status == "offline" || status == "failed" || status == "disabled") {
        return false;
      }
      await Future<void>.delayed(const Duration(seconds: 1));
    }
    return false;
  }

  @override
  void dispose() {
    syncStatusTimer?.cancel();
    url.dispose();
    invite.dispose();
    fingerprint.dispose();
    peerIdentity.dispose();
    peerGrant.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: ListView(
        padding: EdgeInsets.only(
          left: 20,
          right: 20,
          bottom: MediaQuery.viewInsetsOf(context).bottom + 20,
        ),
        shrinkWrap: true,
        children: [
          Text(
            widget.strings("server"),
            style: Theme.of(context).textTheme.titleLarge,
          ),
          Text(widget.strings("server_optional")),
          Row(
            children: [
              Icon(
                syncStatusIcon(syncStatusValue),
                size: 18,
                color: syncStatusColor(context, syncStatusValue),
              ),
              const SizedBox(width: 8),
              Text(
                "${widget.strings("sync_status_label")}: "
                "${widget.strings("sync_status_$syncStatusValue")}",
                style: TextStyle(
                  color: syncStatusColor(context, syncStatusValue),
                ),
              ),
            ],
          ),
          if (syncErrorDetail.isNotEmpty) ...[
            const SizedBox(height: 8),
            SelectableText(
              "${widget.strings("sync_error_details")}: $syncErrorDetail",
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ],
          const SizedBox(height: 12),
          TextField(
            controller: url,
            keyboardType: TextInputType.url,
            decoration: InputDecoration(
              labelText: widget.strings("server_url"),
            ),
          ),
          TextField(
            controller: invite,
            decoration: InputDecoration(
              labelText: widget.strings("invite_code"),
            ),
          ),
          SegmentedButton<String>(
            segments: [
              ButtonSegment(
                value: "pinned",
                label: Text(widget.strings("pinned")),
              ),
              ButtonSegment(
                value: "trusted",
                label: Text(widget.strings("trusted")),
              ),
            ],
            selected: {securityMode},
            onSelectionChanged:
                (value) => setState(() => securityMode = value.first),
          ),
          if (securityMode == "pinned")
            TextField(
              controller: fingerprint,
              decoration: InputDecoration(
                labelText: widget.strings("fingerprint"),
              ),
            ),
          if (error != null)
            Text(
              error!,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          const SizedBox(height: 12),
          FilledButton(
            onPressed: busy ? null : connect,
            child: Text(widget.strings("connect")),
          ),
          TextButton(
            onPressed:
                busy
                    ? null
                    : () async {
                      await widget.gateway.disconnectServer();
                      if (context.mounted) Navigator.pop(context);
                    },
            child: Text(widget.strings("disconnect")),
          ),
          if (workspaces.isNotEmpty) ...[
            const Divider(height: 32),
            Text(
              widget.strings("workspaces"),
              style: Theme.of(context).textTheme.titleMedium,
            ),
            for (final workspace in workspaces)
              ListTile(
                contentPadding: EdgeInsets.zero,
                leading: Icon(
                  workspace["active"] == true
                      ? Icons.folder_shared
                      : Icons.folder_outlined,
                ),
                title: Text(workspace["workspace_id"] as String),
                subtitle: Text(
                  "${workspace["role"] == "owner" ? widget.strings("workspace_owner") : widget.strings("workspace_member")}"
                  " · ${workspace["member_count"] ?? 0} ${widget.strings("workspace_members")}",
                ),
                trailing:
                    workspace["active"] == true
                        ? Text(widget.strings("workspace_active"))
                        : TextButton(
                          onPressed:
                              sharingBusy
                                  ? null
                                  : () => setActiveWorkspace(
                                    workspace["workspace_id"] as String,
                                  ),
                          child: Text(widget.strings("workspace_switch")),
                        ),
              ),
          ],
          const Divider(height: 32),
          Text(
            widget.strings("your_identity"),
            style: Theme.of(context).textTheme.titleMedium,
          ),
          Text(widget.strings("your_identity_hint")),
          SelectableText(identityCode),
          TextButton(
            onPressed:
                identityCode.isEmpty
                    ? null
                    : () => copyToClipboard(identityCode),
            child: Text(
              widget.strings(copied == identityCode ? "copied" : "copy"),
            ),
          ),
          const Divider(height: 32),
          Text(
            widget.strings("share_workspace"),
            style: Theme.of(context).textTheme.titleMedium,
          ),
          TextField(
            controller: peerIdentity,
            decoration: InputDecoration(
              labelText: widget.strings("paste_identity"),
            ),
          ),
          FilledButton(
            onPressed: sharingBusy ? null : shareWorkspace,
            child: Text(widget.strings("generate_share_code")),
          ),
          if (grantCode.isNotEmpty) ...[
            Text(widget.strings("grant_code")),
            SelectableText(grantCode),
            Text(widget.strings("grant_code_hint")),
            TextButton(
              onPressed: () => copyToClipboard(grantCode),
              child: Text(
                widget.strings(copied == grantCode ? "copied" : "copy"),
              ),
            ),
          ],
          const Divider(height: 32),
          Text(
            widget.strings("join_workspace"),
            style: Theme.of(context).textTheme.titleMedium,
          ),
          TextField(
            controller: peerGrant,
            decoration: InputDecoration(
              labelText: widget.strings("paste_grant"),
            ),
          ),
          FilledButton(
            onPressed: sharingBusy ? null : acceptWorkspaceGrant,
            child: Text(widget.strings("join")),
          ),
        ],
      ),
    );
  }

  Future<void> copyToClipboard(String text) async {
    await Clipboard.setData(ClipboardData(text: text));
    if (mounted) setState(() => copied = text);
  }

  Future<void> shareWorkspace() async {
    setState(() {
      sharingBusy = true;
      error = null;
    });
    try {
      final code = await widget.gateway.shareWorkspace(
        peerIdentity.text.trim(),
      );
      if (mounted) {
        setState(() {
          grantCode = code;
          peerIdentity.clear();
        });
      }
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    } finally {
      if (mounted) setState(() => sharingBusy = false);
    }
  }

  Future<void> acceptWorkspaceGrant() async {
    setState(() {
      sharingBusy = true;
      error = null;
    });
    try {
      await widget.gateway.acceptWorkspaceGrant(peerGrant.text.trim());
      final synchronized = await waitForInitialWorkspaceSync();
      if (!mounted) return;
      setState(() => peerGrant.clear());
      await loadWorkspaces();
      if (!mounted) return;
      if (synchronized && context.mounted) Navigator.pop(context);
      if (!synchronized) {
        setState(() => error = widget.strings("workspace_sync_pending"));
      }
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    } finally {
      if (mounted) setState(() => sharingBusy = false);
    }
  }

  Future<void> setActiveWorkspace(String workspaceID) async {
    setState(() {
      sharingBusy = true;
      error = null;
    });
    try {
      await widget.gateway.setActiveWorkspace(workspaceID);
      final synchronized = await waitForInitialWorkspaceSync();
      await loadWorkspaces();
      if (synchronized && mounted && context.mounted) {
        Navigator.pop(context);
      }
      if (!synchronized && mounted) {
        setState(() => error = widget.strings("workspace_sync_pending"));
      }
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    } finally {
      if (mounted) setState(() => sharingBusy = false);
    }
  }

  Future<void> connect() async {
    setState(() {
      busy = true;
      error = null;
    });
    try {
      await widget.gateway.connectServer({
        "url": url.text.trim(),
        "invite_code": invite.text.trim(),
        "fingerprint": fingerprint.text.trim(),
        "security_mode": securityMode,
        "device_name": "Android",
      });
      await loadWorkspaces();
      if (mounted) Navigator.pop(context);
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }
}

class SettingsSheet extends StatefulWidget {
  const SettingsSheet({
    required this.gateway,
    required this.strings,
    required this.notebooks,
    super.key,
  });

  final CoreGateway gateway;
  final Strings strings;
  final List<Map<String, dynamic>> notebooks;

  @override
  State<SettingsSheet> createState() => _SettingsSheetState();
}

class _SettingsSheetState extends State<SettingsSheet> {
  Map<String, dynamic>? settings;
  String? error;

  @override
  void initState() {
    super.initState();
    widget.gateway
        .getSettings()
        .then((value) {
          if (mounted) setState(() => settings = value);
        })
        .catchError((Object failure) {
          if (mounted) {
            setState(() => error = describeFailure(widget.strings, failure));
          }
        });
  }

  @override
  Widget build(BuildContext context) {
    final current = settings;
    if (current == null) {
      return SizedBox(
        height: 240,
        child: Center(
          child:
              error == null ? const CircularProgressIndicator() : Text(error!),
        ),
      );
    }
    final selected = (current["selected_notebooks"] as List<dynamic>).toSet();
    final autoLockOptions =
        <int>{0, 1, 5, 15, 30, current["auto_lock_minutes"] as int}.toList()
          ..sort();
    return SafeArea(
      child: ListView(
        padding: EdgeInsets.only(
          left: 20,
          right: 20,
          bottom: MediaQuery.viewInsetsOf(context).bottom + 20,
        ),
        shrinkWrap: true,
        children: [
          Text(
            widget.strings("settings"),
            style: Theme.of(context).textTheme.titleLarge,
          ),
          DropdownButtonFormField<int>(
            initialValue: current["auto_lock_minutes"] as int,
            decoration: InputDecoration(labelText: widget.strings("auto_lock")),
            items:
                autoLockOptions
                    .map(
                      (value) => DropdownMenuItem(
                        value: value,
                        child: Text("$value min"),
                      ),
                    )
                    .toList(),
            onChanged:
                (value) =>
                    setState(() => current["auto_lock_minutes"] = value!),
          ),
          DropdownButtonFormField<String>(
            initialValue: current["attachment_retention"] as String,
            decoration: InputDecoration(
              labelText: widget.strings("attachment_retention"),
            ),
            items: [
              DropdownMenuItem(
                value: "all",
                child: Text(widget.strings("retention_all")),
              ),
              DropdownMenuItem(
                value: "selected_notebooks",
                child: Text(widget.strings("retention_selected")),
              ),
              DropdownMenuItem(
                value: "metadata_only",
                child: Text(widget.strings("retention_metadata")),
              ),
            ],
            onChanged: (value) {
              setState(() {
                current["attachment_retention"] = value!;
                if (value != "selected_notebooks") {
                  current["selected_notebooks"] = <String>[];
                }
              });
            },
          ),
          if (current["attachment_retention"] == "selected_notebooks")
            ...widget.notebooks
                .where((row) => row["deleted"] != true)
                .map(
                  (row) => CheckboxListTile(
                    value: selected.contains(row["id"]),
                    title: Text(row["name"] as String),
                    onChanged: (checked) {
                      final values = List<String>.from(
                        current["selected_notebooks"] as List<dynamic>,
                      );
                      if (checked == true) {
                        values.add(row["id"] as String);
                      } else {
                        values.remove(row["id"]);
                      }
                      setState(() {
                        current["selected_notebooks"] =
                            values.toSet().toList()..sort();
                      });
                    },
                  ),
                ),
          TextFormField(
            initialValue:
                ((current["cache_limit_bytes"] as int) ~/ (1024 * 1024))
                    .toString(),
            keyboardType: TextInputType.number,
            decoration: InputDecoration(
              labelText: widget.strings("cache_limit"),
            ),
            onChanged: (value) {
              final mebibytes = int.tryParse(value);
              if (mebibytes != null) {
                current["cache_limit_bytes"] = mebibytes * 1024 * 1024;
              }
            },
          ),
          if (error != null)
            Text(
              error!,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          const SizedBox(height: 12),
          FilledButton(
            onPressed: () async {
              try {
                await widget.gateway.updateSettings(current);
                if (context.mounted) Navigator.pop(context);
              } catch (failure) {
                if (mounted) {
                  setState(
                    () => error = describeFailure(widget.strings, failure),
                  );
                }
              }
            },
            child: Text(widget.strings("save")),
          ),
        ],
      ),
    );
  }
}

class BackupSheet extends StatefulWidget {
  const BackupSheet({required this.gateway, required this.strings, super.key});

  final CoreGateway gateway;
  final Strings strings;

  @override
  State<BackupSheet> createState() => _BackupSheetState();
}

class _BackupSheetState extends State<BackupSheet> {
  late Future<List<Map<String, dynamic>>> backups =
      widget.gateway.listBackups();
  String? error;

  void reload() =>
      setState(() {
        backups = widget.gateway.listBackups();
      });

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Wrap(
          spacing: 8,
          children: [
            OutlinedButton.icon(
              onPressed: widget.gateway.selectBackupDestination,
              icon: const Icon(Icons.folder_open),
              label: Text(widget.strings("backup_destination")),
            ),
            OutlinedButton.icon(
              onPressed: () async {
                try {
                  await widget.gateway.importBackups();
                  reload();
                } catch (failure) {
                  if (mounted) {
                    setState(() {
                      error =
                          failure.toString().toLowerCase().contains("space")
                              ? widget.strings("backup_capacity")
                              : describeFailure(widget.strings, failure);
                    });
                  }
                }
              },
              icon: const Icon(Icons.cloud_download_outlined),
              label: Text(widget.strings("backup_import")),
            ),
            FilledButton.icon(
              onPressed: () async {
                try {
                  await widget.gateway.createBackup();
                  reload();
                } catch (failure) {
                  if (mounted) {
                    setState(() {
                      error =
                          failure.toString().toLowerCase().contains("space")
                              ? widget.strings("backup_capacity")
                              : describeFailure(widget.strings, failure);
                    });
                  }
                }
              },
              icon: const Icon(Icons.backup),
              label: Text(widget.strings("backup_now")),
            ),
          ],
        ),
        if (error != null)
          Text(
            error!,
            style: TextStyle(color: Theme.of(context).colorScheme.error),
          ),
        Expanded(
          child: FutureBuilder<List<Map<String, dynamic>>>(
            future: backups,
            builder: (context, snapshot) {
              if (!snapshot.hasData) {
                return const Center(child: CircularProgressIndicator());
              }
              if (snapshot.data!.isEmpty) {
                return Center(child: Text(widget.strings("backup_empty")));
              }
              return ListView.builder(
                itemCount: snapshot.data!.length,
                itemBuilder: (context, index) {
                  final backup = snapshot.data![index];
                  return ListTile(
                    enabled: backup["corrupt"] != true,
                    leading: const Icon(Icons.shield_outlined),
                    title: Text(
                      DateTime.fromMillisecondsSinceEpoch(
                        backup["created_unix_ms"] as int,
                      ).toLocal().toString(),
                    ),
                    trailing: TextButton(
                      onPressed: () => confirmRestore(backup["id"] as String),
                      child: Text(widget.strings("restore")),
                    ),
                  );
                },
              );
            },
          ),
        ),
      ],
    );
  }

  Future<void> confirmRestore(String backupId) async {
    await widget.gateway.previewBackup(backupId);
    if (!mounted) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder:
          (context) => AlertDialog(
            title: Text(widget.strings("replace_restore")),
            content: Text(widget.strings("restore_warning")),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(context, false),
                child: Text(widget.strings("cancel")),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(context, true),
                child: Text(widget.strings("restore")),
              ),
            ],
          ),
    );
    if (confirmed == true) {
      await widget.gateway.restoreBackup(backupId);
      reload();
    }
  }
}

// Restricted to exactly the formatting keys core/sync/yjsadapter's
// canonical Markdown projection understands (see Attr* in
// core/sync/yjsadapter/document.go and markdown_delta.dart's port of it) -
// matching desktop's own TOOLBAR_FORMATS restriction in
// desktop/frontend/src/editor/NoteEditor.tsx. Any other flutter_quill
// format (underline, color, font, alignment, checklists, ...) would
// round-trip through the editor's own Document harmlessly but silently
// vanish the next time this note's Markdown is saved and re-parsed, so it
// is deliberately not offered here either.
final _editorToolbarConfig = QuillSimpleToolbarConfig(
  buttonOptions: QuillSimpleToolbarButtonOptions(
    selectHeaderStyleDropdownButton:
        QuillToolbarSelectHeaderStyleDropdownButtonOptions(
          attributes: [
            Attribute.h1,
            Attribute.h2,
            Attribute.h3,
            Attribute.header,
          ],
        ),
  ),
  showFontFamily: false,
  showFontSize: false,
  showUnderLineButton: false,
  showColorButton: false,
  showBackgroundColorButton: false,
  showListCheck: false,
  showIndent: false,
  showUndo: false,
  showRedo: false,
  showSearchButton: false,
  showSubscript: false,
  showSuperscript: false,
);

class EditorScreen extends StatefulWidget {
  const EditorScreen({
    required this.gateway,
    required this.strings,
    required this.noteId,
    this.clearDefaultTitleOnFocus = false,
    super.key,
  });

  final CoreGateway gateway;
  final Strings strings;
  final String noteId;
  // True only for a note created from this mobile session. Existing notes
  // titled "Untitled" deliberately remain unchanged: that can be a title
  // the user chose themselves.
  final bool clearDefaultTitleOnFocus;

  @override
  State<EditorScreen> createState() => _EditorScreenState();
}

class _EditorScreenState extends State<EditorScreen> {
  final title = TextEditingController();
  final titleFocusNode = FocusNode();
  // Built once GetNote resolves (see initState): flutter_quill's Document
  // takes over its content immediately on construction, so there is no
  // useful "empty" QuillController to show before the note's actual body
  // is known, unlike title's plain TextEditingController.
  QuillController? body;
  final bodyFocusNode = FocusNode();
  final bodyScrollController = ScrollController();
  bool loading = true;
  bool dirty = false;
  List<Map<String, dynamic>> attachments = [];
  bool capturingPhoto = false;
  List<Map<String, dynamic>> allTags = [];
  List<String> noteTagIds = [];
  bool titleLoaded = false;
  bool clearDefaultTitleOnFocus = false;

  @override
  void initState() {
    super.initState();
    clearDefaultTitleOnFocus = widget.clearDefaultTitleOnFocus;
    titleFocusNode.addListener(clearDefaultTitle);
    widget.gateway.getNote(widget.noteId).then((value) {
      if (!mounted) return;
      final note = value["note"] as Map<String, dynamic>;
      title.text = note["title"] as String;
      titleLoaded = true;
      final quillBody = QuillController(
        document: Document.fromDelta(markdownToDelta(value["body"] as String)),
        selection: const TextSelection.collapsed(offset: 0),
      );
      quillBody.addListener(markDirty);
      setState(() {
        body = quillBody;
        loading = false;
      });
      title.addListener(markDirty);
      clearDefaultTitle();
    });
    refreshAttachments();
    refreshTags();
  }

  void markDirty() {
    if (!dirty && mounted) setState(() => dirty = true);
  }

  void clearDefaultTitle() {
    if (!clearDefaultTitleOnFocus || !titleLoaded || !titleFocusNode.hasFocus) return;
    // A newly created mobile note is initialized with the localized default
    // title for compatibility with the existing core boundary. Treat that
    // value as a placeholder in the title editor, without erasing a custom
    // title if the backend returned one instead.
    clearDefaultTitleOnFocus = false;
    if (title.text == widget.strings("untitled")) title.clear();
  }

  Future<void> refreshAttachments() async {
    try {
      final result = await widget.gateway.listNoteAttachments(widget.noteId);
      if (mounted) setState(() => attachments = result);
    } catch (_) {
      // Best-effort: the editor still works without the attachment strip.
    }
  }

  Future<void> refreshTags() async {
    try {
      final results = await Future.wait([
        widget.gateway.listTags(),
        widget.gateway.listNoteTags(widget.noteId),
      ]);
      if (mounted) {
        setState(() {
          allTags = results[0] as List<Map<String, dynamic>>;
          noteTagIds = results[1] as List<String>;
        });
      }
    } catch (_) {
      // Best-effort: the editor still works without the tags row.
    }
  }

  Future<void> toggleTag(String tagId, bool present) async {
    try {
      await widget.gateway.setNoteTag(widget.noteId, tagId, present);
      requestCurrentWorkspaceSync(widget.gateway);
      await refreshTags();
    } catch (failure) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(describeFailure(widget.strings, failure))),
        );
      }
    }
  }

  Future<void> pickTag() async {
    final unassigned =
        allTags
            .where(
              (tag) =>
                  tag["deleted"] != true && !noteTagIds.contains(tag["id"]),
            )
            .toList();
    final selection = await showDialog<String>(
      context: context,
      builder:
          (dialogContext) => SimpleDialog(
            title: Text(widget.strings("add_tag")),
            children: [
              for (final tag in unassigned)
                SimpleDialogOption(
                  onPressed:
                      () => Navigator.pop(dialogContext, tag["id"] as String),
                  child: Text(tag["name"] as String),
                ),
              SimpleDialogOption(
                onPressed: () => Navigator.pop(dialogContext, "\x00new"),
                child: Text(widget.strings("new_tag")),
              ),
            ],
          ),
    );
    if (selection == null) return;
    if (selection != "\x00new") {
      await toggleTag(selection, true);
      return;
    }
    if (!mounted) return;
    final controller = TextEditingController();
    final name = await showDialog<String>(
      context: context,
      builder:
          (dialogContext) => AlertDialog(
            title: Text(widget.strings("new_tag")),
            content: TextField(
              controller: controller,
              autofocus: true,
              decoration: InputDecoration(
                labelText: widget.strings("tag_name"),
              ),
              onSubmitted:
                  (value) => Navigator.pop(dialogContext, value.trim()),
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(dialogContext),
                child: Text(widget.strings("cancel")),
              ),
              FilledButton(
                onPressed:
                    () => Navigator.pop(dialogContext, controller.text.trim()),
                child: Text(widget.strings("create_action")),
              ),
            ],
          ),
    );
    if (name == null || name.isEmpty) return;
    try {
      final created = await widget.gateway.createTag(name);
      await toggleTag(created["id"] as String, true);
    } catch (failure) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(describeFailure(widget.strings, failure))),
        );
      }
    }
  }

  Widget tagsRow() {
    final assigned =
        allTags
            .where(
              (tag) => tag["deleted"] != true && noteTagIds.contains(tag["id"]),
            )
            .toList();
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      child: Wrap(
        spacing: 6,
        children: [
          for (final tag in assigned)
            InputChip(
              label: Text(tag["name"] as String),
              onDeleted: () => toggleTag(tag["id"] as String, false),
            ),
          ActionChip(
            avatar: const Icon(Icons.add, size: 16),
            label: Text(widget.strings("add_tag")),
            onPressed: pickTag,
          ),
        ],
      ),
    );
  }

  Future<void> capturePhoto() async {
    setState(() => capturingPhoto = true);
    try {
      await widget.gateway.capturePhoto(widget.noteId);
      requestCurrentWorkspaceSync(widget.gateway);
      await refreshAttachments();
    } catch (failure) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(describeFailure(widget.strings, failure))),
        );
      }
    } finally {
      if (mounted) setState(() => capturingPhoto = false);
    }
  }

  Future<void> save() async {
    // dirty only ever becomes true via body's own listener (see initState),
    // so by the time save can be invoked (the save button, or a dirty pop)
    // body is guaranteed to be loaded.
    final markdown = deltaToMarkdown(body!.document.toDelta());
    await widget.gateway.saveNote(widget.noteId, title.text, markdown);
    requestCurrentWorkspaceSync(widget.gateway);
    if (mounted) setState(() => dirty = false);
  }

  Future<void> deleteNote() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder:
          (dialogContext) => AlertDialog(
            title: Text(widget.strings("delete_note")),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(dialogContext, false),
                child: Text(widget.strings("cancel")),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(dialogContext, true),
                child: Text(widget.strings("delete")),
              ),
            ],
          ),
    );
    if (confirmed != true) return;
    try {
      await widget.gateway.deleteNote(widget.noteId, true);
      requestCurrentWorkspaceSync(widget.gateway);
      if (mounted) {
        setState(() => dirty = false);
        Navigator.pop(context);
      }
    } catch (failure) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(describeFailure(widget.strings, failure))),
        );
      }
    }
  }

  @override
  void dispose() {
    title.dispose();
    titleFocusNode.dispose();
    body?.dispose();
    bodyFocusNode.dispose();
    bodyScrollController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final quillBody = body;
    return PopScope(
      canPop: !dirty,
      onPopInvokedWithResult: (didPop, _) async {
        if (!didPop && dirty) {
          await save();
          if (context.mounted) Navigator.pop(context);
        }
      },
      child: Scaffold(
        appBar: AppBar(
          title: TextField(
            controller: title,
            focusNode: titleFocusNode,
            decoration: InputDecoration(
              hintText: widget.strings("title"),
              border: InputBorder.none,
            ),
          ),
          actions: [
            IconButton(
              tooltip: widget.strings("save"),
              onPressed: dirty ? save : null,
              icon: const Icon(Icons.save_outlined),
            ),
            IconButton(
              tooltip: widget.strings("delete"),
              onPressed: deleteNote,
              icon: const Icon(Icons.delete_forever_outlined),
            ),
          ],
        ),
        body:
            loading || quillBody == null
                ? const Center(child: CircularProgressIndicator())
                : Column(
                  children: [
                    if (attachments.isNotEmpty) attachmentsStrip(),
                    tagsRow(),
                    QuillSimpleToolbar(
                      controller: quillBody,
                      config: _editorToolbarConfig,
                    ),
                    Expanded(
                      child: QuillEditor(
                        focusNode: bodyFocusNode,
                        scrollController: bodyScrollController,
                        controller: quillBody,
                        config: QuillEditorConfig(
                          placeholder: widget.strings("body"),
                          padding: const EdgeInsets.all(20),
                          expands: true,
                        ),
                      ),
                    ),
                  ],
                ),
        bottomNavigationBar: SafeArea(
          child: Row(
            children: [
              TextButton.icon(
                onPressed: capturingPhoto ? null : capturePhoto,
                icon:
                    capturingPhoto
                        ? const SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                        : const Icon(Icons.photo_camera),
                label: Text(widget.strings("photo")),
              ),
              TextButton.icon(
                onPressed: showRevisions,
                icon: const Icon(Icons.history),
                label: Text(widget.strings("revisions")),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> showRevisions() async {
    await showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder:
          (sheetContext) => RevisionSheet(
            gateway: widget.gateway,
            strings: widget.strings,
            noteId: widget.noteId,
            onRestored: () async {
              Navigator.pop(sheetContext);
              final value = await widget.gateway.getNote(widget.noteId);
              body?.document = Document.fromDelta(
                markdownToDelta(value["body"] as String),
              );
            },
          ),
    );
  }

  Widget attachmentsStrip() {
    return SizedBox(
      height: 96,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        itemCount: attachments.length,
        separatorBuilder: (_, _) => const SizedBox(width: 8),
        itemBuilder: (context, index) {
          final attachment = attachments[index];
          final blobId = attachment["blob_id"] as String;
          return _AttachmentThumbnail(
            key: ValueKey(blobId),
            gateway: widget.gateway,
            strings: widget.strings,
            noteId: widget.noteId,
            blobId: blobId,
            mediaType: attachment["media_type"] as String,
            onDeleted: refreshAttachments,
          );
        },
      ),
    );
  }
}

class _AttachmentThumbnail extends StatefulWidget {
  const _AttachmentThumbnail({
    super.key,
    required this.gateway,
    required this.strings,
    required this.noteId,
    required this.blobId,
    required this.mediaType,
    required this.onDeleted,
  });

  final CoreGateway gateway;
  final Strings strings;
  final String noteId;
  final String blobId;
  final String mediaType;
  final VoidCallback onDeleted;

  @override
  State<_AttachmentThumbnail> createState() => _AttachmentThumbnailState();
}

class _AttachmentThumbnailState extends State<_AttachmentThumbnail> {
  late final Future<Uint8List> bytes = widget.gateway.readAttachmentData(
    widget.blobId,
  );

  Future<void> confirmDelete() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder:
          (dialogContext) => AlertDialog(
            title: Text(widget.strings("delete_attachment")),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(dialogContext, false),
                child: Text(widget.strings("cancel")),
              ),
              FilledButton(
                onPressed: () => Navigator.pop(dialogContext, true),
                child: Text(widget.strings("delete")),
              ),
            ],
          ),
    );
    if (confirmed != true) return;
    try {
      await widget.gateway.removeAttachmentData(widget.noteId, widget.blobId);
      requestCurrentWorkspaceSync(widget.gateway);
      widget.onDeleted();
    } catch (failure) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(describeFailure(widget.strings, failure))),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final isImage = widget.mediaType.startsWith("image/");
    return Stack(
      children: [
        GestureDetector(
          onTap:
              isImage
                  ? () => showDialog<void>(
                    context: context,
                    builder:
                        (_) => Dialog(
                          child: FutureBuilder<Uint8List>(
                            future: bytes,
                            builder: (context, snapshot) {
                              if (snapshot.hasError) {
                                return SizedBox(
                                  height: 200,
                                  child: Center(
                                    child: Icon(
                                      Icons.broken_image_outlined,
                                      color:
                                          Theme.of(context).colorScheme.error,
                                    ),
                                  ),
                                );
                              }
                              if (!snapshot.hasData) {
                                return const SizedBox(
                                  height: 200,
                                  child: Center(
                                    child: CircularProgressIndicator(),
                                  ),
                                );
                              }
                              return InteractiveViewer(
                                child: Image.memory(snapshot.data!),
                              );
                            },
                          ),
                        ),
                  )
                  : null,
          onLongPress: confirmDelete,
          child: ClipRRect(
            borderRadius: BorderRadius.circular(8),
            child: SizedBox(
              width: 96,
              height: 96,
              child: FutureBuilder<Uint8List>(
                future: bytes,
                builder: (context, snapshot) {
                  if (snapshot.hasError) {
                    return const ColoredBox(
                      color: Color(0x11000000),
                      child: Center(child: Icon(Icons.broken_image_outlined)),
                    );
                  }
                  if (!snapshot.hasData) {
                    return const ColoredBox(
                      color: Color(0x11000000),
                      child: Center(
                        child: CircularProgressIndicator(strokeWidth: 2),
                      ),
                    );
                  }
                  if (!isImage) {
                    return const ColoredBox(
                      color: Color(0x11000000),
                      child: Center(
                        child: Icon(Icons.insert_drive_file_outlined),
                      ),
                    );
                  }
                  return Image.memory(snapshot.data!, fit: BoxFit.cover);
                },
              ),
            ),
          ),
        ),
        Positioned(
          top: 4,
          right: 4,
          child: Material(
            color: Theme.of(context).colorScheme.surfaceContainerHighest,
            elevation: 2,
            shape: const CircleBorder(),
            child: IconButton(
              tooltip: widget.strings("delete"),
              constraints: const BoxConstraints.tightFor(width: 40, height: 40),
              padding: EdgeInsets.zero,
              visualDensity: VisualDensity.compact,
              onPressed: confirmDelete,
              icon: const Icon(Icons.delete_outline, size: 20),
            ),
          ),
        ),
      ],
    );
  }
}

class RevisionSheet extends StatefulWidget {
  const RevisionSheet({
    required this.gateway,
    required this.strings,
    required this.noteId,
    required this.onRestored,
    super.key,
  });

  final CoreGateway gateway;
  final Strings strings;
  final String noteId;
  final Future<void> Function() onRestored;

  @override
  State<RevisionSheet> createState() => _RevisionSheetState();
}

class _RevisionSheetState extends State<RevisionSheet> {
  // Oldest first, exactly as the gateway returns it: RevisionDetailSheet's
  // diff-against-previous lookup relies on this order, matching desktop's
  // RevisionsPanel (see its "Newest first for display" comment).
  List<Map<String, dynamic>>? revisions;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final result = await widget.gateway.listRevisions(widget.noteId);
    if (mounted) setState(() => revisions = result);
  }

  Future<void> _openDetail(int oldestFirstIndex) async {
    final all = revisions!;
    final fromId = oldestFirstIndex > 0 ? all[oldestFirstIndex - 1]["id"] as String : "";
    final revision = all[oldestFirstIndex];
    await showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      isScrollControlled: true,
      builder:
          (sheetContext) => RevisionDetailSheet(
            gateway: widget.gateway,
            strings: widget.strings,
            noteId: widget.noteId,
            revisionId: revision["id"] as String,
            fromRevisionId: fromId,
            createdUnixMs: revision["created_unix_ms"] as int,
            checkpoint: revision["checkpoint"] == true,
            onRestored: () async {
              Navigator.pop(sheetContext);
              await widget.onRestored();
            },
          ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final loaded = revisions;
    if (loaded == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (loaded.isEmpty) {
      return Center(child: Text(widget.strings("revisions_empty")));
    }
    // Newest first for display; the gateway itself returns oldest first.
    return ListView.builder(
      itemCount: loaded.length,
      itemBuilder: (context, displayIndex) {
        final oldestFirstIndex = loaded.length - 1 - displayIndex;
        final revision = loaded[oldestFirstIndex];
        return ListTile(
          leading: const Icon(Icons.history),
          title: Text(
            DateTime.fromMillisecondsSinceEpoch(
              revision["created_unix_ms"] as int,
            ).toLocal().toString(),
          ),
          trailing:
              revision["checkpoint"] == true
                  ? Chip(label: Text(widget.strings("revisions_checkpoint")))
                  : null,
          onTap: () => _openDetail(oldestFirstIndex),
        );
      },
    );
  }
}

/// RevisionDetailSheet shows one revision's line-based diff against the
/// revision immediately before it (or against empty content, for the
/// oldest one) and offers restoring it, mirroring desktop's RevisionsPanel
/// selection/diff/restore behavior.
class RevisionDetailSheet extends StatefulWidget {
  const RevisionDetailSheet({
    required this.gateway,
    required this.strings,
    required this.noteId,
    required this.revisionId,
    required this.fromRevisionId,
    required this.createdUnixMs,
    required this.checkpoint,
    required this.onRestored,
    super.key,
  });

  final CoreGateway gateway;
  final Strings strings;
  final String noteId;
  final String revisionId;
  final String fromRevisionId;
  final int createdUnixMs;
  final bool checkpoint;
  final Future<void> Function() onRestored;

  @override
  State<RevisionDetailSheet> createState() => _RevisionDetailSheetState();
}

class _RevisionDetailSheetState extends State<RevisionDetailSheet> {
  List<Map<String, dynamic>>? diffLines;
  Object? diffError;
  Object? restoreError;
  bool restoring = false;

  @override
  void initState() {
    super.initState();
    _loadDiff();
  }

  Future<void> _loadDiff() async {
    try {
      final result = await widget.gateway.diffRevisions(
        widget.noteId,
        widget.fromRevisionId,
        widget.revisionId,
      );
      if (mounted) setState(() => diffLines = result);
    } catch (failure) {
      if (mounted) setState(() => diffError = failure);
    }
  }

  Future<void> _restore() async {
    setState(() {
      restoring = true;
      restoreError = null;
    });
    try {
      await widget.gateway.restoreRevision(widget.noteId, widget.revisionId);
      requestCurrentWorkspaceSync(widget.gateway);
      await widget.onRestored();
    } catch (failure) {
      if (mounted) {
        setState(() {
          restoring = false;
          restoreError = failure;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  DateTime.fromMillisecondsSinceEpoch(
                    widget.createdUnixMs,
                  ).toLocal().toString(),
                  style: Theme.of(context).textTheme.titleMedium,
                ),
              ),
              if (widget.checkpoint)
                Chip(label: Text(widget.strings("revisions_checkpoint"))),
            ],
          ),
          const SizedBox(height: 8),
          Flexible(
            child:
                diffError != null
                    ? Text(
                      describeFailure(widget.strings, diffError!),
                      style: TextStyle(color: scheme.error),
                    )
                    : diffLines == null
                    ? const Padding(
                      padding: EdgeInsets.symmetric(vertical: 24),
                      child: Center(child: CircularProgressIndicator()),
                    )
                    : SingleChildScrollView(
                      child: Text.rich(
                        TextSpan(
                          children: [
                            for (final line in diffLines!)
                              TextSpan(
                                text: "${_diffMarker(line["op"] as String)}${line["text"]}\n",
                                style: TextStyle(
                                  color: _diffColor(
                                    line["op"] as String,
                                    scheme,
                                  ),
                                  fontFamily: "monospace",
                                ),
                              ),
                          ],
                        ),
                      ),
                    ),
          ),
          const SizedBox(height: 8),
          if (restoreError != null)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Text(
                describeFailure(widget.strings, restoreError!),
                style: TextStyle(color: scheme.error),
              ),
            ),
          Align(
            alignment: Alignment.centerRight,
            child: FilledButton(
              onPressed: restoring ? null : _restore,
              child:
                  restoring
                      ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                      : Text(widget.strings("restore")),
            ),
          ),
        ],
      ),
    );
  }

  String _diffMarker(String op) => switch (op) {
    "insert" => "+ ",
    "delete" => "- ",
    _ => "  ",
  };

  Color? _diffColor(String op, ColorScheme scheme) => switch (op) {
    "insert" => Colors.green,
    "delete" => scheme.error,
    _ => null,
  };
}
