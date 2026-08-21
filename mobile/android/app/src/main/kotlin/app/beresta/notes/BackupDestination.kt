package app.beresta.notes

import android.content.Context
import android.net.Uri
import androidx.documentfile.provider.DocumentFile
import java.io.File
import mobileapi.Service
import org.json.JSONObject

internal object BackupDestination {
    private const val PREFERENCES = "backup-destination-v1"
    private const val URI_KEY = "tree-uri"
    private const val MAX_IMPORTED_BYTES = 64L shl 30
    private const val MAX_IMPORTED_ENTRIES = 200_000
    private val safeName = Regex("^[A-Za-z0-9._-]{1,128}$")

    fun remember(context: Context, uri: Uri) {
        context.contentResolver.takePersistableUriPermission(
            uri,
            android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION or android.content.Intent.FLAG_GRANT_WRITE_URI_PERMISSION,
        )
        context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE).edit().putString(URI_KEY, uri.toString()).apply()
    }

    fun selected(context: Context): Uri? =
        context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE).getString(URI_KEY, null)?.let(Uri::parse)

    fun createManual(context: Context, service: Service, requestId: String): String {
        val localRoot = localRoot(context)
        val result = service.createBackup(requestId, localRoot.absolutePath)
        mirror(context, File(JSONObject(result).getString("location")))
        return result
    }

    fun ensureDaily(context: Context, service: Service, requestId: String): Boolean {
        val localRoot = localRoot(context)
        val created = service.ensureDailyBackup(requestId, localRoot.absolutePath)
        if (created) {
            localRoot.listFiles()?.filter { it.isDirectory }?.maxByOrNull { it.lastModified() }?.let { mirror(context, it) }
        }
        return created
    }

    fun restore(context: Context, service: Service, requestId: String, backupId: String): String =
        service.restoreWholeBackup(requestId, backupId, localRoot(context).absolutePath)

    fun importSelected(context: Context, service: Service, requestId: String): Int {
        val uri = selected(context) ?: error("no backup destination is selected")
        val sourceRoot = requireNotNull(DocumentFile.fromTreeUri(context, uri))
        val candidates = sourceRoot.listFiles()
            .filter { it.isDirectory && UUID_PATTERN.matches(it.name.orEmpty()) }
            .sortedBy { it.name }
            .take(64)
        val localRoot = localRoot(context)
        var missingBytes = 0L
        candidates.filter { !File(localRoot, it.name!!).exists() }.forEach {
            val size = measuredBytes(it)
            require(missingBytes <= MAX_IMPORTED_BYTES - size)
            missingBytes += size
        }
        require(missingBytes <= MAX_IMPORTED_BYTES && localRoot.usableSpace >= missingBytes + missingBytes / 10) {
            "insufficient free space for backup import"
        }
        var imported = 0
        val copiedBytes = longArrayOf(0)
        candidates.forEachIndexed { index, source ->
            val name = requireNotNull(source.name)
            val destination = File(localRoot, name)
            if (!destination.exists()) {
                val staging = File(localRoot, ".import-$name").also { it.deleteRecursively() }
                check(staging.mkdirs())
                try {
                    copyFromDocument(context, source, staging, intArrayOf(0), copiedBytes)
                    check(staging.renameTo(destination))
                } finally {
                    staging.deleteRecursively()
                }
            }
            runCatching {
                service.importBackupSet("$requestId-$index", destination.absolutePath, 4L)
                imported += 1
            }
        }
        return imported
    }

    private fun localRoot(context: Context): File =
        File(context.noBackupFilesDir, "encrypted-backups").also { check(it.exists() || it.mkdirs()) }

    private fun mirror(context: Context, source: File) {
        val uri = selected(context) ?: return
        val root = requireNotNull(DocumentFile.fromTreeUri(context, uri))
        val stagingName = ".staging-${source.name}"
        root.findFile(stagingName)?.delete()
        val staging = requireNotNull(root.createDirectory(stagingName))
        copyDirectory(context, source, staging)
        root.findFile(source.name)?.delete()
        check(staging.renameTo(source.name)) { "backup destination cannot publish atomically" }
    }

    private fun copyDirectory(context: Context, source: File, destination: DocumentFile) {
        source.listFiles()?.sortedBy { it.name }?.forEach { child ->
            if (child.isDirectory) {
                val directory = destination.findFile(child.name) ?: requireNotNull(destination.createDirectory(child.name))
                copyDirectory(context, child, directory)
            } else {
                destination.findFile(child.name)?.delete()
                val document = requireNotNull(destination.createFile("application/octet-stream", child.name))
                context.contentResolver.openOutputStream(document.uri, "w")!!.use { output ->
                    child.inputStream().use { input -> input.copyTo(output, 256 * 1024) }
                }
            }
        }
    }

    private fun measuredBytes(root: DocumentFile): Long {
        var entries = 0
        var total = 0L
        fun visit(node: DocumentFile, depth: Int) {
            require(depth <= 8 && ++entries <= MAX_IMPORTED_ENTRIES)
            if (node.isDirectory) node.listFiles().forEach { visit(it, depth + 1) }
            else {
                val size = node.length()
                require(size >= 0 && total <= MAX_IMPORTED_BYTES - size)
                total += size
            }
        }
        visit(root, 0)
        return total
    }

    private fun copyFromDocument(
        context: Context,
        source: DocumentFile,
        destination: File,
        entries: IntArray,
        bytes: LongArray,
        depth: Int = 0,
    ) {
        require(depth <= 8 && ++entries[0] <= MAX_IMPORTED_ENTRIES)
        if (source.isDirectory) {
            source.listFiles().sortedBy { it.name }.forEach { child ->
                val name = child.name.orEmpty()
                require(safeName.matches(name) && name != "." && name != "..")
                val target = File(destination, name)
                require(target.canonicalPath.startsWith(destination.canonicalPath + File.separator))
                if (child.isDirectory) check(target.mkdir())
                copyFromDocument(context, child, target, entries, bytes, depth + 1)
            }
            return
        }
        context.contentResolver.openInputStream(source.uri)!!.use { input ->
            destination.outputStream().use { output ->
                val buffer = ByteArray(256 * 1024)
                while (true) {
                    val count = input.read(buffer)
                    if (count < 0) break
                    require(bytes[0] <= MAX_IMPORTED_BYTES - count)
                    bytes[0] += count
                    output.write(buffer, 0, count)
                }
            }
        }
    }

    private val UUID_PATTERN = Regex("^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
}
