package app.beresta.notes

import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.WindowManager
import androidx.activity.result.contract.ActivityResultContracts
import io.flutter.embedding.android.FlutterFragmentActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import java.io.File
import java.io.FileOutputStream
import java.security.SecureRandom
import java.util.concurrent.Executors
import org.json.JSONObject

class MainActivity : FlutterFragmentActivity() {
    private val executor = Executors.newSingleThreadExecutor()
    private val mainHandler = Handler(Looper.getMainLooper())
    private val lockRunnable = Runnable { runCatching { NativeCore.awaitService().lock() } }
    private var pendingCapture: PendingCapture? = null
    private var pendingBackupDestination: MethodChannel.Result? = null
    @Volatile private var lifecycleGeneration = 0L
    @Volatile private var nativeCoreInitStarted = false

    private val backupTreePicker = registerForActivityResult(ActivityResultContracts.OpenDocumentTree()) { uri ->
        val pending = pendingBackupDestination.also { pendingBackupDestination = null } ?: return@registerForActivityResult
        if (uri == null) pending.success(false)
        else runCatching { BackupDestination.remember(this, uri) }.fold(
            onSuccess = { pending.success(true) },
            onFailure = { error -> pending.error("backup_destination_failed", error.message, null) },
        )
    }

    private val imagePicker = registerForActivityResult(ActivityResultContracts.GetContent()) { uri ->
        val pending = pendingCapture.also { pendingCapture = null } ?: return@registerForActivityResult
        if (uri == null) {
            pending.result.success(null)
            return@registerForActivityResult
        }
        executor.execute {
            runCatching {
                val bytes = contentResolver.openInputStream(uri)?.use { input ->
                    readBounded(input, MAX_CAPTURE_BYTES)
                } ?: error("cannot open selected image")
                require(bytes.size <= MAX_CAPTURE_BYTES) { "selected image is too large" }
                val name = uri.lastPathSegment?.takeLast(128) ?: "photo"
                NativeCore.awaitService().addAttachmentData(
                    pending.requestId,
                    pending.noteId,
                    name,
                    contentResolver.getType(uri) ?: "image/jpeg",
                    bytes,
                )
                bytes.fill(0)
            }.fold(
                onSuccess = { mainHandler.post { pending.result.success(null) } },
                onFailure = { error -> mainHandler.post { pending.result.error("capture_failed", error.message, null) } },
            )
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            setRecentsScreenshotEnabled(false)
        }
        SyncWorker.schedule(this)
        BackupWorker.schedule(this)
        importShareIntent(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        importShareIntent(intent)
    }

    // BiometricPrompt requires the window to actually have focus before
    // authenticate() is called; triggering it any earlier (e.g. from
    // onCreate/onResume) lets the framework fail the attempt immediately with
    // a generic authentication error instead of showing the prompt.
    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (hasFocus && !nativeCoreInitStarted) {
            nativeCoreInitStarted = true
            initializeNativeCore()
        }
    }

    override fun onStart() {
        super.onStart()
        lifecycleGeneration += 1
        mainHandler.removeCallbacks(lockRunnable)
    }

