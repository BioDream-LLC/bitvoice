package llc.biodream.bitvoice

import android.content.Context
import android.net.Uri
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** What pairing gave this phone. */
data class Pairing(val server: String, val device: String, val secret: String, val extension: String, val name: String) {
    fun json(): String = JSONObject().put("server", server).put("device", device).put("secret", secret)
        .put("extension", extension).put("name", name).toString()

    companion object {
        fun from(s: String): Pairing? = runCatching {
            val o = JSONObject(s)
            Pairing(o.getString("server"), o.getString("device"), o.getString("secret"), o.getString("extension"), o.optString("name"))
        }.getOrNull()
    }
}

/**
 * Keeps the pairing encrypted with an AES key held by Android Keystore (the platform's own crypto), so
 * the device secret is never in a plain file.
 */
object Vault {
    private const val ALIAS = "bitvoice-pairing"
    private const val PREFS = "bitvoice"

    private fun key(): SecretKey {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (ks.getEntry(ALIAS, null) as? KeyStore.SecretKeyEntry)?.let { return it.secretKey }
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        gen.init(KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
        return gen.generateKey()
    }

    fun save(ctx: Context, p: Pairing) {
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.ENCRYPT_MODE, key())
        val blob = c.iv + c.doFinal(p.json().toByteArray())
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString("pairing", Base64.encodeToString(blob, Base64.NO_WRAP)).apply()
    }

    fun load(ctx: Context): Pairing? = runCatching {
        val s = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString("pairing", null) ?: return null
        val blob = Base64.decode(s, Base64.NO_WRAP)
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, blob, 0, 12))
        Pairing.from(String(c.doFinal(blob, 12, blob.size - 12)))
    }.getOrNull()

    fun clear(ctx: Context) {
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().remove("pairing").apply()
    }
}

/** The few calls the app makes to BitVoice itself; everything else happens in the phone page. */
object BitVoiceApi {
    class Failure(message: String) : Exception(message)

    private fun post(url: String, body: JSONObject): JSONObject {
        val c = URL(url).openConnection() as HttpURLConnection
        c.requestMethod = "POST"
        c.connectTimeout = 15000
        c.readTimeout = 15000
        c.doOutput = true
        c.setRequestProperty("Content-Type", "application/json")
        c.setRequestProperty("X-BitVoice", "1") // BitVoice's guard against cross-site requests
        c.outputStream.use { it.write(body.toString().toByteArray()) }
        val ok = c.responseCode == 200
        val text = (if (ok) c.inputStream else c.errorStream)?.bufferedReader()?.use { it.readText() } ?: "{}"
        val json = runCatching { JSONObject(text) }.getOrDefault(JSONObject())
        if (!ok) throw Failure(json.optString("message", "BitVoice answered ${c.responseCode}"))
        return json
    }

    /** Reads a pairing link (bitvoice://pair?server=...&code=...). */
    fun parse(link: String): Pair<String, String>? {
        val u = runCatching { Uri.parse(link.trim()) }.getOrNull() ?: return null
        if (u.scheme != "bitvoice" || u.host != "pair") return null
        val server = u.getQueryParameter("server") ?: return null
        val code = u.getQueryParameter("code") ?: return null
        if (!server.startsWith("https://")) return null // the device secret never travels unencrypted
        return server.trimEnd('/') to code
    }

    fun pair(server: String, code: String, name: String): Pairing {
        val r = post("$server/api/mobile/pair", JSONObject().put("code", code).put("name", name).put("platform", "android"))
        return Pairing(r.optString("server", server).trimEnd('/'), r.getString("device"), r.getString("secret"),
            r.getString("extension"), r.optString("name"))
    }

    fun sendToken(p: Pairing, token: String) {
        post("${p.server}/api/mobile/token", JSONObject().put("device", p.device).put("secret", p.secret).put("token", token))
    }

    fun unpair(p: Pairing) {
        runCatching { post("${p.server}/api/mobile/unpair", JSONObject().put("device", p.device).put("secret", p.secret)) }
    }
}
