# The phone page calls these through window.bitvoiceAndroid: R8 must keep their names.
-keepclassmembers class llc.biodream.bitvoice.MainActivity$Bridge {
    @android.webkit.JavascriptInterface <methods>;
}
