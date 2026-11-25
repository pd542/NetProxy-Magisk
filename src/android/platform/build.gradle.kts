import java.util.Properties

plugins { java }

val local = Properties().apply {
    rootProject.file("local.properties").takeIf { it.isFile }?.inputStream()?.use { load(it) }
}
val sdk = providers.environmentVariable("ANDROID_HOME").orNull ?: local.getProperty("sdk.dir")
    ?: error("缺少 Android SDK 路径")
val androidJar = file("$sdk/platforms/android-37.2/android.jar")
dependencies {
    compileOnly(files(androidJar))
    testImplementation(files(androidJar))
    testImplementation(libs.junit)
}
tasks.compileJava { options.release.set(25) }

val dexDirectory = layout.buildDirectory.dir("dex")

// 引导类必须能在 app_process 启动时就解析到，单独出一个小 dex，
// 由 -Xbootclasspath 附加；主 dex 则从内存加载，避开厂商的动态加载检测。
val mainJar = tasks.register<Jar>("mainJar") {
    archiveFileName.set("main.jar")
    destinationDirectory.set(layout.buildDirectory.dir("main"))
    from(sourceSets.main.get().output) {
        include("com/fanjv/netproxy/platform/Main*.class")
    }
}

val bootstrapJar = tasks.register<Jar>("bootstrapJar") {
    archiveFileName.set("bootstrap.jar")
    destinationDirectory.set(layout.buildDirectory.dir("bootstrap"))
    from(sourceSets.main.get().output) {
        include("com/fanjv/netproxy/platform/Bootstrap*.class")
    }
}

tasks.register<JavaExec>("assembleBootstrapDex") {
    dependsOn(bootstrapJar)
    classpath = files(Class.forName("com.android.tools.r8.D8").protectionDomain.codeSource.location.toURI())
    mainClass.set("com.android.tools.r8.D8")
    inputs.file(bootstrapJar.flatMap { it.archiveFile })
    inputs.file(androidJar)
    val output = layout.buildDirectory.dir("bootstrap-dex")
    outputs.dir(output)
    doFirst {
        output.get().asFile.apply { mkdirs() }
        args = listOf("--release", "--min-api", "26", "--lib", androidJar.path,
            "--output", output.get().asFile.path, bootstrapJar.get().archiveFile.get().asFile.path)
    }
}

tasks.register<JavaExec>("assembleDex") {
    // Bootstrap 由 boot dex 提供，主 dex 只装 Main，避免重复定义。
    dependsOn(tasks.test, mainJar, "assembleBootstrapDex")
    classpath = files(Class.forName("com.android.tools.r8.D8").protectionDomain.codeSource.location.toURI())
    mainClass.set("com.android.tools.r8.D8")
    inputs.file(mainJar.flatMap { it.archiveFile })
    inputs.file(androidJar)
    inputs.files(classpath).withPropertyName("dexCompiler").withPathSensitivity(PathSensitivity.NONE)
    outputs.dir(dexDirectory)
    doFirst {
        val output = dexDirectory.get().asFile.apply { mkdirs() }
        args = listOf("--release", "--min-api", "26", "--lib", androidJar.path,
            "--output", output.path, mainJar.get().archiveFile.get().asFile.path)
    }
}
