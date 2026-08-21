package app.beresta.notes

import android.content.Intent
import android.view.WindowManager
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.work.Configuration
import androidx.work.WorkManager
import androidx.work.testing.SynchronousExecutor
import androidx.work.testing.WorkManagerTestInitHelper
import java.io.File
import java.util.concurrent.TimeUnit
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class MobilePrivacyInstrumentedTest {
    @Test
    fun activityPreventsScreenshotsAndRecentTaskCapture() {
        ActivityScenario.launch(MainActivity::class.java).use { scenario ->
            scenario.onActivity { activity ->
                assertTrue(activity.window.attributes.flags and WindowManager.LayoutParams.FLAG_SECURE != 0)
            }
        }
    }

    @Test
    fun shareHandoffNeverPersistsPlaintext() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val secret = "share-handoff-plaintext-fixture"
        ShareHandoff.capture(context, Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, secret))
        val directory = File(context.noBackupFilesDir, "share-handoff")
        val needle = secret.toByteArray()
        assertTrue(directory.listFiles()?.isNotEmpty() == true)
        directory.listFiles()?.forEach { file ->
            assertFalse(file.readBytes().containsSubsequence(needle))
            file.delete()
        }
    }

    @Test
    fun backgroundSyncUsesDurableConstrainedWork() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        WorkManagerTestInitHelper.initializeTestWorkManager(
            context,
            Configuration.Builder().setExecutor(SynchronousExecutor()).build(),
        )
        SyncWorker.schedule(context)
        val rows = WorkManager.getInstance(context).getWorkInfosForUniqueWork("beresta-periodic-sync").get(5, TimeUnit.SECONDS)
        assertTrue(rows.isNotEmpty())
        assertTrue(rows.first().constraints.requiresBatteryNotLow())
        assertTrue(rows.first().constraints.requiredNetworkType == androidx.work.NetworkType.CONNECTED)
    }

    private fun ByteArray.containsSubsequence(needle: ByteArray): Boolean {
        if (needle.isEmpty() || needle.size > size) return false
        return indices.take(size - needle.size + 1).any { offset -> needle.indices.all { this[offset + it] == needle[it] } }
    }
}
