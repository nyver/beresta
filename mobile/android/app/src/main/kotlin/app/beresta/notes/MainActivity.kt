package app.beresta.notes

import io.flutter.embedding.android.FlutterFragmentActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterFragmentActivity() {
    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        val adapter = AndroidKeyWrappingAdapter(this)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, KEYSTORE_CHANNEL).setMethodCallHandler { call, result ->
            handleKeystoreCall(adapter, call, result)
        }
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
                if (plaintext == null) {
                    result.error(AndroidKeyWrappingAdapter.ERROR_INVALID_ARGUMENT, null, null)
                    return
                }
                adapter.wrap(
                    protection,
                    metadata,
                    plaintext,
                    call.argument("prompt"),
                    call.argument("cancelLabel"),
                    methodChannelCallback(result),
                )
            }
            "unwrap" -> {
                val envelope = call.argument<ByteArray>("envelope")
                if (envelope == null) {
                    result.error(AndroidKeyWrappingAdapter.ERROR_INVALID_ARGUMENT, null, null)
                    return
                }
                adapter.unwrap(
                    protection,
                    metadata,
                    envelope,
                    call.argument("prompt"),
                    call.argument("cancelLabel"),
                    methodChannelCallback(result),
                )
            }
            "delete" -> result.success(adapter.delete(protection, metadata))
            else -> result.notImplemented()
        }
    }

    private fun methodChannelCallback(result: MethodChannel.Result) =
        object : AndroidKeyWrappingAdapter.Callback {
            override fun success(value: ByteArray) = result.success(value)

            override fun error(code: String) = result.error(code, null, null)
        }

    companion object {
        private const val KEYSTORE_CHANNEL = "app.beresta.notes/keystore/v1"
    }
}
