package llc.biodream.bitvoice

import android.app.Notification
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import android.telecom.Connection
import android.telecom.ConnectionRequest
import android.telecom.ConnectionService
import android.telecom.DisconnectCause
import android.telecom.PhoneAccountHandle
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage

/** A push from BitVoice: a call is coming. Report it to Android at once, then let the phone page connect. */
class PushService : FirebaseMessagingService() {
    override fun onNewToken(token: String) = BitVoiceApp.sendToken(this, token)

    override fun onMessageReceived(m: RemoteMessage) {
        val d = m.data
        if (d["kind"] != "call") return
        val id = d["callId"] ?: return
        Calls.pending.add(id)
        BitVoiceApp.incoming(this, id, d["from"] ?: "", d["fromName"] ?: "")
        // Start the phone page so it connects and BitVoice rings it.
        startActivity(Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK).putExtra("wake", true))
    }
}

/** The calls Android knows about, by BitVoice's call ID or the phone page's own ID once matched. */
object Calls {
    val pending = mutableSetOf<String>()
    val connections = mutableMapOf<String, BitVoiceConnection>()
    var run: ((String) -> Unit)? = null
    val main = Handler(Looper.getMainLooper())

    fun js(id: String) = org.json.JSONObject.quote(id)

    /** A message from the phone page (see web/src/native.ts). */
    fun fromPage(ctx: Context, text: String) {
        val m = runCatching { org.json.JSONObject(text) }.getOrNull() ?: return
        if (m.optString("type") != "call") return
        val id = m.optString("id")
        val pbx = m.optString("pbxCall")
        when (m.optString("state")) {
            "incoming" -> {
                val c = connections.remove(pbx)
                if (c != null) {
                    pending.remove(pbx)
                    c.pageId = id
                    connections[id] = c
                    if (c.answeredEarly) run?.invoke("window.bitvoice && window.bitvoice.answer(${js(id)})")
                } else if (connections[id] == null) {
                    BitVoiceApp.incoming(ctx, id, m.optString("number"), m.optString("name"))
                }
            }
            "connected" -> connections[id]?.setActive()
            "ended" -> connections.remove(id)?.let {
                it.setDisconnected(DisconnectCause(DisconnectCause.REMOTE))
                it.destroy()
                ctx.getSystemService(NotificationManager::class.java).cancel(NOTIFY)
            }
        }
    }

    const val NOTIFY = 1
}

/** One BitVoice call on Android's call system. */
class BitVoiceConnection(private val ctx: Context, val callId: String, private val from: String, private val name: String) : Connection() {
    var pageId: String? = null
    var answeredEarly = false

    init {
        connectionProperties = PROPERTY_SELF_MANAGED
        audioModeIsVoip = true
        setAddress(android.net.Uri.fromParts("tel", from, null), android.telecom.TelecomManager.PRESENTATION_ALLOWED)
        setCallerDisplayName(name.ifEmpty { from }, android.telecom.TelecomManager.PRESENTATION_ALLOWED)
    }

    override fun onShowIncomingCallUi() {
        val full = PendingIntent.getActivity(ctx, 0, Intent(ctx, MainActivity::class.java).putExtra("wake", true),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        fun action(what: String, code: Int) = PendingIntent.getBroadcast(ctx, code,
            Intent(ctx, CallActions::class.java).setAction(what).putExtra("callId", callId), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val n = Notification.Builder(ctx, BitVoiceApp.CHANNEL)
            .setSmallIcon(R.drawable.ic_phone).setContentTitle(name.ifEmpty { from }).setContentText("BitVoice call")
            .setCategory(Notification.CATEGORY_CALL).setOngoing(true).setFullScreenIntent(full, true)
            .addAction(Notification.Action.Builder(null, "Decline", action("decline", 2)).build())
            .addAction(Notification.Action.Builder(null, "Answer", action("answer", 1)).build())
            .build()
        ctx.getSystemService(NotificationManager::class.java).notify(Calls.NOTIFY, n)
    }

    override fun onAnswer() {
        val id = pageId
        if (id == null) answeredEarly = true else Calls.run?.invoke("window.bitvoice && window.bitvoice.answer(${Calls.js(id)})")
        setActive()
        ctx.getSystemService(NotificationManager::class.java).cancel(Calls.NOTIFY)
        ctx.startActivity(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }

    override fun onReject() = end(DisconnectCause.REJECTED)
    override fun onDisconnect() = end(DisconnectCause.LOCAL)

    private fun end(cause: Int) {
        pageId?.let { Calls.run?.invoke("window.bitvoice && window.bitvoice.hangup(${Calls.js(it)})") }
        Calls.connections.remove(pageId ?: callId)
        setDisconnected(DisconnectCause(cause))
        destroy()
        ctx.getSystemService(NotificationManager::class.java).cancel(Calls.NOTIFY)
    }
}

/** Android asks for the connection of a call reported by BitVoiceApp.incoming. */
class CallService : ConnectionService() {
    override fun onCreateIncomingConnection(account: PhoneAccountHandle?, request: ConnectionRequest): Connection {
        val ex = request.extras.getBundle(android.telecom.TelecomManager.EXTRA_INCOMING_CALL_EXTRAS) ?: request.extras
        val id = ex.getString("callId") ?: ""
        val c = BitVoiceConnection(this, id, ex.getString("from") ?: "", ex.getString("fromName") ?: "")
        if (id !in Calls.pending) c.pageId = id // reported by the page itself, not a push
        Calls.connections[id] = c
        c.setRinging()
        // The caller may give up before the phone page connects: end the call screen too.
        Calls.main.postDelayed({
            if (Calls.pending.remove(id) && Calls.connections.remove(id) != null) {
                c.setDisconnected(DisconnectCause(DisconnectCause.MISSED)); c.destroy()
            }
        }, 35_000)
        return c
    }
}

/** The Answer and Decline buttons on the ringing notification. */
class CallActions : BroadcastReceiver() {
    override fun onReceive(ctx: Context, i: Intent) {
        val id = i.getStringExtra("callId") ?: return
        val c = Calls.connections.values.firstOrNull { it.callId == id } ?: return
        if (i.action == "answer") c.onAnswer() else c.onReject()
    }
}
