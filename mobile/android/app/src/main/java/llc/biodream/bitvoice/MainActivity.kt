package llc.biodream.bitvoice

import android.Manifest
import android.annotation.SuppressLint
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.os.Build
import android.os.Bundle
import android.view.Gravity
import android.view.ViewGroup.LayoutParams.MATCH_PARENT
import android.view.ViewGroup.LayoutParams.WRAP_CONTENT
import android.webkit.JavascriptInterface
import android.webkit.PermissionRequest
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import com.google.firebase.FirebaseApp
import com.google.firebase.messaging.FirebaseMessaging
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import org.json.JSONObject
import kotlin.concurrent.thread

/** Pairing, then the phone page (BitVoice's #/phone) in a web view bridged to Android's call system. */
class MainActivity : AppCompatActivity() {
    private var web: WebView? = null
    private val scan = registerForActivityResult(ScanContract()) { r -> r.contents?.let { pair(it) } }

    override fun onCreate(state: Bundle?) {
        super.onCreate(state)
        val perms = mutableListOf(Manifest.permission.RECORD_AUDIO)
        if (Build.VERSION.SDK_INT >= 33) perms += Manifest.permission.POST_NOTIFICATIONS
        // Ask only for what is missing: Android refuses an empty request (every start once all are granted).
        val missing = perms.filter { checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED }
        if (missing.isNotEmpty()) requestPermissions(missing.toTypedArray(), 1)
        show()
        intent?.data?.toString()?.let { if (Vault.load(this) == null) pair(it) }
    }

    override fun onNewIntent(i: Intent) {
        super.onNewIntent(i)
        i.data?.toString()?.let { if (Vault.load(this) == null) pair(it) }
        // A push woke the app: make sure the page is still connected.
        if (i.getBooleanExtra("wake", false)) web?.evaluateJavascript("document.visibilityState") { v -> if (v == null) web?.reload() }
    }

    private fun show() {
        val p = Vault.load(this)
        if (p == null) setContentView(pairScreen()) else setContentView(phoneScreen(p))
    }

    private fun pairScreen(): LinearLayout {
        val box = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL; gravity = Gravity.CENTER; setPadding(48, 48, 48, 48); setBackgroundColor(Color.BLACK)
        }
        box.addView(TextView(this).apply { text = "BitVoice"; textSize = 32f; setTextColor(Color.WHITE); gravity = Gravity.CENTER })
        box.addView(TextView(this).apply {
            text = "Ask your administrator to open your extension in BitVoice and press Add a phone, then scan the code."
            setTextColor(Color.LTGRAY); gravity = Gravity.CENTER; setPadding(0, 24, 0, 24)
        })
        box.addView(Button(this).apply {
            text = "Scan the code"
            setOnClickListener { scan.launch(ScanOptions().setDesiredBarcodeFormats(ScanOptions.QR_CODE).setPrompt("Scan the BitVoice code").setBeepEnabled(false)) }
        }, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        val link = EditText(this).apply { hint = "or paste the pairing link"; setTextColor(Color.WHITE); setHintTextColor(Color.GRAY) }
        box.addView(link, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        box.addView(Button(this).apply { text = "Pair"; setOnClickListener { pair(link.text.toString()) } })
        return box
    }

    private fun pair(text: String) {
        val (server, code) = BitVoiceApi.parse(text) ?: return toast("That is not a BitVoice pairing code.")
        thread {
            try {
                val p = BitVoiceApi.pair(server, code, Build.MODEL)
                Vault.save(this, p)
                if (FirebaseApp.getApps(this).isNotEmpty()) {
                    FirebaseMessaging.getInstance().token.addOnSuccessListener { BitVoiceApp.sendToken(this, it) }
                }
                runOnUiThread { show() }
            } catch (e: Exception) {
                runOnUiThread { toast(e.message ?: "Pairing failed") }
            }
        }
    }

    private fun toast(m: String) = android.widget.Toast.makeText(this, m, android.widget.Toast.LENGTH_LONG).show()

    @SuppressLint("SetJavaScriptEnabled")
    private fun phoneScreen(p: Pairing): LinearLayout {
        val box = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setBackgroundColor(Color.BLACK) }
        val bar = LinearLayout(this).apply { setPadding(32, 24, 32, 16); gravity = Gravity.CENTER_VERTICAL }
        bar.addView(TextView(this).apply { text = "Extension ${p.extension}  ${p.name}"; setTextColor(Color.WHITE); textSize = 16f },
            LinearLayout.LayoutParams(0, WRAP_CONTENT, 1f))
        bar.addView(Button(this).apply {
            text = "Unpair"
            setOnClickListener {
                AlertDialog.Builder(this@MainActivity).setMessage("Unpair this phone? It stops ringing for extension ${p.extension}.")
                    .setPositiveButton("Unpair") { _, _ -> thread { BitVoiceApi.unpair(p); Vault.clear(this@MainActivity); runOnUiThread { show() } } }
                    .setNegativeButton("Cancel", null).show()
            }
        })
        box.addView(bar)
        val home = android.net.Uri.parse(p.server)
        val w = WebView(this).apply {
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            settings.mediaPlaybackRequiresUserGesture = false
            setBackgroundColor(Color.BLACK)
            addJavascriptInterface(Bridge(), "bitvoiceAndroid")
            webChromeClient = object : WebChromeClient() {
                // The page asks for the microphone; only BitVoice's own page gets it.
                override fun onPermissionRequest(r: PermissionRequest) {
                    if (r.origin.host == home.host && PermissionRequest.RESOURCE_AUDIO_CAPTURE in r.resources) {
                        r.grant(arrayOf(PermissionRequest.RESOURCE_AUDIO_CAPTURE))
                    } else r.deny()
                }
            }
            webViewClient = object : WebViewClient() {
                // The credentials go in through a script before the page runs, never in the address.
                override fun onPageStarted(view: WebView, url: String?, favicon: android.graphics.Bitmap?) {
                    val creds = JSONObject().put("device", p.device).put("secret", p.secret).toString()
                    view.evaluateJavascript("window.bitvoiceNative = Object.assign($creds, { platform: 'android', post: function (m) { bitvoiceAndroid.post(m) } });", null)
                }

                override fun shouldOverrideUrlLoading(view: WebView, r: WebResourceRequest): Boolean {
                    if (r.url.host == home.host) return false
                    startActivity(Intent(Intent.ACTION_VIEW, r.url))
                    return true
                }
            }
        }
        web = w
        Calls.run = { js -> runOnUiThread { w.evaluateJavascript(js, null) } }
        w.loadUrl("${p.server}/#/phone")
        box.addView(w, LinearLayout.LayoutParams(MATCH_PARENT, 0, 1f))
        return box
    }

    inner class Bridge {
        @JavascriptInterface
        fun post(msg: String) = runOnUiThread { Calls.fromPage(this@MainActivity, msg) }
    }
}
