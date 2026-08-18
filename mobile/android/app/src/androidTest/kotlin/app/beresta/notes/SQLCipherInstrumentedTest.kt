package app.beresta.notes

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class SQLCipherInstrumentedTest {
    @Test
    fun encryptedDatabaseRoundTrip() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val database = File(context.cacheDir, "sqlcipher-instrumented.db")
        database.delete()
        File(database.path + "-shm").delete()
        File(database.path + "-wal").delete()

        val version = NativeCore.runSqlCipherProbe(
            database.absolutePath,
            ByteArray(32) { 0x3c },
            "android-arm64-sqlcipher-round-trip-marker",
        )

        assertTrue("SQLCipher version must not be empty", version.isNotEmpty())
    }
}
