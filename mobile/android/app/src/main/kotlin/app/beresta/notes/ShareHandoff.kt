package app.beresta.notes

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import java.io.ByteArrayOutputStream
import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.File
import java.security.KeyStore
import java.util.UUID
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import org.json.JSONObject

internal object ShareHandoff {
    private const val KEY_ALIAS = "beresta.share-handoff.v1"
    private const val MAX_TEXT_BYTES = 1 shl 20
    private const val MAX_IMAGE_BYTES = 64 shl 20
    private val associatedData = "beresta-share-handoff-v1".toByteArray()

    fun capture(context: Context, intent: Intent) {
        val mediaType = intent.type ?: return
        val payload = when {
            mediaType == "text/plain" -> {
                val text = intent.getStringExtra(Intent.EXTRA_TEXT)?.trim().orEmpty()
                val encoded = text.toByteArray()
                require(encoded.isNotEmpty() && encoded.size <= MAX_TEXT_BYTES)
                Capture(1, "text/plain", encoded)
            }
            mediaType.startsWith("image/") -> {
                val uri = intent.shareUri() ?: return
                val bytes = context.contentResolver.openInputStream(uri)?.use { input ->
                    readBounded(input, MAX_IMAGE_BYTES)
                } ?: return
                require(bytes.isNotEmpty() && bytes.size <= MAX_IMAGE_BYTES)
                Capture(2, mediaType.take(128), bytes)
            }
            else -> return
        }
        val encoded = payload.encode()
        val encrypted = encrypt(encoded)
        encoded.fill(0)
        payload.contents.fill(0)
        val directory = File(context.noBackupFilesDir, "share-handoff").also { it.mkdirs() }
        val destination = File(directory, "${UUID.randomUUID()}.bhf")
        val temporary = File(directory, destination.name + ".tmp")
        temporary.writeBytes(encrypted)
        check(temporary.renameTo(destination))
    }

    fun drain(context: Context) {
        val directory = File(context.noBackupFilesDir, "share-handoff")
        directory.listFiles { file -> file.extension == "bhf" }?.sortedBy { it.name }?.forEach { file ->
            runCatching {
                val plaintext = decrypt(file.readBytes())
                val capture = Capture.decode(plaintext)
                plaintext.fill(0)
                val service = NativeCore.awaitService()
                val requestBase = "share-${file.nameWithoutExtension}"
                val created = JSONObject(service.createNote("$requestBase-note", "", "Shared capture"))
                val noteId = created.getString("id")
                if (capture.kind == 1) {
                    val text = capture.contents.toString(Charsets.UTF_8)
                    service.saveNote("$requestBase-text", noteId, "Shared capture", text)
                } else {
                    service.addAttachmentData("$requestBase-image", noteId, "shared-image", capture.mediaType, capture.contents)
                }
                capture.contents.fill(0)
                check(file.delete())
            }
        }
    }

    @Suppress("DEPRECATION")
    private fun Intent.shareUri(): Uri? =
        if (Build.VERSION.SDK_INT >= 33) getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java)
        else getParcelableExtra(Intent.EXTRA_STREAM)

    private fun readBounded(input: java.io.InputStream, maximum: Int): ByteArray {
        val output = ByteArrayOutputStream()
        val buffer = ByteArray(64 * 1024)
        var total = 0
        while (true) {
            val count = input.read(buffer)
            if (count < 0) break
            total += count
            require(total <= maximum) { "shared image is too large" }
            output.write(buffer, 0, count)
        }
        return output.toByteArray()
    }

    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance("AES", "AndroidKeyStore")
        generator.init(
            android.security.keystore.KeyGenParameterSpec.Builder(
                KEY_ALIAS,
                android.security.keystore.KeyProperties.PURPOSE_ENCRYPT or android.security.keystore.KeyProperties.PURPOSE_DECRYPT,
            ).setBlockModes(android.security.keystore.KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(android.security.keystore.KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return generator.generateKey()
    }

    private fun encrypt(plaintext: ByteArray): ByteArray {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, key())
        cipher.updateAAD(associatedData)
        return cipher.iv + cipher.doFinal(plaintext)
    }

    private fun decrypt(encoded: ByteArray): ByteArray {
        require(encoded.size > 12 + 16 && encoded.size <= MAX_IMAGE_BYTES + MAX_TEXT_BYTES)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, encoded.copyOfRange(0, 12)))
        cipher.updateAAD(associatedData)
        return cipher.doFinal(encoded, 12, encoded.size - 12)
    }

    private data class Capture(val kind: Int, val mediaType: String, val contents: ByteArray) {
        fun encode(): ByteArray {
            val media = mediaType.toByteArray()
            require(kind in 1..2 && media.isNotEmpty() && media.size <= 128)
            return ByteArrayOutputStream().use { output ->
                DataOutputStream(output).use { data ->
                    data.writeInt(0x42484631)
                    data.writeByte(kind)
                    data.writeShort(media.size)
                    data.writeInt(contents.size)
                    data.write(media)
                    data.write(contents)
                }
                output.toByteArray()
            }
        }

        companion object {
            fun decode(encoded: ByteArray): Capture = DataInputStream(encoded.inputStream()).use { input ->
                require(input.readInt() == 0x42484631)
                val kind = input.readUnsignedByte()
                val mediaLength = input.readUnsignedShort()
                val contentLength = input.readInt()
                require(kind in 1..2 && mediaLength in 1..128 && contentLength in 1..MAX_IMAGE_BYTES)
                require(encoded.size == 11 + mediaLength + contentLength)
                val media = ByteArray(mediaLength).also(input::readFully).toString(Charsets.UTF_8)
                val contents = ByteArray(contentLength).also(input::readFully)
                Capture(kind, media, contents)
            }
        }
    }
}
