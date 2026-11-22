plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "io.github.jacob3141.doorbell"
    compileSdk = 35

    defaultConfig {
        applicationId = "io.github.jacob3141.doorbell"
        minSdk = 29
        targetSdk = 35
        // CI sets these: the commit count, so the phone takes each newer app as an update, and the
        // tag (releases) or git describe (builds of main)
        versionCode = System.getenv("DOORBELL_VERSION_CODE")?.toIntOrNull() ?: 1
        versionName = System.getenv("DOORBELL_VERSION_NAME") ?: "dev"
    }

    // The release key lives outside the repository (a GitHub secret in CI). Without it the
    // release build stays unsigned.
    val keystore = System.getenv("DOORBELL_KEYSTORE")?.let { file(it) }?.takeIf { it.exists() }
    signingConfigs {
        if (keystore != null) {
            create("doorbell") {
                storeFile = keystore
                storePassword = System.getenv("DOORBELL_KEYSTORE_PASSWORD")
                keyAlias = "doorbell"
                keyPassword = System.getenv("DOORBELL_KEYSTORE_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            if (keystore != null) signingConfig = signingConfigs.getByName("doorbell")
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
        abortOnError = true
        warningsAsErrors = false
    }
}

dependencies {
    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("com.google.android.material:material:1.12.0")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20240303")
}
