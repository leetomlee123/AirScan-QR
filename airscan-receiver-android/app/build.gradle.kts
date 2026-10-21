import java.io.ByteArrayOutputStream

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// 自动计算自增版本号与版本名
fun computeVersionCode(): Int {
    val prop = project.findProperty("versionCode")?.toString()?.toIntOrNull()
    if (prop != null) return prop

    val ciRun = System.getenv("GITHUB_RUN_NUMBER")?.toIntOrNull()
    if (ciRun != null) return 100 + ciRun

    return try {
        val stdout = ByteArrayOutputStream()
        project.exec {
            commandLine("git", "rev-list", "--count", "HEAD")
            standardOutput = stdout
        }
        val count = stdout.toString().trim().toIntOrNull() ?: 5
        100 + count
    } catch (e: Exception) {
        105
    }
}

fun computeVersionName(): String {
    val prop = project.findProperty("versionName")?.toString()?.trim()
    if (!prop.isNullOrEmpty()) return prop

    val refName = System.getenv("GITHUB_REF_NAME")?.trim()
    if (!refName.isNullOrEmpty() && refName.startsWith("v")) {
        return refName.removePrefix("v")
    }

    val ciRun = System.getenv("GITHUB_RUN_NUMBER")
    if (!ciRun.isNullOrEmpty()) {
        return "2.0.$ciRun"
    }

    return try {
        val stdout = ByteArrayOutputStream()
        project.exec {
            commandLine("git", "describe", "--tags", "--always")
            standardOutput = stdout
        }
        val tag = stdout.toString().trim()
        if (tag.startsWith("v")) tag.substring(1) else "2.0.1"
    } catch (e: Exception) {
        "2.0.1"
    }
}

val currentVersionCode = computeVersionCode()
val currentVersionName = computeVersionName()

tasks.register("printVersion") {
    doLast {
        println("VERSION_INFO: versionCode=$currentVersionCode, versionName=$currentVersionName")
    }
}

android {
    namespace = "com.airscan.receiver"
    compileSdk = 34

    defaultConfig {
        applicationId = "com.airscan.receiver"
        minSdk = 24
        targetSdk = 34
        versionCode = currentVersionCode
        versionName = currentVersionName

        // 仅打包 64 位 ARM (arm64-v8a)，大幅缩减 APK 体积
        ndk {
            abiFilters.clear()
            abiFilters.add("arm64-v8a")
        }

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        vectorDrawables {
            useSupportLibrary = true
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            signingConfig = signingConfigs.getByName("debug")
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
        }
        debug {
            applicationIdSuffix = ".debug"
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
        buildConfig = true
    }

    composeOptions {
        kotlinCompilerExtensionVersion = "1.5.14"
    }

    packaging {
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
        }
        jniLibs {
            // 仅打包 64 位 ARM (arm64-v8a)，剔除 32 位及 x86 冗余
            excludes += listOf(
                "lib/armeabi-v7a/**",
                "lib/x86/**",
                "lib/x86_64/**"
            )
        }
    }
}

dependencies {
    // AndroidX & Lifecycle
    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.8.2")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.2")
    implementation("androidx.activity:activity-compose:1.9.0")

    // Jetpack Compose
    val composeBom = platform("androidx.compose:compose-bom:2024.06.00")
    implementation(composeBom)
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-graphics")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")

    // CameraX
    val cameraVersion = "1.3.4"
    implementation("androidx.camera:camera-core:$cameraVersion")
    implementation("androidx.camera:camera-camera2:$cameraVersion")
    implementation("androidx.camera:camera-lifecycle:$cameraVersion")
    implementation("androidx.camera:camera-view:$cameraVersion")
    implementation("com.google.guava:guava:33.2.1-android")

    // Google ML Kit Barcode Scanning
    implementation("com.google.mlkit:barcode-scanning:17.3.0")

    // Unit Testing
    testImplementation("junit:junit:4.13.2")
}
