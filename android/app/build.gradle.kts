plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
}

android {
    namespace = "dev.castor.android"
    compileSdk = 36

    defaultConfig {
        applicationId = "dev.castor.android"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
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
        compose = true
    }
    // Generated protobuf/Connect code (see android/README.md); never edited by hand.
    sourceSets["main"].java.srcDir("src/main/gen")
    testOptions {
        unitTests.isReturnDefaultValues = true
    }
}

dependencies {
    implementation(platform(libs.androidx.compose.bom))
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.compose.ui)
    implementation(libs.androidx.compose.material3)
    implementation(libs.androidx.compose.material.icons)
    implementation(libs.androidx.compose.tooling.preview)
    debugImplementation(libs.androidx.compose.tooling)
    implementation(libs.androidx.security.crypto)
    implementation(libs.kotlinx.coroutines.android)

    implementation(libs.connect.kotlin)
    implementation(libs.connect.kotlin.okhttp)
    implementation(libs.connect.kotlin.javalite)
    implementation(libs.okhttp)
    // Must be >= the protoc version that generated src/main/gen (see header of the generated files).
    implementation(libs.protobuf.javalite)
    implementation(libs.protobuf.kotlin.lite)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
}
