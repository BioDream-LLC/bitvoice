import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// The upload key for Google Play lives outside the repository: a properties file named by
// BITVOICE_UPLOAD_KEY (storeFile, storePassword, keyAlias, keyPassword). Without it the release build is
// unsigned.
val upload = System.getenv("BITVOICE_UPLOAD_KEY")?.let { f -> Properties().apply { file(f).inputStream().use { load(it) } } }

// Firebase's app settings (not secret, but BioDream's own): BITVOICE_FIREBASE names the JSON file, which
// goes into the app as assets/firebase.json. Without it the app rings only while open.
val firebase = System.getenv("BITVOICE_FIREBASE")?.let { file(it) }
val firebaseAssets = layout.buildDirectory.dir("generated/firebase")
val copyFirebase by tasks.registering(Copy::class) {
    if (firebase != null) from(firebase) { rename { "firebase.json" } }
    into(firebaseAssets)
}

android {
    namespace = "llc.biodream.bitvoice"
    compileSdk = 36
    defaultConfig {
        applicationId = "llc.biodream.bitvoice"
        minSdk = 29
        targetSdk = 36
        versionCode = 1
        versionName = "1.0.0"
    }
    sourceSets["main"].assets.srcDir(firebaseAssets)
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
    signingConfigs {
        if (upload != null) create("upload") {
            storeFile = file(upload.getProperty("storeFile"))
            storePassword = upload.getProperty("storePassword")
            keyAlias = upload.getProperty("keyAlias")
            keyPassword = upload.getProperty("keyPassword")
        }
    }
    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            if (upload != null) signingConfig = signingConfigs.getByName("upload")
        }
    }
}

dependencies {
    // All Apache-2.0.
    implementation("androidx.core:core-ktx:1.15.0")
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("com.google.firebase:firebase-messaging:24.1.0")
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.9.0")
}

tasks.named("preBuild") { dependsOn(copyFirebase) }
