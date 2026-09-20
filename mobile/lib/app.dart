import "dart:async";
import "dart:typed_data";

import "package:flutter/foundation.dart" show kDebugMode;
import "package:flutter/material.dart";
import "package:flutter/services.dart"
    show AutofillHints, Clipboard, ClipboardData, PlatformException;
import "package:flutter_localizations/flutter_localizations.dart";
import "package:flutter_quill/flutter_quill.dart";

import "commit_tracker.dart";
import "core_gateway.dart";
import "markdown_delta.dart";
import "paste_format.dart";
import "strings.dart";

/// appVersion is this Flutter app's display version, matching
/// pubspec.yaml's version field (without its build-number suffix). The Go
/// core has no knowledge of the Android app package's own version, so
/// diagnostics calls that need it (see DiagnosticsSection below) pass this
/// constant explicitly rather than the core inventing one.
const String appVersion = "0.1.0";

/// knownQuarantineReasons is the complete, closed set of classes
/// core/sync.Reject can currently produce (see
/// core/store/sync_repository.go and core/sync/worker.go's
/// verificationClass) - a rejected operation never carries free-form
/// text, so this stays exhaustive rather than growing an ever-widening
/// switch.
const Set<String> knownQuarantineReasons = {
  "unsupported_version",
  "verification_failed",
  "empty_verified_operation",
  "op_id_reuse",
  "apply_failed",
};

/// quarantineReasonMessage localizes a quarantined operation's rejection
/// class (specs/product-experience's "Actionable and safe error
/// presentation" requirement: raw internal classification codes must not
/// appear as primary UI text) instead of rendering the class string
/// verbatim, falling back to a generic message for a class this build
/// does not recognize (server/client version skew) rather than showing
/// nothing or the raw code.
String quarantineReasonMessage(Strings strings, String reason) {
  final key =
      knownQuarantineReasons.contains(reason)
          ? "sync_quarantine_reason_$reason"
          : "sync_quarantine_reason_unknown";
  return strings(key);
}

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

/// The currently mounted [EditorScreen]'s local-only flush barrier, if
/// any - see [AppLifecycleLock]'s didChangeAppLifecycleState, which sits
/// above the Navigator and so has no direct reference to whatever screen
/// is currently pushed, but still needs to flush a pending edit before
/// backgrounding lets the app be paused, screenshotted, or eventually
/// locked. [EditorScreen] registers its commit() in initState and
/// unregisters it in dispose; this app never shows more than one editor
/// at a time, so a single slot is sufficient. Like every other flush
/// barrier in this app, this only waits for the local commit - never for
/// synchronization.
class ActiveEditorFlush {
  static Future<void> Function()? _flush;

  static void register(Future<void> Function() flush) {
    _flush = flush;
  }

  static void unregister(Future<void> Function() flush) {
    // Instance method tear-offs of the same method from the same object
    // are == but not identical (each `obj.method` expression allocates a
    // distinct closure), so equality - not identical() - is the correct
    // comparison here to avoid ever leaving a disposed EditorScreen's
    // commit() registered.
    if (_flush == flush) _flush = null;
  }

  static Future<void> flushIfAny() async {
    await _flush?.call();
  }
}

