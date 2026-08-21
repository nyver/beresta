package app.beresta.notes

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.Context
import android.content.Intent
import android.widget.RemoteViews

class QuickNoteWidget : AppWidgetProvider() {
    override fun onUpdate(context: Context, manager: AppWidgetManager, ids: IntArray) {
        ids.forEach { id ->
            val intent = Intent(context, QuickNoteActivity::class.java)
            val pending = PendingIntent.getActivity(
                context,
                id,
                intent,
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
            )
            val views = RemoteViews(context.packageName, R.layout.quick_note_widget).apply {
                setOnClickPendingIntent(R.id.quick_note_button, pending)
                // The widget deliberately never renders note titles or content.
                setTextViewText(R.id.quick_note_status, context.getString(R.string.widget_private_status))
            }
            manager.updateAppWidget(id, views)
        }
    }
}
