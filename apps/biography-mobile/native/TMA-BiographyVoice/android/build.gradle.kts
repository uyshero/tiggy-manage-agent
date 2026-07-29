plugins {
    id("com.android.library") version "8.7.3"
    kotlin("android") version "2.0.21"
}

android {
    namespace = "com.tma.biography.voice"
    compileSdk = 35

    defaultConfig {
        minSdk = 23
        consumerProguardFiles("consumer-rules.pro")
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_1_8
        targetCompatibility = JavaVersion.VERSION_1_8
    }

    kotlinOptions { jvmTarget = "1.8" }
}

dependencies {
    // Copy uniapp-v8-release.aar from the matching DCloud offline SDK into android/libs.
    compileOnly(files("libs/uniapp-v8-release.aar"))
    compileOnly("com.alibaba:fastjson:1.2.83")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("androidx.security:security-crypto:1.1.0-alpha06")
}