/// Reports whether the free-text search box already contains one of the
/// backend filter language's special tokens (tag:/after:/before:/
/// deleted:true), mirroring desktop's identical check in SearchBar.tsx.
/// Without this, such a query would be misread as a literal, tokens-and-all
/// substring to match against note titles instead of being sent to the
/// backend to parse as a filter.
bool containsQueryToken(String text) {
  return text
      .trim()
      .split(RegExp(r"\s+"))
      .any(
        (word) =>
            word.startsWith("tag:") ||
            word.startsWith("after:") ||
            word.startsWith("before:") ||
            word == "deleted:true",
      );
}

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
      // Local-only flush barrier: whatever note is open must commit to
      // this device before the app can be paused, screenshotted, or
      // (via lockTimer below) locked - matching desktop's flush before
      // navigation/workspace switch/lock. Never waits for synchronization,
      // unlike the best-effort sync request beside it.
      unawaited(ActiveEditorFlush.flushIfAny());
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
  // Set from OnboardingScreen's optional post-create sync prompt (task
  // 7.2): true only when the user just created a local account and chose
  // "Connect now", so NotesShell opens the server sheet on this one
  // transition and never again on an ordinary unlock.
  bool openServerOnUnlock = false;

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
        onUnlocked:
            ({openServer = false}) => setState(() {
              unlocked = true;
              openServerOnUnlock = openServer;
            }),
      );
    }
    return NotesShell(
      gateway: widget.gateway,
      strings: strings,
      language: widget.language,
      onLanguageChanged: widget.onLanguageChanged,
      onLocked: () => setState(() => unlocked = false),
      openServerOnMount: openServerOnUnlock,
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
  // openServer is only ever true after a fresh local account creation, when
  // the user chose "Connect now" on the optional post-create sync prompt
  // below (task 7.2) - never for an ordinary unlock.
  final void Function({bool openServer}) onUnlocked;
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
  // Set once CreateAccount durably succeeds: from then on this screen
  // shows the optional post-create sync prompt instead of calling
  // widget.onUnlocked immediately (task 7.2, mirroring desktop's task
  // 7.1) - local account creation is never blocked on this choice, but
  // the choice itself is not skipped silently either. Never set on the
  // unlock (returning-user) path.
  bool showSyncPrompt = false;

  Future<void> submit(bool create) async {
    if (passphrase.text.isEmpty) return;
    setState(() {
      busy = true;
      error = null;
    });
    try {
      if (create) {
        await widget.gateway.createAccount(passphrase.text);
        if (mounted) setState(() => showSyncPrompt = true);
      } else {
        await widget.gateway.unlockAccount(passphrase.text);
        widget.onUnlocked();
      }
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
    if (showSyncPrompt) {
      return Scaffold(
        body: SafeArea(
          child: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 480),
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(
                      widget.strings("sync_prompt_title"),
                      textAlign: TextAlign.center,
                      style: Theme.of(context).textTheme.headlineSmall,
                    ),
                    const SizedBox(height: 12),
                    Text(
                      widget.strings("sync_prompt_description"),
                      textAlign: TextAlign.center,
                    ),
                    const SizedBox(height: 24),
                    FilledButton(
                      onPressed: () => widget.onUnlocked(openServer: true),
                      child: Text(widget.strings("sync_prompt_connect")),
                    ),
                    TextButton(
                      onPressed: () => widget.onUnlocked(),
                      child: Text(widget.strings("sync_prompt_skip")),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      );
    }
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
                AutofillGroup(
                  child: TextField(
                    controller: passphrase,
                    obscureText: true,
                    enableSuggestions: false,
                    autocorrect: false,
                    // Platform-native password/autofill behavior (task
                    // 7.2): a real password manager only offers to save or
                    // fill this field when it carries the hint matching
                    // what's actually happening - a new credential while
                    // creating an account, an existing one while unlocking
                    // - rather than a generic obscured text field.
                    autofillHints: [
                      widget.accountExists
                          ? AutofillHints.password
                          : AutofillHints.newPassword,
                    ],
                    decoration: InputDecoration(
                      labelText: widget.strings("passphrase"),
                      border: const OutlineInputBorder(),
                    ),
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
    this.openServerOnMount = false,
    super.key,
  });

  final CoreGateway gateway;
  final Strings strings;
  final String language;
  final ValueChanged<String> onLanguageChanged;
  final VoidCallback onLocked;
  // Opens the server/sync sheet once, right after first build - set when
  // onboarding's optional post-create sync prompt (task 7.2, mirroring
  // desktop's task 7.1) chose "Connect now", so the user lands directly in
  // the connect flow instead of having to find it in settings themselves
  // right after asking for it.
  final bool openServerOnMount;

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
  String syncStatusValue = "local_only";
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
    refreshSyncSummary();
    syncStatusTimer = Timer.periodic(
      const Duration(seconds: 5),
      (_) => refreshSyncSummary(),
    );
    pollEvents();
    syncEventsTimer = Timer.periodic(
      const Duration(seconds: 1),
      (_) => pollEvents(),
    );
    if (widget.openServerOnMount) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) unawaited(showServer());
      });
    }
  }

  Future<void> refreshSyncSummary() async {
    try {
      final summary = await widget.gateway.syncSummary();
      final state = summary["state"] as String? ?? "local_only";
      if (mounted) setState(() => syncStatusValue = state);
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
        final summary = await widget.gateway.syncSummary();
        final state = summary["state"] as String? ?? "local_only";
        if (state == "current") {
          synchronized = true;
          break;
        }
        // "action_required" and "local_only" cannot resolve on their own -
        // give up the wait immediately. Every other state (active, pending,
        // offline, retrying) is a normal, automatically-recovering part of
        // synchronization, never a blocking error: keep waiting for it to
        // resolve within the attempt budget instead of surfacing it as a
        // failure (the bug this replaces - offline used to abort the wait
        // and show an error banner).
        if (state == "action_required" || state == "local_only") break;
        await Future<void>.delayed(const Duration(seconds: 1));
      }
      if (!mounted) return;
      if (synchronized) {
        await refresh();
      } else {
        setState(() => error = widget.strings("workspace_sync_pending"));
      }
      await refreshSyncSummary();
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
      var refreshSummary = false;
      for (final event in events) {
        final sequence = event["sequence"];
        if (sequence is num && sequence.toInt() > eventCursor) {
          eventCursor = sequence.toInt();
        }
        switch (event["type"]) {
          case "workspace_changed":
          case "workspace_synced":
            refreshCollection = true;
            refreshSummary = true;
          case "sync_progress":
            refreshSummary = true;
        }
      }
      if (refreshCollection && mounted) await refresh();
      if (refreshSummary && mounted) await refreshSyncSummary();
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
                      (note["title"] as String).toLowerCase().contains(needle),
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
                  itemBuilder:
                      (context) => [
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
      // Placeholder titles SHALL not become stored titles unless edited
      // (specs/notes-management): unlike a note the user has actually
      // named, an untouched draft's title is never persisted or synced as
      // the literal localized "Untitled" - the note list and editor both
      // already render an empty title as that string for display only
      // (see the "untitled" lookups elsewhere in this file).
      final created = await widget.gateway.createNote(
        "",
        notebookId: notebookId,
      );
      requestCurrentWorkspaceSync(widget.gateway);
      if (!mounted) return;
      if (closeDrawer) Navigator.pop(context);
      await openEditor(created["id"] as String, autoFocusTitle: true);
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
    }
  }

  Future<void> openEditor(String noteId, {bool autoFocusTitle = false}) async {
    await Navigator.push(
      context,
      MaterialPageRoute(
        builder:
            (_) => EditorScreen(
              gateway: widget.gateway,
              strings: widget.strings,
              noteId: noteId,
              autoFocusTitle: autoFocusTitle,
              onNoteListChanged: () => unawaited(refresh()),
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
    await refreshSyncSummary();
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
  final connectionCode = TextEditingController();
  final url = TextEditingController();
  final invite = TextEditingController();
  final fingerprint = TextEditingController();
  final peerIdentity = TextEditingController();
  final peerGrant = TextEditingController();
  String securityMode = "pinned";
  // Server setup prefers the single pasted connection code (task 7.3): URL,
  // TLS policy, and fingerprint only appear once a user explicitly expands
  // this manual/advanced branch.
  bool advancedOpen = false;
  String? error;
  bool busy = false;
  String identityCode = "";
  String grantCode = "";
  String? copied;
  bool sharingBusy = false;
  String syncStatusValue = "local_only";
  bool connectionEnabled = false;
  String connectedURL = "";
  String connectedProtocol = "";
  String connectedSecurityMode = "pinned";
  List<Map<String, dynamic>> workspaces = const [];
  List<Map<String, dynamic>> quarantine = const [];
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
            connectionEnabled = info["enabled"] as bool? ?? false;
            url.text = info["url"] as String? ?? "";
            fingerprint.text = info["fingerprint"] as String? ?? "";
            securityMode = info["security_mode"] as String? ?? "pinned";
            connectedURL = url.text;
            connectedProtocol =
                info["protocol"] as String? ??
                (connectedURL.isEmpty ? "" : "https");
            connectedSecurityMode = securityMode;
          });
        })
        .catchError((_) {
          // No account context yet - the form simply stays empty.
        });
    refreshSyncSummary();
    loadWorkspaces();
    syncStatusTimer = Timer.periodic(
      const Duration(seconds: 3),
      (_) => refreshSyncSummary(),
    );
  }

  Future<void> refreshSyncSummary() async {
    try {
      final summary = await widget.gateway.syncSummary();
      if (mounted) {
        setState(
          () => syncStatusValue = summary["state"] as String? ?? "local_only",
        );
      }
    } catch (_) {
      // Keep showing the last known status; the periodic timer retries.
    }
    // Unsafe-incoming-operation details (specs/sync-engine's "Safe
    // incoming-operation recovery" requirement) share the same refresh
    // cadence as the status pill above.
    await loadQuarantine();
  }

  Future<void> loadQuarantine() async {
    try {
      final entries = await widget.gateway.listSyncQuarantine();
      if (mounted) setState(() => quarantine = entries);
    } catch (_) {
      // No account/server context yet, or a transient bridge error; the
      // periodic refresh above retries.
    }
  }

  Future<void> retryQuarantine(String operationId) async {
    try {
      await widget.gateway.retryQuarantined(operationId);
      await loadQuarantine();
      await refreshSyncSummary();
    } catch (failure) {
      if (mounted) {
        setState(() => error = describeFailure(widget.strings, failure));
      }
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

  /// Waits (up to a bounded budget) for the just-joined or just-switched
  /// workspace's first synchronization cycle to complete. "action_required"
  /// and "local_only" cannot resolve on their own and end the wait early;
  /// every other state (active, pending, offline, retrying) is normal,
  /// automatically-recovering synchronization, never a blocking error, so
  /// the wait keeps polling for it to reach "current" within the budget
  /// instead of surfacing it as a failure.
  Future<bool> waitForInitialWorkspaceSync() async {
    for (var attempt = 0; attempt < 30; attempt++) {
      final summary = await widget.gateway.syncSummary();
      final state = summary["state"] as String? ?? "local_only";
      if (state == "current") return true;
      if (state == "action_required" || state == "local_only") return false;
      await Future<void>.delayed(const Duration(seconds: 1));
    }
    return false;
  }

  @override
  void dispose() {
    syncStatusTimer?.cancel();
    connectionCode.dispose();
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
          const SizedBox(height: 12),
          // Only surfaced when there is something to act on: an
          // always-visible "no issues" row would push later sections (e.g.
          // the workspace list) further down for every ordinary user who
          // never hits an unsafe incoming operation.
          if (quarantine.isNotEmpty ||
              syncStatusValue == "action_required") ...[
            Text(
              widget.strings("sync_journal_title"),
              style: Theme.of(context).textTheme.titleMedium,
            ),
            if (quarantine.isEmpty)
              Text(widget.strings("sync_journal_empty"))
            else
              for (final entry in quarantine)
                ListTile(
                  contentPadding: EdgeInsets.zero,
                  title: SelectableText(entry["operation_id"] as String? ?? ""),
                  subtitle: Text(
                    quarantineReasonMessage(
                      widget.strings,
                      entry["reason"] as String? ?? "",
                    ),
                  ),
                  trailing: TextButton(
                    onPressed:
                        () => retryQuarantine(entry["operation_id"] as String),
                    child: Text(widget.strings("retry")),
                  ),
                ),
          ],
          if (connectionEnabled && connectedURL.isNotEmpty)
            Card(
              child: Padding(
                padding: const EdgeInsets.all(12),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      widget.strings("current_server"),
                      style: Theme.of(context).textTheme.labelMedium,
                    ),
                    SelectableText(connectedURL),
                    const SizedBox(height: 8),
                    Text(
                      widget.strings("connection_protocol"),
                      style: Theme.of(context).textTheme.labelMedium,
                    ),
                    Text(
                      connectedProtocol == "https"
                          ? widget.strings("protocol_https")
                          : connectedProtocol,
                    ),
                    const SizedBox(height: 8),
                    Text(
                      widget.strings("certificate_verification"),
                      style: Theme.of(context).textTheme.labelMedium,
                    ),
                    Text(widget.strings(connectedSecurityMode)),
                  ],
                ),
              ),
            ),
          TextField(
            controller: connectionCode,
            maxLines: 3,
            decoration: InputDecoration(
              labelText: widget.strings("connection_code"),
              helperText: widget.strings("connection_code_hint"),
              helperMaxLines: 4,
            ),
          ),
          const SizedBox(height: 12),
          FilledButton(
            onPressed: busy ? null : connectWithCode,
            child: Text(widget.strings("connect_with_code")),
          ),
          if (error != null)
            Text(
              error!,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          const SizedBox(height: 12),
          TextButton(
            onPressed: () => setState(() => advancedOpen = !advancedOpen),
            child: Text(widget.strings("advanced_server_setup")),
          ),
          if (advancedOpen) ...[
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
            const SizedBox(height: 12),
            Text(widget.strings("certificate_verification")),
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
            const SizedBox(height: 12),
            FilledButton(
              onPressed: busy ? null : connectManually,
              child: Text(
                widget.strings(
                  connectionEnabled ? "apply_server_changes" : "connect",
                ),
              ),
            ),
          ],
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
      // The open editor, if any, belongs to the workspace being switched
      // away from - flush it first so a still-pending edit commits before
      // that workspace's notes become unreachable, matching the same
      // flush-before-navigate barrier used elsewhere (background, lock,
      // revision restore).
      await ActiveEditorFlush.flushIfAny();
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

  // The simple path: a pasted connection code (from a QR scan or copied
  // text) carries the server URL, invite, TLS policy, and fingerprint
  // together, so it never touches the advanced manual fields below.
  Future<void> connectWithCode() => performConnect({
    "url": "",
    "invite_code": "",
    "fingerprint": "",
    "security_mode": "",
    "qr_code": connectionCode.text.trim(),
    "device_name": "Android",
  });

  // The advanced path: URL, invite, TLS policy, and fingerprint are all
  // entered manually, for setups without a connection code to paste.
  Future<void> connectManually() => performConnect({
    "url": url.text.trim(),
    "invite_code": invite.text.trim(),
    "fingerprint": fingerprint.text.trim(),
    "security_mode": securityMode,
    "qr_code": "",
    "device_name": "Android",
  });

  Future<void> performConnect(Map<String, dynamic> config) async {
    setState(() {
      busy = true;
      error = null;
    });
    try {
      await widget.gateway.connectServer(config);
      // The connection code path leaves url/fingerprint/securityMode empty
      // locally (the server derives them from the pasted code), so read
      // the actual resulting connection back instead of assuming the
      // request's own fields describe it.
      final info = await widget.gateway.syncConnectionInfo();
      if (mounted) {
        setState(() {
          connectionEnabled = true;
          connectedURL = info["url"] as String? ?? "";
          connectedProtocol = info["protocol"] as String? ?? "https";
          connectedSecurityMode = info["security_mode"] as String? ?? "pinned";
          securityMode = connectedSecurityMode;
          url.text = connectedURL;
          fingerprint.text = info["fingerprint"] as String? ?? "";
        });
      }
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
          const Divider(height: 32),
          DiagnosticsSection(gateway: widget.gateway, strings: widget.strings),
        ],
      ),
    );
  }
}

