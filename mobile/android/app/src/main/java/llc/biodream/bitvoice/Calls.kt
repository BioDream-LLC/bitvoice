package llc.biodream.bitvoice

import android.app.Notification
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Person
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.IBinder
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
        if (d["kind"] == "chat") {
            // A chat message. When the app is in the background the system shows the notification itself;
            // in the foreground it does not, so post one. The title and body come with the message.
            val n = m.notification
            BitVoiceApp.chatNotice(this, d["from"] ?: "", n?.title ?: (d["from"] ?: "Message"), n?.body ?: "New message")
            return
        }
        if (d["kind"] != "call") return
        val id = d["callId"] ?: return
        Calls.pending.add(id)
        // Android shows the call (CallService, then the ringing notification, whose full-screen intent
        // opens the app over the lock screen). An app in the background may not start its own screen, so
        // the phone page connects when the app opens: on the full-screen intent, or on Answer.
        BitVoiceApp.incoming(this, id, d["from"] ?: "", d["fromName"] ?: "")
    }
}

/** The calls Android knows about, by BitVoice's call ID or the phone page's own ID once matched. */
object Calls {
    val pending = mutableSetOf<String>()
    val connections = mutableMapOf<String, BitVoiceConnection>()
    /** The phone page's calls that are connected, outgoing ones too: while any is, the call service runs. */
    val active = mutableSetOf<String>()
    /** Outgoing calls already handed to Android's call system. */
    val placed = mutableSetOf<String>()
    var run: ((String) -> Unit)? = null
    val main = Handler(Looper.getMainLooper())

    fun js(id: String) = org.json.JSONObject.quote(id)

    /** A message from the phone page (see web/src/native.ts). */
    fun fromPage(ctx: Context, text: String) {
        val m = runCatching { org.json.JSONObject(text) }.getOrNull() ?: return
        if (m.optString("type") == "videoroom") {
            // A video room joined or left in the web view: keep the app and its audio alive while in it,
            // with an ongoing-call notification, as the iPhone shows a video call on its call screen.
            val room = m.optString("room").ifEmpty { "video" }
            if (m.optString("state") == "joined") { active.add("video:$room"); CallForeground.start(ctx, "Video room $room", "video:$room") }
            else if (active.remove("video:$room") && active.isEmpty()) CallForeground.stop(ctx)
            return
        }
        if (m.optString("type") == "chat") {
            // A colleague's chat message while the app is in the background (the page saw it on its stream).
            BitVoiceApp.chatNotice(ctx, m.optString("from"), m.optString("name").ifEmpty { m.optString("from") }, m.optString("text"))
            return
        }
        if (m.optString("type") == "dial") {
            // A video room asks the phone to call its sound: the phone page (Calls.run) places the call.
            run?.invoke("window.bitvoice && window.bitvoice.dial(${js(m.optString("number"))})")
            return
        }
        if (m.optString("type") != "call") return
        val id = m.optString("id")
        val pbx = m.optString("pbxCall")
        when (m.optString("state")) {
            // "calling" can pass too quickly for the page to report it: "ringing" starts the call too.
            "calling", "ringing" -> if (m.optString("direction") == "out") place(ctx, id, m)
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
            "connected" -> {
                // A call answered at once (a conference room, voicemail) never rang: place it now, as active.
                if (m.optString("direction") == "out") place(ctx, id, m) // active by the time Android asks (below)
                // Answered (on the page or the notification): the ringing notification goes, and the call
                // service keeps the microphone and the app running while the phone is used for other things.
                // Held or resumed on the page: Android's call state follows (a car or a watch shows it).
                val c = connections[id]
                if (m.optBoolean("held")) c?.setOnHold() else c?.setActive()
                ctx.getSystemService(NotificationManager::class.java).cancel(NOTIFY)
                if (active.add(id)) CallForeground.start(ctx, m.optString("name").ifEmpty { m.optString("number") }, id)
            }
            "ended" -> {
                connections.remove(id)?.let {
                    it.setDisconnected(DisconnectCause(DisconnectCause.REMOTE))
                    it.destroy()
                }
                ctx.getSystemService(NotificationManager::class.java).cancel(NOTIFY)
                placed.remove(id)
                if (active.remove(id) && active.isEmpty()) CallForeground.stop(ctx)
            }
        }
    }

