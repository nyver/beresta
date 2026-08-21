package app.beresta.notes

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.text.InputType
import android.view.WindowManager
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.Toast

class QuickNoteActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        val editor = EditText(this).apply {
            hint = getString(R.string.quick_note_hint)
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE
            minLines = 5
        }
        val save = Button(this).apply {
            text = getString(R.string.quick_note_save)
            setOnClickListener {
                val text = editor.text.toString().trim()
                if (text.isEmpty()) return@setOnClickListener
                val capture = Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text)
                runCatching { ShareHandoff.capture(this@QuickNoteActivity, capture) }.fold(
                    onSuccess = {
                        editor.text.clear()
                        Toast.makeText(this@QuickNoteActivity, R.string.quick_note_saved, Toast.LENGTH_SHORT).show()
                        finish()
                    },
                    onFailure = { Toast.makeText(this@QuickNoteActivity, R.string.quick_note_error, Toast.LENGTH_LONG).show() },
                )
            }
        }
        setContentView(LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            val padding = (24 * resources.displayMetrics.density).toInt()
            setPadding(padding, padding, padding, padding)
            addView(editor, LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f))
            addView(save)
        })
    }
}
