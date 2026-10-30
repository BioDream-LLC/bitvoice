package llc.biodream.bitvoice

import android.app.Application
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.telecom.PhoneAccount
import android.telecom.PhoneAccountHandle
import android.telecom.TelecomManager
import com.google.firebase.FirebaseApp
import com.google.firebase.FirebaseOptions
import com.google.firebase.messaging.FirebaseMessaging
import org.json.JSONObject
import kotlin.concurrent.thread

/**
 * Sets up what calls need once: the notification channel for ringing, the self-managed phone account
 * that puts BitVoice calls on Android's call system, and Firebase, from assets/firebase.json (the app's
 * project ID, application ID and API key); without that file the app rings only while open.
 */
class BitVoiceApp : Application() {
    override fun onCreate() {
        super.onCreate()
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(NotificationChannel(CHANNEL, "Incoming calls", NotificationManager.IMPORTANCE_HIGH).apply {
            description = "Rings for calls to your extension"
        })
        nm.createNotificationChannel(NotificationChannel(ONGOING_CHANNEL, "Calls in progress", NotificationManager.IMPORTANCE_LOW).apply {
            description = "Shows the call you are on, with Hang up"
        })
        nm.createNotificationChannel(NotificationChannel(CHAT_CHANNEL, "Chat messages", NotificationManager.IMPORTANCE_HIGH).apply {
            description = "Messages from colleagues in the staff chat"
        })
        val tm = getSystemService(TelecomManager::class.java)
        tm.registerPhoneAccount(PhoneAccount.builder(account(this), "BitVoice")
            .setCapabilities(PhoneAccount.CAPABILITY_SELF_MANAGED).build())
        startFirebase()
    }

    private fun startFirebase() {
        val cfg = runCatching { assets.open("firebase.json").bufferedReader().use { JSONObject(it.readText()) } }.getOrNull() ?: return
        if (FirebaseApp.getApps(this).isEmpty()) {
            FirebaseApp.initializeApp(this, FirebaseOptions.Builder()
                .setProjectId(cfg.getString("projectId")).setApplicationId(cfg.getString("applicationId"))
                .setApiKey(cfg.getString("apiKey")).setGcmSenderId(cfg.optString("senderId")).build())
        }
        FirebaseMessaging.getInstance().token.addOnSuccessListener { sendToken(this, it) }
    }

    companion object {
        const val CHANNEL = "calls"
        const val ONGOING_CHANNEL = "ongoing"
        const val CHAT_CHANNEL = "chat"

        fun account(ctx: Context) = PhoneAccountHandle(ComponentName(ctx, CallService::class.java), "bitvoice")

        fun sendToken(ctx: Context, token: String) {
            val p = Vault.load(ctx) ?: return
            thread { runCatching { BitVoiceApi.sendToken(p, token) } }
        }

        /** Tells Android a call is coming in; CallService then shows it. */
        fun incoming(ctx: Context, callId: String, from: String, fromName: String) {
            val tm = ctx.getSystemService(TelecomManager::class.java)
            val extras = Bundle().apply {
                putParcelable(TelecomManager.EXTRA_INCOMING_CALL_ADDRESS, Uri.fromParts("tel", from, null))
                putBundle(TelecomManager.EXTRA_INCOMING_CALL_EXTRAS, Bundle().apply {
                    putString("callId", callId); putString("from", from); putString("fromName", fromName)
                })
            }
            runCatching { tm.addNewIncomingCall(account(ctx), extras) }
        }

        /** Shows a chat message as a notification; tapping it opens the app (its Chat tab). */
        fun chatNotice(ctx: Context, from: String, title: String, body: String) {
            val open = android.app.PendingIntent.getActivity(ctx, "chat$from".hashCode(),
                Intent(ctx, MainActivity::class.java).putExtra("chat", from),
                android.app.PendingIntent.FLAG_IMMUTABLE or android.app.PendingIntent.FLAG_UPDATE_CURRENT)
            val n = Notification.Builder(ctx, CHAT_CHANNEL)
                .setSmallIcon(R.drawable.ic_phone).setContentTitle(title).setContentText(body)
                .setCategory(Notification.CATEGORY_MESSAGE).setAutoCancel(true).setContentIntent(open).build()
            ctx.getSystemService(NotificationManager::class.java).notify("chat:$from".hashCode(), n)
        }
    }
}