/// DiagnosticsSection covers task 4.2's Android user diagnostics screen:
/// the always-shown summary, plus an expandable technical-details layer
/// and a shared copy-diagnostics flow (specs/product-experience's
/// "Layered privacy-preserving diagnostics" requirement). Both layers
/// load lazily, on first expand, so opening Settings for an unrelated
/// reason never fetches diagnostics the user did not ask for.
class DiagnosticsSection extends StatefulWidget {
  const DiagnosticsSection({
    required this.gateway,
    required this.strings,
    super.key,
  });

  final CoreGateway gateway;
  final Strings strings;

  @override
  State<DiagnosticsSection> createState() => _DiagnosticsSectionState();
}

class _DiagnosticsSectionState extends State<DiagnosticsSection> {
  bool expanded = false;
  Map<String, dynamic>? summary;
  String? summaryError;
  bool loadingSummary = false;

  bool technicalExpanded = false;
  Map<String, dynamic>? technical;
  String? technicalError;
  bool loadingTechnical = false;

  String? copyError;
  bool copied = false;

  Future<void> loadSummary() async {
    setState(() {
      loadingSummary = true;
      summaryError = null;
    });
    try {
      final value = await widget.gateway.diagnosticSummary(appVersion);
      if (mounted) setState(() => summary = value);
    } catch (failure) {
      if (mounted) {
        setState(() => summaryError = describeFailure(widget.strings, failure));
      }
    } finally {
      if (mounted) setState(() => loadingSummary = false);
    }
  }

