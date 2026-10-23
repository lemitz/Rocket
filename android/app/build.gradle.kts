plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// One version for everything: the VERSION file in the repository root (also read by the Go build
// and the release workflow). versionCode = major * 10000 + minor * 100 + patch.
val rocketVersion = rootProject.file("../VERSION").readText().trim()
require(Regex("""\d+\.\d+\.\d+""").matches(rocketVersion)) { "VERSION must look like 1.2.3, got '$rocketVersion'" }
val versionParts = rocketVersion.split('.').map { it.toInt() }

// Release signing comes from the environment (CI secrets); see scripts/make-keystore.ps1.
// Without it the release APK is signed with the local debug key so it still installs.
val keystorePath: String? = System.getenv("ROCKET_KEYSTORE_FILE")?.takeIf { it.isNotBlank() }

android {
    namespace = "dev.rocket.android"
    compileSdk = 36
    androidResources {
        generateLocaleConfig = true
        localeFilters += listOf("en", "ru")
    }

    defaultConfig {
        applicationId = "dev.rocket.android"
        minSdk = 34
        targetSdk = 36
        versionCode = versionParts[0] * 10000 + versionParts[1] * 100 + versionParts[2]
        versionName = rocketVersion
    }

    signingConfigs {
        if (keystorePath != null) {
            create("release") {
                storeFile = file(keystorePath)
                storePassword = System.getenv("ROCKET_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("ROCKET_KEY_ALIAS")
                keyPassword = System.getenv("ROCKET_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName(if (keystorePath != null) "release" else "debug")
        }
    }

    // Release lint needs artifacts that are not cached for --offline builds; the debug build is the gate.
    lint { checkReleaseBuilds = false }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
}