    /** Puts a call made on the page on Android's call system too, as the iPhone's goes on CallKit:
     * Bluetooth, a car, a watch and a mobile call arriving meanwhile all know of it. */
    private fun place(ctx: Context, id: String, m: org.json.JSONObject) {
        if (connections[id] != null || !placed.add(id)) return
        val tm = ctx.getSystemService(android.telecom.TelecomManager::class.java)
        val extras = Bundle().apply {
            putParcelable(android.telecom.TelecomManager.EXTRA_PHONE_ACCOUNT_HANDLE, BitVoiceApp.account(ctx))
            putBundle(android.telecom.TelecomManager.EXTRA_OUTGOING_CALL_EXTRAS, Bundle().apply {
                putString("callId", id); putString("name", m.optString("name"))
            })
        }
        runCatching { tm.placeCall(android.net.Uri.fromParts("tel", m.optString("number"), null), extras) }
    }

    /** Answer from the notification's button, which opens the app (Android 12 and later let a notification
     * open an activity, not start one from a broadcast). */
    fun answer(callId: String) {
        // Only a call still ringing: Android hands an activity its starting intent again when it is
        // recreated, and a second answer for a call already answered fails on the page.
        connections.values.firstOrNull { it.callId == callId && it.state == android.telecom.Connection.STATE_RINGING }?.onAnswer()
    }

    /** Ends a call from the ongoing call's Hang up button. */
    fun hangup(pageId: String) {
        run?.invoke("window.bitvoice && window.bitvoice.hangup(${js(pageId)})")
    }

    const val NOTIFY = 1
    const val ONGOING = 2
}

/** One BitVoice call on Android's call system. */
class BitVoiceConnection(private val ctx: Context, val callId: String, private val from: String, private val name: String) : Connection() {
    var pageId: String? = null
    var answeredEarly = false

    init {
        connectionProperties = PROPERTY_SELF_MANAGED
        connectionCapabilities = CAPABILITY_HOLD or CAPABILITY_SUPPORT_HOLD or CAPABILITY_MUTE
        audioModeIsVoip = true
        setAddress(android.net.Uri.fromParts("tel", from, null), android.telecom.TelecomManager.PRESENTATION_ALLOWED)
        setCallerDisplayName(name.ifEmpty { from }, android.telecom.TelecomManager.PRESENTATION_ALLOWED)
    }

    override fun onHold() {
        // Hold from the call screen, passed to the phone page (which re-INVITEs BitVoice).
        pageId?.let { Calls.run?.invoke("window.bitvoice && window.bitvoice.hold(true)") }
        setOnHold()
    }

    override fun onUnhold() {
        pageId?.let { Calls.run?.invoke("window.bitvoice && window.bitvoice.hold(false)") }
        setActive()
    }