  void toggleExpanded() {
    final next = !expanded;
    setState(() => expanded = next);
    if (next && summary == null && !loadingSummary) {
      unawaited(loadSummary());
    }
  }

  Future<void> toggleTechnical() async {
    final next = !technicalExpanded;
    setState(() => technicalExpanded = next);
    if (next && technical == null && !loadingTechnical) {
      setState(() {
        loadingTechnical = true;
        technicalError = null;
      });
      try {
        final value = await widget.gateway.technicalDiagnostics();
        if (mounted) setState(() => technical = value);
      } catch (failure) {
        if (mounted) {
          setState(
            () => technicalError = describeFailure(widget.strings, failure),
          );
        }
      } finally {
        if (mounted) setState(() => loadingTechnical = false);
      }
    }
  }

  Future<void> handleCopy() async {
    setState(() => copyError = null);
    try {
      final bundle = await widget.gateway.copyDiagnostics(appVersion);
      await Clipboard.setData(ClipboardData(text: bundle));
      if (mounted) setState(() => copied = true);
    } catch (failure) {
      if (mounted) {
        setState(() => copyError = describeFailure(widget.strings, failure));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final current = summary;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        TextButton(
          onPressed: toggleExpanded,
          child: Text(widget.strings("diagnostics_title")),
        ),
        if (expanded)
          if (loadingSummary)
            const Padding(
              padding: EdgeInsets.symmetric(vertical: 8),
              child: CircularProgressIndicator(),
            )
          else if (summaryError != null)
            Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  summaryError!,
                  style: TextStyle(color: Theme.of(context).colorScheme.error),
                ),
                TextButton(
                  onPressed: () => unawaited(loadSummary()),
                  child: Text(widget.strings("retry")),
                ),
              ],
            )
          else if (current != null)
            Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                _diagnosticsRow(
                  widget.strings("diagnostics_app_version"),
                  current["app_version"] as String? ?? "",
                ),
                _diagnosticsRow(
                  widget.strings("diagnostics_platform"),
                  current["platform"] as String? ?? "",
                ),
                _diagnosticsRow(
                  widget.strings("diagnostics_sync_configured"),
                  current["sync_configured"] == true
                      ? widget.strings("diagnostics_yes")
                      : widget.strings("diagnostics_no"),
                ),
                _diagnosticsRow(
                  widget.strings("diagnostics_last_successful_sync"),
                  ((current["last_successful_sync_unix_ms"] as num?)?.toInt() ??
                              0) >
                          0
                      ? DateTime.fromMillisecondsSinceEpoch(
                        (current["last_successful_sync_unix_ms"] as num)
                            .toInt(),
                      ).toLocal().toString()
                      : widget.strings("diagnostics_never"),
                ),
                _diagnosticsRow(
                  widget.strings("diagnostics_pending_count"),
                  "${current["pending_count"] ?? 0}",
                ),
                _diagnosticsRow(
                  widget.strings("diagnostics_connection_state"),
                  widget.strings("sync_status_${current["connection_state"]}"),
                ),
                _diagnosticsRow(
                  widget.strings("diagnostics_backup"),
                  widget.strings(
                    "backup_health_${(current["backup"] as Map<String, dynamic>?)?["health"] ?? "unknown"}",
                  ),
                ),
                _diagnosticsRow(
                  widget.strings("diagnostics_storage_usage"),
                  _formatBytes(
                    (current["storage_usage_bytes"] as num?)?.toInt() ?? 0,
                  ),
                ),
                _diagnosticsRow(
                  widget.strings("diagnostics_database"),
                  widget.strings("database_health_${current["database"]}"),
                ),
                _diagnosticsRow(
                  widget.strings("diagnostics_update"),
                  widget.strings("update_status_${current["update"]}"),
                ),
                TextButton(
                  onPressed: () => unawaited(toggleTechnical()),
                  child: Text(
                    widget.strings(
                      technicalExpanded
                          ? "diagnostics_hide_technical_details"
                          : "diagnostics_show_technical_details",
                    ),
                  ),
                ),
                if (technicalExpanded)
                  if (loadingTechnical)
                    const Padding(
                      padding: EdgeInsets.symmetric(vertical: 8),
                      child: CircularProgressIndicator(),
                    )
                  else if (technicalError != null)
                    Text(
                      technicalError!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    )
                  else if (technical != null)
                    _technicalDetails(technical!),
                if (copyError != null)
                  Text(
                    copyError!,
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.error,
                    ),
                  ),
                TextButton(
                  onPressed: () => unawaited(handleCopy()),
                  child: Text(
                    widget.strings(
                      copied ? "diagnostics_copied" : "diagnostics_copy",
                    ),
                  ),
                ),
              ],
            ),
      ],
    );
  }

  Widget _technicalDetails(Map<String, dynamic> technical) {
    final quarantined =
        (technical["quarantined_operation_ids"] as List<dynamic>? ?? [])
            .cast<String>();
    final retryInMs = (technical["retry_in_ms"] as num?)?.toInt() ?? 0;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _diagnosticsRow(
          widget.strings("diagnostics_workspace_id"),
          technical["workspace_id"] as String? ?? "",
        ),
        _diagnosticsRow(
          widget.strings("diagnostics_device_id"),
          technical["device_id"] as String? ?? "",
        ),
        _diagnosticsRow(
          widget.strings("diagnostics_last_error_class"),
          (technical["last_error_class"] as String?)?.isNotEmpty == true
              ? technical["last_error_class"] as String
              : widget.strings("diagnostics_none"),
        ),
        _diagnosticsRow(
          widget.strings("diagnostics_pending_operation_count"),
          "${technical["pending_operation_count"] ?? 0}",
        ),
        _diagnosticsRow(
          widget.strings("diagnostics_quarantined_operation_ids"),
          quarantined.isEmpty
              ? widget.strings("diagnostics_none")
              : quarantined.join(", "),
        ),
        _diagnosticsRow(
          widget.strings("diagnostics_cursor"),
          "${technical["cursor_sequence"] ?? 0}@${technical["cursor_epoch"] ?? 0}",
        ),
        _diagnosticsRow(
          widget.strings("diagnostics_retry_count"),
          "${technical["retry_count"] ?? 0}",
        ),
        _diagnosticsRow(
          widget.strings("diagnostics_retry_in"),
          retryInMs > 0
              ? "${(retryInMs / 1000).ceil()}s"
              : widget.strings("diagnostics_none"),
        ),
        _diagnosticsRow(
          widget.strings("diagnostics_transport_protocol"),
          (technical["transport_protocol"] as String?)?.isNotEmpty == true
              ? technical["transport_protocol"] as String
              : widget.strings("diagnostics_none"),
        ),
        _diagnosticsRow(
          widget.strings("diagnostics_transport_security_mode"),
          (technical["transport_security_mode"] as String?)?.isNotEmpty == true
              ? technical["transport_security_mode"] as String
              : widget.strings("diagnostics_none"),
        ),
        _diagnosticsRow(
          widget.strings("diagnostics_transport_url"),
          (technical["transport_url"] as String?)?.isNotEmpty == true
              ? technical["transport_url"] as String
              : widget.strings("diagnostics_none"),
        ),
        _diagnosticsRow(
          widget.strings("diagnostics_migration_version"),
          "${technical["migration_version"] ?? 0}",
        ),
      ],
    );
  }

  Widget _diagnosticsRow(String label, String value) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 2),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(width: 160, child: Text(label)),
        Expanded(child: SelectableText(value)),
      ],
    ),
  );
}

