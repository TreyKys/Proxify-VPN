import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    alias(libs.plugins.kotlin.jvm)
}

// The core module is pure Kotlin/JVM on purpose. The reliability engine is the
// part of this product that has to be correct, and keeping Android out of it
// means it can be tested in milliseconds instead of on an emulator.
//
// Bytecode targets 17 because the Android module consumes this module and AGP
// will not read newer class files. We target rather than use a toolchain so the
// build works on whatever JDK (17+) a contributor happens to have.
kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_17)
    }
}

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

tasks.withType<JavaCompile>().configureEach {
    options.release.set(17)
}

dependencies {
    testImplementation(kotlin("test"))
}

tasks.test {
    useJUnitPlatform()
}
