package app.beresta.notes

import java.io.File
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import mobileapi.Mobileapi
import mobileapi.Service

internal object NativeCore {
    private val ready = CountDownLatch(1)

    @Volatile
    private var service: Service? = null

    fun initialize(deviceSecret: ByteArray) {
        check(service == null) { "native core is already initialized" }
        service = Mobileapi.newService(deviceSecret)
        deviceSecret.fill(0)
        ready.countDown()
    }

    fun awaitService(): Service {
        check(ready.await(30, TimeUnit.SECONDS)) { "native core initialization timed out" }
        return checkNotNull(service) { "native core initialization failed" }
    }

    fun accountPath(filesDirectory: File): String {
        val directory = File(filesDirectory, "accounts")
        check(directory.exists() || directory.mkdirs()) { "cannot create account directory" }
        return File(directory, "primary.db").absolutePath
    }

    fun close() {
        service?.close()
        service = null
    }

    fun runSqlCipherProbe(path: String, key: ByteArray, value: String): String =
        Mobileapi.runSQLCipherProbe(path, key, value)
}