String _formatBytes(int bytes) {
  const units = ["B", "KB", "MB", "GB"];
  double value = bytes.toDouble();
  var unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex++;
  }
  return "${value.toStringAsFixed(unitIndex == 0 ? 0 : 1)} ${units[unitIndex]}";
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
  late Future<Map<String, dynamic>> status = widget.gateway.backupStatus();
  late Future<int> estimatedSize = widget.gateway.estimateBackupSize();
  String? error;
  // spaceError tracks whether the current error came from insufficient
  // storage, so the UI can offer the one concrete remedy that helps here -
  // choosing a different destination - rather than just a static message
  // (specs/backup-and-recovery.md, "Crash-safe backup publication and
  // storage pressure": "offer cache cleanup, retention adjustment, or
  // destination change as applicable").
  bool spaceError = false;

  void reload() => setState(() {
    backups = widget.gateway.listBackups();
    status = widget.gateway.backupStatus();
    estimatedSize = widget.gateway.estimateBackupSize();
  });

  void _setBackupError(Object failure) {
    if (!mounted) return;
    final isSpaceError = failure.toString().toLowerCase().contains("space");
    setState(() {
      spaceError = isSpaceError;
      error =
          isSpaceError
              ? widget.strings("backup_capacity")
              : describeFailure(widget.strings, failure);
    });
  }

  // _backupStatusSummary covers task 5.4's Data settings backup status
  // surface: the catalog's health, last verified time, and location -
  // specs/backup-and-recovery.md's "Understandable verified backup status"
  // requirement - shown above the catalog list itself rather than only
  // inside the separate Diagnostics screen.
  Widget _backupStatusSummary() => FutureBuilder<Map<String, dynamic>>(
    future: status,
    builder: (context, snapshot) {
      final value = snapshot.data;
      if (value == null) return const SizedBox.shrink();
      final health = value["health"] as String? ?? "unknown";
      final lastVerifiedMS =
          (value["last_verified_unix_ms"] as num?)?.toInt() ?? 0;
      final location = value["location"] as String? ?? "";
      return Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            _diagnosticsRow(
              widget.strings("diagnostics_backup"),
              widget.strings("backup_health_$health"),
            ),
            _diagnosticsRow(
              widget.strings("backup_last_verified"),
              lastVerifiedMS > 0
                  ? DateTime.fromMillisecondsSinceEpoch(
                    lastVerifiedMS,
                  ).toLocal().toString()
                  : widget.strings("diagnostics_never"),
            ),
            if (location.isNotEmpty)
              _diagnosticsRow(widget.strings("backup_location"), location),
            if (health == "corrupt")
              Text(
                widget.strings("backup_corrupt_explanation"),
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
          ],
        ),
      );
    },
  );

  Widget _diagnosticsRow(String label, String value) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 2),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(width: 140, child: Text(label)),
        Expanded(child: SelectableText(value)),
      ],
    ),
  );

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        _backupStatusSummary(),
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
                  _setBackupError(failure);
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
                  _setBackupError(failure);
                }
              },
              icon: const Icon(Icons.backup),
              label: Text(widget.strings("backup_now")),
            ),
          ],
        ),
        FutureBuilder<int>(
          future: estimatedSize,
          builder: (context, snapshot) {
            final size = snapshot.data;
            if (size == null) return const SizedBox.shrink();
            return Text(
              "${widget.strings("backup_estimated_size")}: ${_formatBytes(size)}",
            );
          },
        ),
        if (error != null)
          Text(
            error!,
            style: TextStyle(color: Theme.of(context).colorScheme.error),
          ),
        if (spaceError)
          OutlinedButton(
            onPressed: widget.gateway.selectBackupDestination,
            child: Text(widget.strings("backup_destination")),
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
                      onPressed:
                          () => openRestoreOptions(backup["id"] as String),
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

  // openRestoreOptions covers task 5.5's restore planner: a read-only
  // preview of the backup's notes, a dry-run plan classifying each note as
  // new/updated/unchanged before committing to the non-destructive
  // "restore selected as new notes" (RestoreSelective, mirroring desktop's
  // BackupsPanel), or the separately confirmed, destructive "replace
  // everything" (RestoreWhole) - both already take a mandatory pre-restore
  // safety backup in core/account, so this sheet only needs to surface the
  // choice and the result.
  void openRestoreOptions(String backupId) {
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      builder:
          (sheetContext) => _RestoreOptionsSheet(
            gateway: widget.gateway,
            strings: widget.strings,
            backupId: backupId,
            onRestored: () {
              Navigator.of(sheetContext).pop();
              reload();
            },
          ),
    );
  }
}

