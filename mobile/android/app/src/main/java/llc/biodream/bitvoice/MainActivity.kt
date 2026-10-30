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
        answerFrom(intent)
        askFullScreen()
    }

    /** Answer on the ringing notification opens the app with the call to answer. */
    private fun answerFrom(i: Intent?) {
        i?.getStringExtra("answer")?.let { Calls.answer(it) }
    }

    /**
     * From Android 14 a call may ring over the lock screen only with the user's leave (Google Play gives it
     * to calling apps on install; a phone set up otherwise may not have it). Asked once, after pairing.
     */
    private fun askFullScreen() {
        if (Build.VERSION.SDK_INT < 34 || Vault.load(this) == null) return
        val nm = getSystemService(android.app.NotificationManager::class.java)
        val prefs = getSharedPreferences("bitvoice", MODE_PRIVATE)
        if (nm.canUseFullScreenIntent() || prefs.getBoolean("askedFullScreen", false)) return
        prefs.edit().putBoolean("askedFullScreen", true).apply()
        AlertDialog.Builder(this).setMessage("Let BitVoice show calls over the lock screen, so a call rings like a phone call?")
            .setPositiveButton("Settings") { _, _ ->
                runCatching { startActivity(Intent(android.provider.Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT, android.net.Uri.parse("package:$packageName"))) }
            }
            .setNegativeButton("Not now", null).show()
    }

    override fun onNewIntent(i: Intent) {
        super.onNewIntent(i)
        i.data?.toString()?.let { if (Vault.load(this) == null) pair(it) }
        answerFrom(i)
        // A push woke the app: make sure the page is still connected.
        if (i.getBooleanExtra("wake", false)) web?.evaluateJavascript("document.visibilityState") { v -> if (v == null) web?.reload() }
    }

    private fun show() {
        val p = Vault.load(this)
        val v = if (p == null) pairScreen() else phoneScreen(p)
        // From Android 15 apps are drawn edge to edge: keep the content clear of the status and
        // navigation bars (they covered the extension and the Unpair button).
        // The bars' space is added to the screen's own padding, not put in its place.
        val (l, t, r, bt) = listOf(v.paddingLeft, v.paddingTop, v.paddingRight, v.paddingBottom)
        androidx.core.view.ViewCompat.setOnApplyWindowInsetsListener(v) { view, insets ->
            val b = insets.getInsets(androidx.core.view.WindowInsetsCompat.Type.systemBars() or androidx.core.view.WindowInsetsCompat.Type.displayCutout())
            view.setPadding(l + b.left, t + b.top, r + b.right, bt + b.bottom)
            insets
        }
        setContentView(v)
    }

    private fun dp(v: Int) = (v * resources.displayMetrics.density).toInt()

    /** A button in the web interface's style: rounded, filled (primary) or outlined. */
    private fun button(label: String, primary: Boolean, onClick: () -> Unit) = Button(this).apply {
        text = label; isAllCaps = false; textSize = 16f; stateListAnimator = null
        setTextColor(if (primary) Color.parseColor("#ECFDF5") else Color.parseColor("#E2E8F0"))
        background = android.graphics.drawable.GradientDrawable().apply {
            cornerRadius = dp(12).toFloat()
            setColor(Color.parseColor(if (primary) "#059669" else "#1E293B"))
            if (!primary) setStroke(dp(1), Color.parseColor("#334155"))
        }
        setPadding(dp(16), dp(14), dp(16), dp(14))
        setOnClickListener { onClick() }
    }

    private fun pairScreen(): LinearLayout {
        val box = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL; gravity = Gravity.CENTER; setPadding(dp(28), dp(28), dp(28), dp(28))
            setBackgroundColor(Color.parseColor("#050816"))
        }
        val gap = { h: Int -> LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT).apply { topMargin = dp(h) } }
        box.addView(android.widget.ImageView(this).apply {
            setImageResource(R.drawable.ic_launcher_foreground)
            background = android.graphics.drawable.GradientDrawable().apply { cornerRadius = dp(24).toFloat(); setColor(Color.parseColor("#0B1220")) }
        }, LinearLayout.LayoutParams(dp(96), dp(96)))
        box.addView(TextView(this).apply { text = "BitVoice"; textSize = 30f; setTextColor(Color.WHITE); gravity = Gravity.CENTER
            typeface = android.graphics.Typeface.DEFAULT_BOLD }, gap(16))
        box.addView(TextView(this).apply {
            text = "Your office phone, on your phone. Ask your administrator to open your extension in BitVoice and press Add a phone, then scan the code it shows."
            textSize = 15f; setTextColor(Color.parseColor("#94A3B8")); gravity = Gravity.CENTER; setLineSpacing(0f, 1.2f)
        }, gap(12))
        box.addView(button("Scan the code", true) {
            scan.launch(ScanOptions().setDesiredBarcodeFormats(ScanOptions.QR_CODE).setPrompt("Scan the BitVoice code").setBeepEnabled(false))
        }, gap(32))
        val link = EditText(this).apply {
            hint = "or paste the pairing link"; textSize = 15f; setTextColor(Color.WHITE); setHintTextColor(Color.parseColor("#64748B")); isSingleLine = true
            background = android.graphics.drawable.GradientDrawable().apply { cornerRadius = dp(12).toFloat(); setColor(Color.parseColor("#0B1220")); setStroke(dp(1), Color.parseColor("#334155")) }
            setPadding(dp(16), dp(14), dp(16), dp(14))
        }
        box.addView(link, gap(24))
        box.addView(button("Pair", false) { pair(link.text.toString()) }, gap(12))
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
                runOnUiThread { show(); askFullScreen() }
            } catch (e: Exception) {
                runOnUiThread { toast(e.message ?: "Pairing failed") }
            }
        }
    }

    private fun toast(m: String) = android.widget.Toast.makeText(this, m, android.widget.Toast.LENGTH_LONG).show()

    @SuppressLint("SetJavaScriptEnabled")
    private fun phoneScreen(p: Pairing): LinearLayout {
        // The phone page shows the extension itself: the bar holds only the app's own action.
        val box = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setBackgroundColor(Color.parseColor("#050816")) }
        val bar = LinearLayout(this).apply { setPadding(dp(16), dp(8), dp(12), dp(4)); gravity = Gravity.CENTER_VERTICAL }
        bar.addView(TextView(this).apply { text = "BitVoice"; setTextColor(Color.parseColor("#7DD3FC")); textSize = 17f
            typeface = android.graphics.Typeface.DEFAULT_BOLD }, LinearLayout.LayoutParams(0, WRAP_CONTENT, 1f))
        bar.addView(button("Unpair", false) {
            AlertDialog.Builder(this@MainActivity).setMessage("Unpair this phone? It stops ringing for extension ${p.extension}.")
                .setPositiveButton("Unpair") { _, _ -> thread { BitVoiceApi.unpair(p); Vault.clear(this@MainActivity); runOnUiThread { show() } } }
                .setNegativeButton("Cancel", null).show()
        }.apply { textSize = 14f; setPadding(dp(14), dp(6), dp(14), dp(6)); minHeight = 0; minimumHeight = 0 })
        box.addView(bar)
        val home = android.net.Uri.parse(p.server)
        val w = WebView(this).apply {
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            settings.mediaPlaybackRequiresUserGesture = false
            settings.setSupportMultipleWindows(true) // a video room opens with window.open
            settings.javaScriptCanOpenWindowsAutomatically = true
            setBackgroundColor(Color.BLACK)
            addJavascriptInterface(Bridge(), "bitvoiceAndroid")
            webChromeClient = object : WebChromeClient() {
                // The page asks for the microphone (and the camera, for video rooms); only BitVoice's page gets it.
                override fun onPermissionRequest(r: PermissionRequest) {
                    if (r.origin.host == home.host) {
                        r.grant(r.resources.filter { it == PermissionRequest.RESOURCE_AUDIO_CAPTURE || it == PermissionRequest.RESOURCE_VIDEO_CAPTURE }.toTypedArray())
                    } else r.deny()
                }

                // window.open (a video room) loads in this same web view.
                override fun onCreateWindow(view: WebView, dialog: Boolean, gesture: Boolean, msg: android.os.Message): Boolean {
                    val t = WebView(view.context)
                    t.webViewClient = object : WebViewClient() {
                        override fun shouldOverrideUrlLoading(v: WebView, r: WebResourceRequest): Boolean { view.loadUrl(r.url.toString()); return true }
                    }
                    (msg.obj as WebView.WebViewTransport).webView = t
                    msg.sendToTarget()
                    return true
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
