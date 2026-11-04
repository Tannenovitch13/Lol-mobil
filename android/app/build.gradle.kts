plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "fr.lolmobil"
    compileSdk = 35

    defaultConfig {
        applicationId = "fr.lolmobil"
        minSdk = 24
        targetSdk = 35
        versionCode = (System.getenv("LOLMOBIL_VERSION_CODE") ?: "1").toInt()
        versionName = System.getenv("LOLMOBIL_VERSION_NAME") ?: "1.0.0-dev"
    }

    // All builds are signed with the same key so a new APK installs over the
    // previous one. CI uses the keystore given in its secrets when present,
    // else the development keystore committed here (anyone can sign with it:
    // set your own secrets before distributing the app widely).
    signingConfigs {
        create("lolmobil") {
            storeFile = file(System.getenv("LOLMOBIL_KEYSTORE") ?: "dev-signing.keystore")
            storePassword = System.getenv("LOLMOBIL_KEYSTORE_PASSWORD") ?: "lolmobil-dev"
            keyAlias = System.getenv("LOLMOBIL_KEY_ALIAS") ?: "lolmobil"
            keyPassword = System.getenv("LOLMOBIL_KEY_PASSWORD") ?: "lolmobil-dev"
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName("lolmobil")
        }
        debug {
            signingConfig = signingConfigs.getByName("lolmobil")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    buildFeatures {
        buildConfig = true
    }

    lint {
        abortOnError = false
        checkReleaseBuilds = false
    }
}

dependencies {
    implementation("androidx.core:core-ktx:1.15.0")
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("androidx.activity:activity-ktx:1.9.3")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
}
