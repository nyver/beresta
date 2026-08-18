package app.beresta.notes

import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyPermanentlyInvalidatedException
import android.security.keystore.KeyProperties
import android.util.Base64
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.nio.charset.StandardCharsets
import java.security.KeyStore
import java.security.MessageDigest
import javax.crypto.AEADBadTagException
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

internal class AndroidKeyWrappingAdapter(
    private val activity: FragmentActivity,
) {
    enum class Protection(val code: Byte, val wireName: String) {
        KEYSTORE(3, "android-keystore"),
        BIOMETRIC(4, "android-biometric"),
        ;

        companion object {
            fun fromWireName(value: String?): Protection? = entries.firstOrNull { it.wireName == value }

            fun fromCode(value: Byte): Protection? = entries.firstOrNull { it.code == value }
        }
    }

    data class Metadata(
        val keyId: String,
        val purpose: String,
    ) {
        fun isValid(): Boolean = validToken(keyId) && validToken(purpose)
    }

    interface Callback {
        fun success(value: ByteArray)

        fun error(code: String)
    }

    fun biometricAvailable(): Boolean =
        BiometricManager.from(activity).canAuthenticate(BiometricManager.Authenticators.BIOMETRIC_STRONG) ==
            BiometricManager.BIOMETRIC_SUCCESS

    fun wrap(
        protection: Protection,
        metadata: Metadata,
        plaintext: ByteArray,
        prompt: String?,
        cancelLabel: String?,
        callback: Callback,
    ) {
        if (!metadata.isValid() || plaintext.isEmpty() || plaintext.size > MAX_PLAINTEXT_BYTES) {
            callback.error(ERROR_INVALID_ARGUMENT)
            return
        }
        val ownedPlaintext = plaintext.copyOf()
        val wipingCallback = wipingCallback(ownedPlaintext, callback)
        try {
            val cipher = Cipher.getInstance(CIPHER_TRANSFORMATION)
            cipher.init(Cipher.ENCRYPT_MODE, getOrCreateKey(protection, metadata))
            cipher.updateAAD(binding(protection, metadata))
            execute(
                protection = protection,
                cipher = cipher,
                prompt = prompt,
                cancelLabel = cancelLabel,
                callback = wipingCallback,
            ) { authorizedCipher ->
                val ciphertext = authorizedCipher.doFinal(ownedPlaintext)
                try {
                    sealEnvelope(protection, metadata, authorizedCipher.iv, ciphertext)
                } finally {
                    ciphertext.fill(0)
                }
            }
        } catch (_: KeyPermanentlyInvalidatedException) {
            ownedPlaintext.fill(0)
            callback.error(ERROR_KEY_INVALIDATED)
        } catch (_: Exception) {
            ownedPlaintext.fill(0)
            callback.error(ERROR_UNAVAILABLE)
        }
    }

    fun unwrap(
        protection: Protection,
        metadata: Metadata,
        envelope: ByteArray,
        prompt: String?,
        cancelLabel: String?,
        callback: Callback,
    ) {
        val parsed = openEnvelope(protection, metadata, envelope)
        if (parsed == null) {
            callback.error(ERROR_INVALID_ENVELOPE)
            return
        }
        val wipingCallback = wipingCallback(parsed.ciphertext, callback)
        try {
            val key = loadKey(protection, metadata)
            if (key == null) {
                parsed.ciphertext.fill(0)
                wipingCallback.error(ERROR_KEY_INVALIDATED)
                return
            }
            val cipher = Cipher.getInstance(CIPHER_TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(GCM_TAG_BITS, parsed.nonce))
            cipher.updateAAD(binding(protection, metadata))
            execute(
                protection = protection,
                cipher = cipher,
                prompt = prompt,
                cancelLabel = cancelLabel,
                callback = wipingCallback,
            ) { authorizedCipher ->
                authorizedCipher.doFinal(parsed.ciphertext)
            }
        } catch (_: KeyPermanentlyInvalidatedException) {
            wipingCallback.error(ERROR_KEY_INVALIDATED)
        } catch (_: AEADBadTagException) {
            wipingCallback.error(ERROR_AUTHENTICATION)
        } catch (_: Exception) {
            wipingCallback.error(ERROR_AUTHENTICATION)
        }
    }

    fun delete(
        protection: Protection,
        metadata: Metadata,
    ): Boolean {
        if (!metadata.isValid()) return false
        return try {
            keyStore().deleteEntry(alias(protection, metadata))
            true
        } catch (_: Exception) {
            false
        }
    }

    private fun execute(
        protection: Protection,
        cipher: Cipher,
        prompt: String?,
        cancelLabel: String?,
        callback: Callback,
        operation: (Cipher) -> ByteArray,
    ) {
        if (protection == Protection.KEYSTORE) {
            executeCipher(cipher, callback, operation)
            return
        }
        if (!biometricAvailable() || !validPrompt(prompt) || !validPrompt(cancelLabel)) {
            callback.error(ERROR_UNAVAILABLE)
            return
        }

        val executor = ContextCompat.getMainExecutor(activity)
        val biometricPrompt =
            BiometricPrompt(
                activity,
                executor,
                object : BiometricPrompt.AuthenticationCallback() {
                    override fun onAuthenticationError(
                        errorCode: Int,
                        errString: CharSequence,
                    ) {
                        val code =
                            when (errorCode) {
                                BiometricPrompt.ERROR_CANCELED,
                                BiometricPrompt.ERROR_NEGATIVE_BUTTON,
                                BiometricPrompt.ERROR_USER_CANCELED,
                                -> ERROR_CANCELED
                                else -> ERROR_AUTHENTICATION
                            }
                        callback.error(code)
                    }

                    override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                        val authorized = result.cryptoObject?.cipher
                        if (authorized == null) {
                            callback.error(ERROR_AUTHENTICATION)
                            return
                        }
                        executeCipher(authorized, callback, operation)
                    }
                },
            )
        val promptInfo =
            BiometricPrompt.PromptInfo.Builder()
                .setTitle(requireNotNull(prompt))
                .setNegativeButtonText(requireNotNull(cancelLabel))
                .setAllowedAuthenticators(BiometricManager.Authenticators.BIOMETRIC_STRONG)
                .build()
        biometricPrompt.authenticate(promptInfo, BiometricPrompt.CryptoObject(cipher))
    }

    private fun executeCipher(
        cipher: Cipher,
        callback: Callback,
        operation: (Cipher) -> ByteArray,
    ) {
        try {
            callback.success(operation(cipher))
        } catch (_: KeyPermanentlyInvalidatedException) {
            callback.error(ERROR_KEY_INVALIDATED)
        } catch (_: Exception) {
            callback.error(ERROR_AUTHENTICATION)
        }
    }

    private fun wipingCallback(
        sensitive: ByteArray,
        delegate: Callback,
    ): Callback =
        object : Callback {
            override fun success(value: ByteArray) {
                sensitive.fill(0)
                delegate.success(value)
            }

            override fun error(code: String) {
                sensitive.fill(0)
                delegate.error(code)
            }
        }

    private fun getOrCreateKey(
        protection: Protection,
        metadata: Metadata,
    ): SecretKey {
        loadKey(protection, metadata)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEY_STORE)
        val builder =
            KeyGenParameterSpec.Builder(
                alias(protection, metadata),
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            ).setKeySize(KEY_BITS)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setRandomizedEncryptionRequired(true)
        if (protection == Protection.BIOMETRIC) {
            builder.setUserAuthenticationRequired(true)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                builder.setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG)
            } else {
                @Suppress("DEPRECATION")
                builder.setUserAuthenticationValidityDurationSeconds(-1)
            }
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
                builder.setInvalidatedByBiometricEnrollment(true)
            }
        }
        generator.init(builder.build())
        return generator.generateKey()
    }

    private fun loadKey(
        protection: Protection,
        metadata: Metadata,
    ): SecretKey? = keyStore().getKey(alias(protection, metadata), null) as? SecretKey

    private fun keyStore(): KeyStore =
        KeyStore.getInstance(ANDROID_KEY_STORE).apply {
            load(null)
        }

    private fun alias(
        protection: Protection,
        metadata: Metadata,
    ): String {
        val digest = MessageDigest.getInstance("SHA-256").digest(binding(protection, metadata))
        return ALIAS_PREFIX + Base64.encodeToString(digest, Base64.URL_SAFE or Base64.NO_WRAP or Base64.NO_PADDING)
    }

    private data class ParsedEnvelope(
        val nonce: ByteArray,
        val ciphertext: ByteArray,
    )

    companion object {
        const val ERROR_INVALID_ARGUMENT = "invalid-argument"
        const val ERROR_INVALID_ENVELOPE = "invalid-envelope"
        const val ERROR_UNAVAILABLE = "unavailable"
        const val ERROR_AUTHENTICATION = "authentication"
        const val ERROR_CANCELED = "canceled"
        const val ERROR_KEY_INVALIDATED = "key-invalidated"

        private const val ANDROID_KEY_STORE = "AndroidKeyStore"
        private const val CIPHER_TRANSFORMATION = "AES/GCM/NoPadding"
        private const val ALIAS_PREFIX = "beresta.wrap.v1."
        private const val FORMAT_VERSION: Byte = 1
        private const val HEADER_BYTES = 14
        private const val MAX_IDENTIFIER = 128
        private const val MAX_WRAPPED_BYTES = 64 shl 10
        private const val MAX_PLAINTEXT_BYTES = 4096
        private const val KEY_BITS = 256
        private const val GCM_TAG_BITS = 128
        private val MAGIC = byteArrayOf('B'.code.toByte(), 'K'.code.toByte(), 'W'.code.toByte(), '1'.code.toByte())

        private fun validToken(value: String): Boolean =
            value.isNotEmpty() &&
                value.length <= MAX_IDENTIFIER &&
                value.all { it.isLetterOrDigit() && it.code < 128 || it == '-' || it == '_' || it == '.' }

        private fun validPrompt(value: String?): Boolean = value != null && value.isNotBlank() && value.length <= 128

        private fun binding(
            protection: Protection,
            metadata: Metadata,
        ): ByteArray {
            val keyId = metadata.keyId.toByteArray(StandardCharsets.US_ASCII)
            val purpose = metadata.purpose.toByteArray(StandardCharsets.US_ASCII)
            val domain = "beresta-keystore-v1".toByteArray(StandardCharsets.US_ASCII)
            return ByteBuffer.allocate(domain.size + 1 + 2 + keyId.size + 2 + purpose.size)
                .order(ByteOrder.BIG_ENDIAN)
                .put(domain)
                .put(protection.code)
                .putShort(keyId.size.toShort())
                .put(keyId)
                .putShort(purpose.size.toShort())
                .put(purpose)
                .array()
        }

        private fun sealEnvelope(
            protection: Protection,
            metadata: Metadata,
            nonce: ByteArray,
            ciphertext: ByteArray,
        ): ByteArray {
            require(nonce.isNotEmpty() && nonce.size <= 255)
            val wrapped = ByteBuffer.allocate(1 + nonce.size + ciphertext.size)
                .put(nonce.size.toByte())
                .put(nonce)
                .put(ciphertext)
                .array()
            require(wrapped.size <= MAX_WRAPPED_BYTES)
            val keyId = metadata.keyId.toByteArray(StandardCharsets.US_ASCII)
            val purpose = metadata.purpose.toByteArray(StandardCharsets.US_ASCII)
            return ByteBuffer.allocate(HEADER_BYTES + keyId.size + purpose.size + wrapped.size)
                .order(ByteOrder.BIG_ENDIAN)
                .put(MAGIC)
                .put(FORMAT_VERSION)
                .put(protection.code)
                .putShort(keyId.size.toShort())
                .putShort(purpose.size.toShort())
                .putInt(wrapped.size)
                .put(keyId)
                .put(purpose)
                .put(wrapped)
                .array()
        }

        private fun openEnvelope(
            protection: Protection,
            metadata: Metadata,
            encoded: ByteArray,
        ): ParsedEnvelope? {
            if (!metadata.isValid() || encoded.size < HEADER_BYTES) return null
            val buffer = ByteBuffer.wrap(encoded).order(ByteOrder.BIG_ENDIAN)
            val magic = ByteArray(MAGIC.size).also { buffer.get(it) }
            if (!magic.contentEquals(MAGIC) || buffer.get() != FORMAT_VERSION ||
                Protection.fromCode(buffer.get()) != protection
            ) {
                return null
            }
            val keyLength = buffer.short.toInt() and 0xffff
            val purposeLength = buffer.short.toInt() and 0xffff
            val wrappedLength = buffer.int
            if (keyLength !in 1..MAX_IDENTIFIER || purposeLength !in 1..MAX_IDENTIFIER ||
                wrappedLength !in 1..MAX_WRAPPED_BYTES ||
                HEADER_BYTES + keyLength + purposeLength + wrappedLength != encoded.size
            ) {
                return null
            }
            val keyId = ByteArray(keyLength).also { buffer.get(it) }
            val purpose = ByteArray(purposeLength).also { buffer.get(it) }
            if (!keyId.contentEquals(metadata.keyId.toByteArray(StandardCharsets.US_ASCII)) ||
                !purpose.contentEquals(metadata.purpose.toByteArray(StandardCharsets.US_ASCII))
            ) {
                return null
            }
            val nonceLength = buffer.get().toInt() and 0xff
            if (nonceLength == 0 || nonceLength >= wrappedLength - 1) return null
            val nonce = ByteArray(nonceLength).also { buffer.get(it) }
            val ciphertext = ByteArray(buffer.remaining()).also { buffer.get(it) }
            return ParsedEnvelope(nonce, ciphertext)
        }
    }
}
