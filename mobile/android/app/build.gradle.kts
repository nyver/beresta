plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

android {
    namespace = "app.beresta.notes"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        applicationId = "app.beresta.notes"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        // Uses the version code from pubspec.yaml. When using split APKs, 1000 * ABI_VERSION
        // is added automatically by Flutter. (https://developer.android.com/studio/build/configure-apk-splits#configure-APK-versions)
        // You can force using the value of versionCode by specifying the `-P force-version-code-ignoring-abi=true`
        // flag during build.
        versionCode = flutter.versionCode
        versionName = flutter.versionName
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    // Release signing reads the upload keystore from environment variables
    // (BERESTA_ANDROID_KEYSTORE_PATH/_PASSWORD/_KEY_ALIAS/_KEY_PASSWORD) so
    // the private key never lives in this repository or in a debug key. A
    // release build with any of these unset fails closed instead of
    // silently falling back to the debug keystore - the same discipline
    // BERESTA_REQUIRE_SIGNING enforces for the Windows installer (see
    // docs/desktop-updates.md).
    val releaseKeystorePath = System.getenv("BERESTA_ANDROID_KEYSTORE_PATH")
    val releaseKeystorePassword = System.getenv("BERESTA_ANDROID_KEYSTORE_PASSWORD")
    val releaseKeyAlias = System.getenv("BERESTA_ANDROID_KEY_ALIAS")
    val releaseKeyPassword = System.getenv("BERESTA_ANDROID_KEY_PASSWORD")
    val releaseSigningConfigured =
        !releaseKeystorePath.isNullOrEmpty() && !releaseKeystorePassword.isNullOrEmpty() &&
            !releaseKeyAlias.isNullOrEmpty() && !releaseKeyPassword.isNullOrEmpty()

    signingConfigs {
        if (releaseSigningConfigured) {
            create("release") {
                storeFile = file(releaseKeystorePath!!)
                storePassword = releaseKeystorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseKeyPassword
            }
        }
    }

    buildTypes {
        debug {
            // A debug build must never share a signature-verified identity
            // with a release build: Android refuses to install an APK over
            // an existing app when the certificates differ
            // (INSTALL_FAILED_UPDATE_INCOMPATIBLE), which made a release
            // APK unable to replace a previously installed debug one under
            // the shared "app.beresta.notes" id. The suffix makes debug
            // builds a distinct app instead, so both can be installed
            // side by side and a release build always installs cleanly.
            applicationIdSuffix = ".debug"
            versionNameSuffix = "-debug"
        }
        release {
            if (releaseSigningConfigured) {
                signingConfig = signingConfigs.getByName("release")
            } else {
                // Fail closed: a `flutter build apk --release` /
                // `appbundle --release` invocation without the signing
                // environment variables must not silently produce a
                // debug-signed release artifact.
                signingConfig = null
            }
            isMinifyEnabled = false
            isShrinkResources = false
        }
    }
}

tasks.matching {
    it.name.endsWith("Release") &&
        (it.name.startsWith("package") || it.name.startsWith("bundle") || it.name.startsWith("assemble"))
}.configureEach {
    doFirst {
        check(
            android.signingConfigs.findByName("release") != null
        ) {
            "Release Android builds require BERESTA_ANDROID_KEYSTORE_PATH, " +
                "BERESTA_ANDROID_KEYSTORE_PASSWORD, BERESTA_ANDROID_KEY_ALIAS, and " +
                "BERESTA_ANDROID_KEY_PASSWORD to be set; see docs/android-build.md."
        }
    }
}

dependencies {
    implementation(files(rootProject.file("../../build/output/beresta-core.aar")))
    implementation("androidx.biometric:biometric:1.1.0")
    implementation("androidx.work:work-runtime-ktx:2.10.5")
    implementation("androidx.documentfile:documentfile:1.1.0")
    androidTestImplementation("androidx.test:runner:1.7.0")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.work:work-testing:2.10.5")
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17
    }
}

flutter {
    source = "../.."
}