    override fun onShowIncomingCallUi() {
        fun open(code: Int, answer: Boolean) = PendingIntent.getActivity(ctx, code,
            Intent(ctx, MainActivity::class.java).putExtra("wake", true).apply { if (answer) putExtra("answer", callId) },
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val decline = PendingIntent.getBroadcast(ctx, 3,
            Intent(ctx, CallActions::class.java).setAction("decline").putExtra("callId", callId), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val caller = Person.Builder().setName(name.ifEmpty { from.ifEmpty { "BitVoice call" } }).setImportant(true).build()
        val b = Notification.Builder(ctx, BitVoiceApp.CHANNEL)
            .setSmallIcon(R.drawable.ic_phone).setContentText("BitVoice call")
            .setCategory(Notification.CATEGORY_CALL).setOngoing(true)
            .setFullScreenIntent(open(1, false), true)
        if (Build.VERSION.SDK_INT >= 31) {
            b.setStyle(Notification.CallStyle.forIncomingCall(caller, decline, open(2, true)))
        } else {
            // Android 10 and 11 have no call style: plain buttons.
            b.setContentTitle(name.ifEmpty { from })
                .addAction(Notification.Action.Builder(null, "Decline", decline).build())
                .addAction(Notification.Action.Builder(null, "Answer", open(2, true)).build())
        }
        val n = b.build()
        ctx.getSystemService(NotificationManager::class.java).notify(Calls.NOTIFY, n)
    }

    override fun onAnswer() {
        val id = pageId
        if (id == null) answeredEarly = true else Calls.run?.invoke("window.bitvoice && window.bitvoice.answer(${Calls.js(id)})")
        setActive()
        ctx.getSystemService(NotificationManager::class.java).cancel(Calls.NOTIFY)
        // Answered from a headset or a watch: bring the phone page up, if Android lets it.
        runCatching { ctx.startActivity(Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
    }

    override fun onReject() = end(DisconnectCause.REJECTED)
    override fun onDisconnect() = end(DisconnectCause.LOCAL)

    fun end(cause: Int) {
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
                getSystemService(NotificationManager::class.java).cancel(Calls.NOTIFY)
            }
        }, 35_000)
        return c
    }

    /** A call made on the phone page (Calls.fromPage placed it): BitVoice is already dialling it. */
    override fun onCreateOutgoingConnection(account: PhoneAccountHandle?, request: ConnectionRequest): Connection {
        val ex = request.extras.getBundle(android.telecom.TelecomManager.EXTRA_OUTGOING_CALL_EXTRAS) ?: request.extras
        val id = ex.getString("callId") ?: ""
        val c = BitVoiceConnection(this, id, request.address?.schemeSpecificPart ?: "", ex.getString("name") ?: "")
        c.pageId = id
        Calls.connections[id] = c
        // Answered already, before Android got round to asking (placeCall is not immediate)?
        if (id in Calls.active) c.setActive() else c.setDialing()
        return c
    }

    /** Android refused the call (another app's call it cannot hold, say): the page's call goes on without it. */
    override fun onCreateOutgoingConnectionFailed(account: PhoneAccountHandle?, request: ConnectionRequest) {}
}

/** The Decline button on the ringing notification, and Hang up on the ongoing call's. */
class CallActions : BroadcastReceiver() {
    override fun onReceive(ctx: Context, i: Intent) {
        when (i.action) {
            "decline" -> {
                val id = i.getStringExtra("callId") ?: return
                Calls.connections.values.firstOrNull { it.callId == id }?.onReject()
            }
            "hangup" -> Calls.hangup(i.getStringExtra("pageId") ?: return)
        }
    }
}

/**
 * Runs while a call is connected: a foreground service of the phone-call and microphone kinds, so the
 * call keeps its sound when the phone is used for something else or the screen goes off. Its ongoing
 * notification shows who the call is with and can hang up.
 */
class CallForeground : Service() {
    override fun onBind(i: Intent?): IBinder? = null

    override fun onStartCommand(i: Intent?, flags: Int, startId: Int): Int {
        val who = i?.getStringExtra("who").orEmpty().ifEmpty { "BitVoice call" }
        val pageId = i?.getStringExtra("pageId").orEmpty()
        val open = PendingIntent.getActivity(this, 4, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val hangup = PendingIntent.getBroadcast(this, 5, Intent(this, CallActions::class.java).setAction("hangup").putExtra("pageId", pageId),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val b = Notification.Builder(this, BitVoiceApp.ONGOING_CHANNEL)
            .setSmallIcon(R.drawable.ic_phone).setContentIntent(open).setOngoing(true)
            .setCategory(Notification.CATEGORY_CALL)
        if (Build.VERSION.SDK_INT >= 31) {
            b.setStyle(Notification.CallStyle.forOngoingCall(Person.Builder().setName(who).build(), hangup))
        } else {
            b.setContentTitle(who).setContentText("BitVoice call").addAction(Notification.Action.Builder(null, "Hang up", hangup).build())
        }
        val n = b.build()
        var types = ServiceInfo.FOREGROUND_SERVICE_TYPE_PHONE_CALL
        if (Build.VERSION.SDK_INT >= 30) types = types or ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE
        runCatching { startForeground(Calls.ONGOING, n, types) }.onFailure { stopSelf() }
        return START_NOT_STICKY
    }

    companion object {
        fun start(ctx: Context, who: String, pageId: String) {
            val i = Intent(ctx, CallForeground::class.java).putExtra("who", who).putExtra("pageId", pageId)
            runCatching { ctx.startForegroundService(i) }
        }

        fun stop(ctx: Context) {
            ctx.stopService(Intent(ctx, CallForeground::class.java))
        }
    }
}