    override fun onStop() {
        val stoppedGeneration = lifecycleGeneration + 1
        lifecycleGeneration = stoppedGeneration
        executor.execute {
            val delay = runCatching {
                val requestId = "android-lock-policy-${System.nanoTime()}"
                val settings = JSONObject(NativeCore.awaitService().getSettings(requestId))
                settings.optLong("auto_lock_minutes", DEFAULT_AUTO_LOCK_MINUTES)
                    .coerceIn(0L, MAX_AUTO_LOCK_MINUTES) * 60_000L
            }.getOrDefault(DEFAULT_AUTO_LOCK_MINUTES * 60_000L)
            mainHandler.post {
                if (lifecycleGeneration != stoppedGeneration) return@post
                if (delay == 0L) lockRunnable.run()
                else mainHandler.postDelayed(lockRunnable, delay)
            }
        }
        super.onStop()
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        val adapter = AndroidKeyWrappingAdapter(this)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, KEYSTORE_CHANNEL).setMethodCallHandler { call, result ->
            handleKeystoreCall(adapter, call, result)
        }
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, CORE_CHANNEL).setMethodCallHandler { call, result ->
            handleCoreCall(call, result)
        }
    }

    private fun initializeNativeCore() {
        val adapter = AndroidKeyWrappingAdapter(this)
        val metadata = AndroidKeyWrappingAdapter.Metadata("mobile-core", "device-secret")
        val envelopeFile = File(filesDir, "mobile-core.bkw1")
        val temporaryEnvelope = File(filesDir, "mobile-core.bkw1.tmp")
        if (!envelopeFile.exists() && temporaryEnvelope.exists()) {
            if (!temporaryEnvelope.renameTo(envelopeFile)) {
                NativeCore.failInitialization("cannot recover pending device-secret envelope")
                return
            }
        }
        val newProtection = if (adapter.biometricAvailable()) AndroidKeyWrappingAdapter.Protection.BIOMETRIC else AndroidKeyWrappingAdapter.Protection.KEYSTORE
        val callback = object : AndroidKeyWrappingAdapter.Callback {
            override fun success(value: ByteArray) {
                if (envelopeFile.exists()) {
                    NativeCore.initialize(value)
                    return
                }
                adapter.wrap(
                    newProtection,
                    metadata,
                    value,
                    getString(R.string.biometric_unlock_title),
                    getString(R.string.biometric_cancel),
                    object : AndroidKeyWrappingAdapter.Callback {
                        override fun success(envelope: ByteArray) {
                            try {
                                FileOutputStream(temporaryEnvelope).use { output ->
                                    output.write(envelope)
                                    output.fd.sync()
                                }
                                check(temporaryEnvelope.renameTo(envelopeFile)) {
                                    "cannot publish device-secret envelope"
                                }
                            } catch (failure: Exception) {
                                value.fill(0)
                                NativeCore.failInitialization("wrap: publish envelope failed: ${failure.message}")
                                return
                            }
                            NativeCore.initialize(value)
                        }

                        override fun error(code: String, detail: String?) {
                            value.fill(0)
                            NativeCore.failInitialization("wrap: $code" + (detail?.let { " ($it)" } ?: ""))
                        }
                    },
                )
            }

            override fun error(code: String, detail: String?) {
                NativeCore.failInitialization("unwrap: $code" + (detail?.let { " ($it)" } ?: ""))
            }
        }
        if (envelopeFile.exists()) {
            val envelope = envelopeFile.readBytes()
            val protection = envelope.getOrNull(5)?.let(AndroidKeyWrappingAdapter.Protection::fromCode)
            if (protection == null) {
                NativeCore.failInitialization("corrupt device-secret envelope")
                return
            }
            adapter.unwrap(
                protection,
                metadata,
                envelope,
                getString(R.string.biometric_unlock_title),
                getString(R.string.biometric_cancel),
                callback,
            )
        } else {
            callback.success(ByteArray(32).also(SecureRandom()::nextBytes))
        }
    }

    private fun handleCoreCall(call: MethodCall, result: MethodChannel.Result) {
        if (call.method == "capturePhoto") {
            val requestId = call.argument<String>("requestId")
            val noteId = call.argument<String>("noteId")
            if (requestId == null || noteId == null || pendingCapture != null) {
                result.error("invalid_request", null, null)
                return
            }
            pendingCapture = PendingCapture(requestId, noteId, result)
            imagePicker.launch("image/*")
            return
        }
        if (call.method == "selectBackupDestination") {
            if (pendingBackupDestination != null) result.error("busy", null, null)
            else {
                pendingBackupDestination = result
                backupTreePicker.launch(BackupDestination.selected(this))
            }
            return
        }
        executor.execute {
            runCatching { invokeCore(call) }.fold(
                // Void-returning core calls (lock, saveNote, ...) surface here
                // as kotlin.Unit, which StandardMessageCodec cannot encode.
                onSuccess = { value ->
                    mainHandler.post { result.success(value.takeUnless { it is Unit }) }
                },
                onFailure = { error -> mainHandler.post { result.error("core_error", error.message, null) } },
            )
        }
    }

    private fun invokeCore(call: MethodCall): Any? {
        val service = NativeCore.awaitService()
        val requestId = call.argument<String>("requestId") ?: "android-${System.nanoTime()}"
        return when (call.method) {
            "status" -> annotateStatus(service.status())
            "createAccount" -> service.createAccount(requestId, NativeCore.accountPath(filesDir), required(call, "passphrase")).also {
                ShareHandoff.drain(this)
                BackupWorker.requestImmediate(this)
            }
            "unlockAccount" -> service.unlockAccount(requestId, NativeCore.accountPath(filesDir), required(call, "passphrase")).also {
                ShareHandoff.drain(this)
                BackupWorker.requestImmediate(this)
            }
            "lock" -> service.lock()
            "listNotes" -> service.listNotes(requestId)
            "createNote" -> service.createNote(requestId, call.argument<String>("notebookId") ?: "", required(call, "title"))
            "getNote" -> service.getNote(requestId, required(call, "noteId"))
            "saveNote" -> service.saveNote(requestId, required(call, "noteId"), required(call, "title"), required(call, "body"))
            "deleteNote" -> service.deleteNote(requestId, required(call, "noteId"), call.argument<Boolean>("deleted") == true)
            "moveNote" -> service.moveNote(requestId, required(call, "noteId"), call.argument<String>("notebookId") ?: "")
            "search" -> service.search(requestId, required(call, "query"), 100L)
            "createNotebook" -> service.createNotebook(requestId, call.argument<String>("parentId") ?: "", required(call, "name"))
            "listNotebooks" -> service.listNotebooks(requestId)
            "deleteNotebook" -> service.deleteNotebook(requestId, required(call, "notebookId"), call.argument<Boolean>("deleted") == true)
            "listNoteAttachments" -> service.listNoteAttachments(requestId, required(call, "noteId"))
            "readAttachmentData" -> service.readAttachmentData(requestId, required(call, "blobId"))
            "removeAttachmentData" -> service.removeAttachmentData(requestId, required(call, "noteId"), required(call, "blobId"))
            "listTags" -> service.listTags(requestId)
            "createTag" -> service.createTag(requestId, required(call, "name"))
            "deleteTag" -> service.deleteTag(requestId, required(call, "tagId"), call.argument<Boolean>("deleted") == true)
            "setNoteTag" -> service.setNoteTag(requestId, required(call, "noteId"), required(call, "tagId"), call.argument<Boolean>("present") == true)
            "listNoteTags" -> service.listNoteTags(requestId, required(call, "noteId"))
            "listRevisions" -> service.listRevisions(requestId, required(call, "noteId"))
            "restoreRevision" -> service.restoreRevision(requestId, required(call, "noteId"), required(call, "revisionId"))
            "syncNow" -> service.syncNow()
            "connectServer" -> service.connectServer(requestId, required(call, "encoded"))
            "disconnectServer" -> service.disconnectServer()
            "syncStatus" -> service.syncStatus()
            "syncError" -> service.syncError()
            "syncConnectionInfo" -> service.syncConnectionInfo(requestId)
            "exportIdentity" -> service.exportIdentity(requestId)
            "shareWorkspace" -> service.shareWorkspace(requestId, required(call, "identityCode"))
            "acceptWorkspaceGrant" -> service.acceptWorkspaceGrant(requestId, required(call, "grantCode"))
            "listWorkspaces" -> service.listWorkspaces(requestId)
            "setActiveWorkspace" -> service.setActiveWorkspace(requestId, required(call, "workspaceId"))
            "pollEvents" -> service.pollEvents((call.argument<Number>("afterSequence")?.toLong() ?: 0L), 64L)
            "createBackup" -> BackupDestination.createManual(this, service, requestId)
            "listBackups" -> service.listBackups(requestId)
            "previewBackup" -> service.previewBackup(requestId, required(call, "backupId"))
            "restoreBackup" -> BackupDestination.restore(this, service, requestId, required(call, "backupId"))
            "importBackups" -> BackupDestination.importSelected(this, service, requestId)
            "getSettings" -> service.getSettings(requestId)
            "updateSettings" -> service.updateSettings(requestId, required(call, "encoded"))
            else -> error("unsupported core method: ${call.method}")
        }
    }

    private fun required(call: MethodCall, name: String): String =
        requireNotNull(call.argument<String>(name)) { "missing $name" }

    // The Go core only knows about the account it holds in memory, so a
    // locked status can't tell a fresh install apart from a device that
    // already has an account and just needs its passphrase. Flag on-disk
    // presence here so the onboarding screen can offer "Unlock" instead of
    // "Create" to a returning user.
    private fun annotateStatus(rawStatus: String): String {
        val status = JSONObject(rawStatus)
        if (status.optBoolean("unlocked", false)) return rawStatus
        status.put("account_exists", File(NativeCore.accountPath(filesDir)).exists())
        return status.toString()
    }

    private fun readBounded(input: java.io.InputStream, maximum: Int): ByteArray {
        val output = java.io.ByteArrayOutputStream()
        val buffer = ByteArray(64 * 1024)
        var total = 0
        while (true) {
            val count = input.read(buffer)
            if (count < 0) break
            total += count
            require(total <= maximum) { "selected image is too large" }
            output.write(buffer, 0, count)
        }
        return output.toByteArray()
    }

    private fun importShareIntent(intent: Intent) {
        if (intent.action != Intent.ACTION_SEND) return
        ShareHandoff.capture(this, intent)
        intent.action = null
    }

    private fun handleKeystoreCall(
        adapter: AndroidKeyWrappingAdapter,
        call: MethodCall,
        result: MethodChannel.Result,
    ) {
        if (call.method == "biometricAvailable") {
            result.success(adapter.biometricAvailable())
            return
        }
        val protection = AndroidKeyWrappingAdapter.Protection.fromWireName(call.argument<String>("protection"))
        val keyId = call.argument<String>("keyId")
        val purpose = call.argument<String>("purpose")
        if (protection == null || keyId == null || purpose == null) {
            result.error(AndroidKeyWrappingAdapter.ERROR_INVALID_ARGUMENT, null, null)
            return
        }
        val metadata = AndroidKeyWrappingAdapter.Metadata(keyId, purpose)
        when (call.method) {
            "wrap" -> {
                val plaintext = call.argument<ByteArray>("plaintext")
                if (plaintext == null) result.error(AndroidKeyWrappingAdapter.ERROR_INVALID_ARGUMENT, null, null)
                else adapter.wrap(protection, metadata, plaintext, call.argument("prompt"), call.argument("cancelLabel"), methodChannelCallback(result))
            }
            "unwrap" -> {
                val envelope = call.argument<ByteArray>("envelope")
                if (envelope == null) result.error(AndroidKeyWrappingAdapter.ERROR_INVALID_ARGUMENT, null, null)
                else adapter.unwrap(protection, metadata, envelope, call.argument("prompt"), call.argument("cancelLabel"), methodChannelCallback(result))
            }
            "delete" -> result.success(adapter.delete(protection, metadata))
            else -> result.notImplemented()
        }
    }

    private fun methodChannelCallback(result: MethodChannel.Result) =
        object : AndroidKeyWrappingAdapter.Callback {
            override fun success(value: ByteArray) = result.success(value)
            override fun error(code: String, detail: String?) = result.error(code, detail, null)
        }

    private data class PendingCapture(
        val requestId: String,
        val noteId: String,
        val result: MethodChannel.Result,
    )

    companion object {
        private const val KEYSTORE_CHANNEL = "app.beresta.notes/keystore/v1"
        private const val CORE_CHANNEL = "app.beresta.notes/core/v1"
        private const val DEFAULT_AUTO_LOCK_MINUTES = 5L
        private const val MAX_AUTO_LOCK_MINUTES = 24L * 60L
        private const val MAX_CAPTURE_BYTES = 64 * 1024 * 1024
    }
}
