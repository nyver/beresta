package app.beresta.notes

import android.content.Context
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.Worker
import androidx.work.WorkerParameters
import java.time.Duration
import java.util.concurrent.TimeUnit

class BackupWorker(context: Context, parameters: WorkerParameters) : Worker(context, parameters) {
    override fun doWork(): Result {
        if (isStopped) return Result.retry()
        return runCatching {
            BackupDestination.ensureDaily(applicationContext, NativeCore.awaitService(), "daily-backup-$id")
            Result.success()
        }.getOrElse {
            if (runAttemptCount >= MAX_ATTEMPTS) Result.failure() else Result.retry()
        }
    }

    companion object {
        private const val PERIODIC_NAME = "beresta-daily-backup"
        private const val IMMEDIATE_NAME = "beresta-catch-up-backup"
        private const val MAX_ATTEMPTS = 5
        private val constraints = Constraints.Builder().setRequiresBatteryNotLow(true).build()

        fun schedule(context: Context) {
            val request = PeriodicWorkRequestBuilder<BackupWorker>(24, TimeUnit.HOURS)
                .setConstraints(constraints)
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, Duration.ofMinutes(15))
                .build()
            androidx.work.WorkManager.getInstance(context).enqueueUniquePeriodicWork(
                PERIODIC_NAME,
                ExistingPeriodicWorkPolicy.KEEP,
                request,
            )
        }

        fun requestImmediate(context: Context) {
            val request = OneTimeWorkRequestBuilder<BackupWorker>()
                .setConstraints(constraints)
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, Duration.ofSeconds(30))
                .build()
            androidx.work.WorkManager.getInstance(context).enqueueUniqueWork(
                IMMEDIATE_NAME,
                ExistingWorkPolicy.KEEP,
                request,
            )
        }
    }
}
