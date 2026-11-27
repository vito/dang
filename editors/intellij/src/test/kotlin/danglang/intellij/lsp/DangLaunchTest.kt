package danglang.intellij.lsp

import junit.framework.TestCase
import java.nio.file.Files
import java.nio.file.Path
import kotlin.io.path.createDirectories
import kotlin.io.path.createFile

class DangLaunchTest : TestCase() {
    private lateinit var tmp: Path
    private val dang = Path.of("/opt/dang/bin/dang")
    private val mise = Path.of("/opt/mise/bin/mise")

    override fun setUp() {
        super.setUp()
        tmp = Files.createTempDirectory("dang-launch")
    }

    override fun tearDown() {
        tmp.toFile().deleteRecursively()
        super.tearDown()
    }

    private fun dir(name: String, vararg files: String): Path {
        val d = tmp.resolve(name).createDirectories()
        files.forEach { d.resolve(it).also { f -> f.parent.createDirectories() }.createFile() }
        return d
    }

    fun testDetectsEveryMiseConfigLocation() {
        for (file in DangLaunch.MISE_CONFIG_FILES) {
            assertTrue(file, DangLaunch.hasMiseConfig(dir("p-" + file.replace('/', '_'), file)))
        }
        assertFalse(DangLaunch.hasMiseConfig(dir("plain", "dang.toml")))
    }

    fun testLaunchesThroughMiseInTheRootWithTheConfig() {
        val base = dir("base")
        val root = dir("root", "mise.toml")
        val launch = DangLaunch.resolve(dang, mise, listOf(base, root), logFile = "/tmp/lsp.log")!!
        assertEquals(listOf("$mise", "exec", "--", "$dang", "--lsp", "--lsp-log-file", "/tmp/lsp.log"), launch.command)
        assertEquals(root, launch.workingDirectory)
        assertTrue(launch.viaMise)
    }

    fun testLetsMiseResolveDangWhenItIsNotOnPath() {
        val root = dir("root", ".mise.toml")
        assertEquals(listOf("$mise", "exec", "--", "dang", "--lsp"), DangLaunch.resolve(null, mise, listOf(root), null)!!.command)
    }

    fun testFallsBackToPathWithoutMiseOrConfig() {
        val withConfig = dir("with", "mise.toml")
        val without = dir("without")
        // mise not found, or the setting off: plain launch even with a config.
        DangLaunch.resolve(dang, null, listOf(withConfig), null)!!.let {
            assertEquals(listOf("$dang", "--lsp"), it.command)
            assertEquals(withConfig, it.workingDirectory)
            assertFalse(it.viaMise)
        }
        // mise available but no config: plain launch.
        assertEquals(listOf("$dang", "--lsp"), DangLaunch.resolve(dang, mise, listOf(without), null)!!.command)
        // Nothing to run.
        assertNull(DangLaunch.resolve(null, null, listOf(withConfig), null))
        assertNull(DangLaunch.resolve(null, mise, listOf(without), null))
    }

    fun testSkipsRootsThatDoNotExist() {
        val root = dir("root")
        val launch = DangLaunch.resolve(dang, null, listOf(tmp.resolve("missing"), root), null)!!
        assertEquals(root, launch.workingDirectory)
    }
}
