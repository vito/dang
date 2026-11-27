package danglang.intellij.lsp

import com.intellij.openapi.util.SystemInfo
import com.intellij.util.EnvironmentUtil
import java.io.File
import java.nio.file.Path
import kotlin.io.path.Path
import kotlin.io.path.isExecutable
import kotlin.io.path.isRegularFile

/** Locates executables the way the language server's environment will see them. */
internal object Executables {
    private fun exe(name: String) = if (SystemInfo.isWindows) "$name.exe" else name

    /**
     * The login-shell PATH (what the server process inherits; see
     * ParentEnvironmentType.CONSOLE), falling back to the IDE's own.
     */
    private fun searchPath(): List<Path> =
        (EnvironmentUtil.getValue("PATH") ?: System.getenv("PATH") ?: "")
            .split(File.pathSeparatorChar).filter { it.isNotBlank() }.map { Path(it) }

    /** The configured path if set, otherwise [name] on PATH, then in [extraDirs]. */
    fun find(name: String, configured: String?, extraDirs: () -> List<Path> = { emptyList() }): Path? {
        if (!configured.isNullOrBlank()) return Path(configured).takeIf { it.isRegularFile() }
        return (searchPath() + extraDirs())
            .map { it.resolve(exe(name)) }
            .firstOrNull { it.isRegularFile() && it.isExecutable() }
    }

    private val home get() = Path(System.getProperty("user.home"))

    fun findDang(configured: String?): Path? = find("dang", configured) {
        // IDEs launched from a desktop entry often lack the user's Go bin
        // directory on PATH.
        buildList {
            System.getenv("GOBIN")?.takeIf { it.isNotBlank() }?.let { add(Path(it)) }
            System.getenv("GOPATH")?.takeIf { it.isNotBlank() }?.let { gp ->
                gp.split(File.pathSeparatorChar).forEach { add(Path(it, "bin")) }
            }
            add(home.resolve("go/bin"))
        }
    }

    fun findMise(configured: String?): Path? = find("mise", configured) {
        // mise's installer and package managers' usual locations.
        listOf(home.resolve(".local/bin"), home.resolve(".local/share/mise/bin"), Path("/usr/local/bin"), Path("/opt/homebrew/bin"))
    }
}