class _RestoreOptionsSheet extends StatefulWidget {
  const _RestoreOptionsSheet({
    required this.gateway,
    required this.strings,
    required this.backupId,
    required this.onRestored,
  });

  final CoreGateway gateway;
  final Strings strings;
  final String backupId;
  final VoidCallback onRestored;

  @override
  State<_RestoreOptionsSheet> createState() => _RestoreOptionsSheetState();
}

class _RestoreOptionsSheetState extends State<_RestoreOptionsSheet> {
  late Future<Map<String, dynamic>> preview = widget.gateway.previewBackup(
    widget.backupId,
  );
  Map<String, dynamic>? plan;
  Set<String> selected = {};
  bool planning = false;
  bool restoring = false;
  String? error;

  Future<void> loadPlan() async {
    setState(() {
      planning = true;
      error = null;
    });
    try {
      final result = await widget.gateway.planRestore(
        widget.backupId,
        const [],
      );
      final entries =
          (result["entries"] as List<dynamic>? ?? [])
              .cast<Map<String, dynamic>>();
      if (!mounted) return;
      setState(() {
        plan = result;
        selected =
            entries
                .where((entry) => entry["kind"] != "unchanged")
                .map((entry) => entry["note_id"] as String)
                .toSet();
      });
    } catch (failure) {
      if (mounted)
        setState(() => error = describeFailure(widget.strings, failure));
    } finally {
      if (mounted) setState(() => planning = false);
    }
  }

