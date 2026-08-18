package app.beresta.notes

import mobileapi.Mobileapi

internal object NativeCore {
    fun runSqlCipherProbe(path: String, key: ByteArray, value: String): String =
        Mobileapi.runSQLCipherProbe(path, key, value)
}
