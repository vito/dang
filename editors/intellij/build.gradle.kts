import org.jetbrains.intellij.platform.gradle.IntelliJPlatformType
import org.jetbrains.kotlin.gradle.dsl.JvmDefaultMode
import org.jetbrains.intellij.platform.gradle.TestFrameworkType

plugins {
    id("org.jetbrains.kotlin.jvm") version "2.4.21"
    id("org.jetbrains.intellij.platform") version "2.20.1"
}

group = "danglang"

// Local builds are stamped with the commit they were built from, e.g.
// 0.1.0+77e01fb (or 0.1.0+77e01fb.dirty with uncommitted changes), so the
// installed build is identifiable in Settings | Plugins. Release builds pass
// -Prelease to get the plain version.
val baseVersion = providers.gradleProperty("pluginVersion").get()
val gitCommit = providers.exec {
    commandLine("git", "describe", "--always", "--abbrev=7", "--dirty=.dirty", "--match=NO-TAGS")
    isIgnoreExitValue = true
}.standardOutput.asText.map { it.trim() }.orElse("")
version = if (providers.gradleProperty("release").isPresent || gitCommit.get().isEmpty()) {
    baseVersion
} else {
    "$baseVersion+${gitCommit.get()}"
}

kotlin {
    jvmToolchain(21)
    compilerOptions {
        // Use the platform interfaces' Java default methods directly, rather
        // than generating bridges that override (deprecated/experimental)
        // members we don't implement.
        jvmDefault = JvmDefaultMode.NO_COMPATIBILITY
    }
}

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    intellijPlatform {
        intellijIdea(providers.gradleProperty("platformVersion"))
        testFramework(TestFrameworkType.Platform)
    }
    testImplementation("junit:junit:4.13.2")
}

intellijPlatform {
    // Pure Kotlin, no GUI forms or Java @NotNull assertions to instrument.
    instrumentCode = false

    pluginConfiguration {
        version = project.version.toString()
        ideaVersion {
            sinceBuild = providers.gradleProperty("pluginSinceBuild")
            untilBuild = provider { null }
        }
    }
    pluginVerification {
        ides {
            create(IntelliJPlatformType.IntellijIdea, providers.gradleProperty("platformVersion"))
        }
    }
}

tasks.test {
    // The lexer mirrors the VS Code TextMate grammar; a test checks they agree.
    systemProperty(
        "dang.tmLanguage",
        layout.projectDirectory.file("../vscode/syntaxes/dang.tmLanguage.json").asFile.absolutePath,
    )
    // Integration tests start a real `dang --lsp`; point DANG_BINARY at one
    // (e.g. `go build -o /tmp/dang ./cmd/dang`) or they are skipped.
    providers.environmentVariable("DANG_BINARY").orNull?.let { systemProperty("dang.binary", it) }
}
