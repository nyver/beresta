package app.beresta.notes

import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class KeyWrappingInstrumentedTest {
    @Test
    fun keystoreRoundTripRejectsTamperingAndMetadataSubstitution() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            scenario.onActivity { activity ->
                val adapter = AndroidKeyWrappingAdapter(activity)
                val metadata = AndroidKeyWrappingAdapter.Metadata("instrumented.database", "database-key")
                adapter.delete(AndroidKeyWrappingAdapter.Protection.KEYSTORE, metadata)

                val plaintext = ByteArray(32) { 0x5a }
                val envelope = await { callback ->
                    adapter.wrap(
                        AndroidKeyWrappingAdapter.Protection.KEYSTORE,
                        metadata,
                        plaintext,
                        null,
                        null,
                        callback,
                    )
                }
                assertNotNull(envelope.value)
                val opened = await { callback ->
                    adapter.unwrap(
                        AndroidKeyWrappingAdapter.Protection.KEYSTORE,
                        metadata,
                        requireNotNull(envelope.value),
                        null,
                        null,
                        callback,
                    )
                }
                assertArrayEquals(plaintext, opened.value)

                val substituted = await { callback ->
                    adapter.unwrap(
                        AndroidKeyWrappingAdapter.Protection.KEYSTORE,
                        metadata.copy(keyId = "instrumented.other"),
                        requireNotNull(envelope.value),
                        null,
                        null,
                        callback,
                    )
                }
                assertEquals(AndroidKeyWrappingAdapter.ERROR_INVALID_ENVELOPE, substituted.error)

                val tampered = requireNotNull(envelope.value).copyOf()
                tampered[tampered.lastIndex] = (tampered.last().toInt() xor 0x80).toByte()
                val rejected = await { callback ->
                    adapter.unwrap(
                        AndroidKeyWrappingAdapter.Protection.KEYSTORE,
                        metadata,
                        tampered,
                        null,
                        null,
                        callback,
                    )
                }
                assertEquals(AndroidKeyWrappingAdapter.ERROR_AUTHENTICATION, rejected.error)
                adapter.delete(AndroidKeyWrappingAdapter.Protection.KEYSTORE, metadata)
            }
        }
    }

    private data class Result(
        var value: ByteArray? = null,
        var error: String? = null,
    )

    private fun await(invoke: (AndroidKeyWrappingAdapter.Callback) -> Unit): Result {
        val latch = CountDownLatch(1)
        val result = Result()
        invoke(
            object : AndroidKeyWrappingAdapter.Callback {
                override fun success(value: ByteArray) {
                    result.value = value
                    latch.countDown()
                }

                override fun error(code: String) {
                    result.error = code
                    latch.countDown()
                }
            },
        )
        check(latch.await(10, TimeUnit.SECONDS)) { "keystore callback timed out" }
        return result
    }
}
