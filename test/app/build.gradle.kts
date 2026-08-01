plugins {
    id("com.android.application")
}

android {
    namespace = "com.gravitycone.test"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.gravitycone.test"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "1.0"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_11
        targetCompatibility = JavaVersion.VERSION_11
    }

    sourceSets {
        getByName("main") {
            // 直接引用 FFI SDK 构建产物（相对 app/ 模块目录）：
            //   ../../ffi/android/jniLibs  → GravityCone FFI .so（arm64-v8a / x86_64）
            //   ../../ffi/android/java     → GravityConeAndroidAPI.java（JNI 封装）
            // SDK 重新构建（task build:android:ffi:sdk）后本应用自动使用新产物，无需复制。
            jniLibs.srcDirs("../../ffi/android/jniLibs")
            java.srcDirs("src/main/java", "../../ffi/android/java")
        }
    }
}

dependencies {
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("com.google.android.material:material:1.12.0")
}
