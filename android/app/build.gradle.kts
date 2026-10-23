plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

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
        versionCode = 1
        versionName = "0.1"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions { jvmTarget = "17" }
}