  Future<void> restoreSelected() async {
    if (selected.isEmpty) return;
    setState(() {
      restoring = true;
      error = null;
    });
    try {
      final result = await widget.gateway.restoreSelective(
        widget.backupId,
        selected.toList(),
      );
      final newNoteIds = result["new_note_ids"] as List<dynamic>? ?? [];
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            "${widget.strings("restore_selected_success")}: ${newNoteIds.length}",
          ),
        ),
      );
      widget.onRestored();
    } catch (failure) {
      if (mounted)
        setState(() => error = describeFailure(widget.strings, failure));
    } finally {
      if (mounted) setState(() => restoring = false);
    }
  }

  Future<void> confirmWholeRestore() async {
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
    if (confirmed != true) return;
    setState(() {
      restoring = true;
      error = null;
    });
    try {
      await widget.gateway.restoreBackup(widget.backupId);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(widget.strings("restore_success"))),
      );
      widget.onRestored();
    } catch (failure) {
      if (mounted)
        setState(() => error = describeFailure(widget.strings, failure));
    } finally {
      if (mounted) setState(() => restoring = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      expand: false,
      initialChildSize: 0.75,
      builder:
          (context, scrollController) => Padding(
            padding: EdgeInsets.only(
              left: 16,
              right: 16,
              top: 16,
              bottom: MediaQuery.of(context).viewInsets.bottom + 16,
            ),
            child: ListView(
              controller: scrollController,
              children: [
                Text(
                  widget.strings("restore_preview_title"),
                  style: Theme.of(context).textTheme.titleMedium,
                ),
                const SizedBox(height: 8),
                FutureBuilder<Map<String, dynamic>>(
                  future: preview,
                  builder: (context, snapshot) {
                    if (!snapshot.hasData) {
                      return const Padding(
                        padding: EdgeInsets.symmetric(vertical: 16),
                        child: Center(child: CircularProgressIndicator()),
                      );
                    }
                    final titles =
                        (snapshot.data!["note_titles"] as List<dynamic>? ?? [])
                            .cast<String>();
                    if (titles.isEmpty) {
                      return Text(widget.strings("restore_preview_empty"));
                    }
                    return Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: titles.map(Text.new).toList(),
                    );
                  },
                ),
                const SizedBox(height: 16),
                if (error != null)
                  Text(
                    error!,
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.error,
                    ),
                  ),
                if (plan == null)
                  FilledButton(
                    onPressed: planning ? null : loadPlan,
                    child:
                        planning
                            ? const SizedBox(
                              width: 16,
                              height: 16,
                              child: CircularProgressIndicator(strokeWidth: 2),
                            )
                            : Text(widget.strings("restore_selected_button")),
                  )
                else ...[
                  Text(
                    widget.strings("restore_plan_title"),
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  Text(
                    "${widget.strings("restore_required_storage")}: "
                    "${_formatBytes((plan!["required_storage_bytes"] as num?)?.toInt() ?? 0)}",
                  ),
                  ...((plan!["entries"] as List<dynamic>? ?? [])
                      .cast<Map<String, dynamic>>()
                      .map((entry) {
                        final noteId = entry["note_id"] as String;
                        final kind = entry["kind"] as String? ?? "unchanged";
                        final title = entry["title"] as String? ?? "";
                        return CheckboxListTile(
                          value: selected.contains(noteId),
                          onChanged:
                              (value) => setState(() {
                                if (value == true) {
                                  selected.add(noteId);
                                } else {
                                  selected.remove(noteId);
                                }
                              }),
                          title: Text(
                            title.isNotEmpty ? title : widget.strings("title"),
                          ),
                          subtitle: Text(widget.strings("restore_kind_$kind")),
                        );
                      })),
                  FilledButton(
                    onPressed:
                        restoring || selected.isEmpty ? null : restoreSelected,
                    child:
                        restoring
                            ? const SizedBox(
                              width: 16,
                              height: 16,
                              child: CircularProgressIndicator(strokeWidth: 2),
                            )
                            : Text(widget.strings("restore_selected_button")),
                  ),
                ],
                const Divider(height: 32),
                OutlinedButton(
                  onPressed: restoring ? null : confirmWholeRestore,
                  child: Text(widget.strings("replace_restore")),
                ),
                const SizedBox(height: 8),
                TextButton(
                  onPressed: () => Navigator.of(context).pop(),
                  child: Text(widget.strings("close")),
                ),
              ],
            ),
          ),
    );
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
    required this.onNoteListChanged,
    this.autoFocusTitle = false,
    super.key,
  });

  final CoreGateway gateway;
  final Strings strings;
  final String noteId;
  // True only for a note created from this mobile session: it should
  // receive focus immediately (specs/notes-management's "Creating a note
  // SHALL expose an editable note immediately ... and SHALL focus its
  // title or body"), and is still an untouched empty draft eligible for
  // silent removal if the user leaves without editing it - see
  // _EditorScreenState's PopScope handler and _touched.
  final bool autoFocusTitle;
  // Called after this note is deleted (once, on the way out) and again
  // if the user later taps Undo on the snackbar - both happen after this
  // screen has already popped, so the caller (NotesShell.refresh) is
  // reached via this captured callback rather than any BuildContext of
  // this screen's own, which is gone by then.
  final VoidCallback onNoteListChanged;

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
  // True once an edit exists that no accepted commit has covered yet.
  // Distinct from saveState below: dirty gates whether a pop or dispose
  // must still attempt a commit, and survives a discarded (stale) commit
  // completion that a newer edit has already superseded.
  bool dirty = false;
  List<Map<String, dynamic>> attachments = [];
  bool capturingPhoto = false;
  List<Map<String, dynamic>> allTags = [];
  List<String> noteTagIds = [];
  // True once the title, body, a tag, or an attachment has actually been
  // edited this session - only meaningful while widget.autoFocusTitle
  // marks this as a freshly created draft (see the PopScope handler
  // below): "Creating a note ... SHALL expose an editable note
  // immediately" but "Placeholder titles SHALL not become stored titles
  // unless edited", and an abandoned untouched draft "may [be removed]
  // without creating a visible note or synchronization error"
  // (specs/notes-management).
  bool _touched = false;

  // How long an edit waits, with no further edits, before it is committed
  // to the Go core - matching desktop/frontend's useNoteDocument. Chosen
  // to keep keystrokes from each individually round-tripping through the
  // platform channel while still saving promptly once the user pauses.
  static const _commitDebounce = Duration(milliseconds: 800);
  final _tracker = CommitTracker();
  Timer? _commitTimer;
  // The requestId of the commit currently awaiting its gateway response,
  // if any - used to cancel it when a newer edit needs to commit first
  // (see commit()) and to recognize its own completion as self-canceled
  // rather than a real failure.
  String? _inFlightRequestId;
  final Set<String> _selfCanceledRequestIds = {};
  int _requestCounter = 0;
  // The CRDT state commit() must build its next edit against (task 6.8):
  // set from getNote's own base_revision on load and after a revision
  // restore, then advanced to each successful saveNoteCancelable call's
  // returned base_revision - never left stale, since a commit built
  // against an outdated base_revision would still risk silently
  // discarding a remote merge that landed since.
  String _baseRevision = "";

  /// The closed local-save state for the status text in the app bar (see
  /// build()), or null before this note session's first commit attempt -
  /// the UI treats that the same as "saved", since there is nothing
  /// pending or at risk yet.
  LocalSaveState? saveState;

  @override
  void initState() {
    super.initState();
    ActiveEditorFlush.register(commit);
    // Instant creation and durable automatic save (specs/notes-management):
    // "Creating a note SHALL expose an editable note immediately without a
    // modal and SHALL focus its title or body." Requesting focus here,
    // before the first frame even builds, is safe: FocusNode queues the
    // request until the Focus widget it is attached to (the title
    // TextField in build(), always present regardless of `loading`) is
    // actually in the tree.
    if (widget.autoFocusTitle) titleFocusNode.requestFocus();
    widget.gateway.getNote(widget.noteId).then((value) {
      if (!mounted) return;
      final note = value["note"] as Map<String, dynamic>;
      title.text = note["title"] as String;
      _baseRevision = value["base_revision"] as String;
      final quillBody = QuillController(
        document: Document.fromDelta(markdownToDelta(value["body"] as String)),
        selection: const TextSelection.collapsed(offset: 0),
        // Degrades a rich-text paste's formatting to the canonical
        // semantic format set (see paste_format.dart), matching desktop's
        // own clipboard matcher in
        // desktop/frontend/src/editor/NoteEditor.tsx: without this, a
        // pasted heading level 4-6 or an unsupported attribute (underline,
        // color, font, ...) would round-trip through the Document
        // harmlessly but silently vanish only later, when this note's
        // body is next saved as Markdown.
        config: QuillControllerConfig(
          clipboardConfig: QuillClipboardConfig(
            onRichTextPaste:
                (delta, isExternal) async => stripUnsupportedFormats(delta),
          ),
        ),
      );
      quillBody.addListener(markDirty);
      setState(() {
        body = quillBody;
        loading = false;
      });
      title.addListener(markDirty);
    });
    refreshAttachments();
    refreshTags();
  }

  void markDirty() {
    if (!mounted) return;
    // dirty must go through setState: PopScope's build() reads it via
    // canPop: !dirty, so a plain field mutation here would leave that
    // stale (still allowing an unflushed pop) until some unrelated
    // rebuild happened to occur first - a real regression found by
    // testing a pop attempted immediately after typing, before the
    // 800ms debounce's own eventual setState.
    if (!dirty) setState(() => dirty = true);
    _touched = true;
    _tracker.dirty();
    _commitTimer?.cancel();
    _commitTimer = Timer(_commitDebounce, () => unawaited(commit()));
  }

  String _newCommitRequestId() =>
      "editor-${widget.noteId}-${DateTime.now().microsecondsSinceEpoch}-${++_requestCounter}";

  /// Commits the current title and body right now, bypassing the
  /// debounce. Safe to call with nothing dirty: it is then a no-op. Never
  /// throws: a failed commit is reported through saveState (and dirty
  /// stays true so the next commit - the next debounce, or an explicit
  /// flush like this one - resends the current content) instead of
  /// propagating to the caller.
  Future<void> commit() async {
    _commitTimer?.cancel();
    if (!dirty || body == null) return;

    final previous = _inFlightRequestId;
    if (previous != null) {
      // Cancel rather than let a superseded commit race a fresh read of
      // the note's current state - see saveNoteCancelable's doc comment
      // on why an overlapping commit is not just a stale-status risk here
      // but a real data-loss one.
      _selfCanceledRequestIds.add(previous);
      unawaited(widget.gateway.cancelRequest(previous).catchError((_) {}));
    }

    final generation = _tracker.current();
    final requestId = _newCommitRequestId();
    _inFlightRequestId = requestId;
    dirty = false;
    if (mounted) setState(() => saveState = LocalSaveState.saving);

    final markdown = deltaToMarkdown(body!.document.toDelta());
    final usedBaseRevision = _baseRevision;
    try {
      final newBaseRevision = await widget.gateway.saveNoteCancelable(
        requestId,
        widget.noteId,
        title.text,
        markdown,
        usedBaseRevision,
      );
      if (_inFlightRequestId == requestId) _inFlightRequestId = null;
      if (_selfCanceledRequestIds.remove(requestId)) return;
      _baseRevision = newBaseRevision;
      requestCurrentWorkspaceSync(widget.gateway);
      final accepted = _tracker.accept(generation, true);
      if (accepted != null && mounted) setState(() => saveState = accepted);
    } catch (_) {
      if (_inFlightRequestId == requestId) _inFlightRequestId = null;
      if (_selfCanceledRequestIds.remove(requestId)) return;
      dirty = true;
      final accepted = _tracker.accept(generation, false);
      if (accepted != null && mounted) setState(() => saveState = accepted);
    }
  }

  String saveStateLabel() => switch (saveState) {
    LocalSaveState.saving => widget.strings("save_state_saving"),
    LocalSaveState.couldNotSave => widget.strings("save_state_could_not_save"),
    LocalSaveState.saved || null => widget.strings("save_state_saved"),
  };

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
    _touched = true;
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
    _touched = true;
    setState(() => capturingPhoto = true);
    try {
      await widget.gateway.capturePhoto(widget.noteId);
      requestCurrentWorkspaceSync(widget.gateway);
      await refreshAttachments();
    } catch (failure) {
      // The user backing out of the content-URI picker is not a failure to
      // report (task 6.5: consistent cancellation behavior) - only a
      // genuine add failure gets a snackbar, with a Retry action that
      // simply re-runs this whole flow: Android's SAF content URIs are not
      // safely re-readable after the fact, so re-prompting the picker
      // (rather than re-attempting the same bytes, as desktop's queue
      // does) is this platform's equivalent retry.
      if (failure is PlatformException && failure.code == "canceled") return;
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(describeFailure(widget.strings, failure)),
            action: SnackBarAction(
              label: widget.strings("retry"),
              onPressed: capturePhoto,
            ),
          ),
        );
      }
    } finally {
      if (mounted) setState(() => capturingPhoto = false);
    }
  }

  // Recoverable ordinary note deletion (specs/notes-management): removes
  // the note immediately and offers undo via a snackbar, with no
  // confirmation prompt - unlike notebook deletion or a whole backup
  // restore, which stay behind their own explicit confirmations since
  // this task's recovery policy does not cover them.
  Future<void> deleteNote() async {
    final noteId = widget.noteId;
    final gateway = widget.gateway;
    final onNoteListChanged = widget.onNoteListChanged;
    try {
      await gateway.deleteNote(noteId, true);
      requestCurrentWorkspaceSync(gateway);
    } catch (failure) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(describeFailure(widget.strings, failure))),
        );
      }
      return;
    }
    if (!mounted) return;
    // Captured before popping: this screen's own context is gone once
    // Navigator.pop returns, but the snackbar (and, if tapped, Undo) must
    // still show on whatever screen becomes visible underneath.
    final messenger = ScaffoldMessenger.of(context);
    setState(() => dirty = false);
    Navigator.pop(context);
    onNoteListChanged();
    messenger.showSnackBar(
      SnackBar(
        content: Text(widget.strings("note_deleted")),
        action: SnackBarAction(
          label: widget.strings("undo"),
          onPressed: () {
            // Offline-capable: deleteNote(..., false) is the same signed
            // local commit path as any other edit, so it durably un-
            // tombstones the note now and synchronizes later, with no
            // network required (specs/notes-management's "note returns
            // from local state and the resulting operations synchronize
            // later" scenario).
            unawaited(
              gateway.deleteNote(noteId, false).then((_) {
                requestCurrentWorkspaceSync(gateway);
                onNoteListChanged();
              }),
            );
          },
        ),
      ),
    );
  }

  @override
  void dispose() {
    ActiveEditorFlush.unregister(commit);
    _commitTimer?.cancel();
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
          await commit();
          if (context.mounted) Navigator.pop(context);
          return;
        }
        if (didPop && widget.autoFocusTitle && !_touched) {
          // Empty draft is abandoned (specs/notes-management): the client
          // "may remove the empty draft without creating a visible note
          // or synchronization error" - a best-effort local cleanup, not
          // surfaced if it fails, matching every other flush/commit
          // barrier's "never block or alarm the user" behavior.
          unawaited(
            widget.gateway
                .deleteNote(widget.noteId, true)
                .then((_) => requestCurrentWorkspaceSync(widget.gateway))
                .catchError((_) {}),
          );
        }
      },
      child: Scaffold(
        appBar: AppBar(
          title: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              TextField(
                controller: title,
                focusNode: titleFocusNode,
                decoration: InputDecoration(
                  hintText: widget.strings("title"),
                  border: InputBorder.none,
                  isDense: true,
                ),
              ),
              Text(
                saveStateLabel(),
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ],
          ),
          actions: [
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
                label: Text(
                  widget.strings(capturingPhoto ? "photo_adding" : "photo"),
                ),
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
              _baseRevision = value["base_revision"] as String;
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
    final fromId =
        oldestFirstIndex > 0 ? all[oldestFirstIndex - 1]["id"] as String : "";
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
      // Local-only flush barrier: this sheet is always opened from within
      // the note's own open EditorScreen, so any edit still pending there
      // must commit first - otherwise restoring rewrites the note out
      // from under it, and reopening the editor afterward (onRestored,
      // below) would silently reintroduce the stale edit on top of the
      // just-restored content, exactly as desktop's NoteEditorPane
      // guards against before RestoreRevision.
      await ActiveEditorFlush.flushIfAny();
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
          if (diffError == null && diffLines != null)
            Text(
              widget.strings("revisions_diff_heading"),
              style: Theme.of(context).textTheme.labelLarge,
            ),
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
                                text:
                                    "${_diffMarker(line["op"] as String)}${line["text"]}\n",
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
          Text(
            widget.strings("revision_restore_explanation"),
            style: Theme.of(context).textTheme.bodySmall,
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
