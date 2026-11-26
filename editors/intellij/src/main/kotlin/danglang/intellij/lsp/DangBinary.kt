package danglang.intellij.lsp

import com.intellij.openapi.util.SystemInfo
import java.io.File
import java.nio.file.Path
import kotlin.io.path.Path
import kotlin.io.path.isExecutable
import kotlin.io.path.isRegularFile

object DangBinary {
    private val exeName = if (SystemInfo.isWindows) "dang.exe" else "dang"

    /**
     * Resolves the `dang` binary: the configured path if set, otherwise PATH,
     * otherwise the default `go install` locations.
     */
    fun find(configured: String?): Path? {
        if (!configured.isNullOrBlank()) {
            return Path(configured).takeIf { it.isRegularFile() }
        }
        val candidates = buildList {
            System.getenv("PATH")?.split(File.pathSeparatorChar)?.filter { it.isNotBlank() }?.forEach { add(Path(it)) }
            // Fallbacks for IDEs launched from a desktop entry, whose PATH
            // often lacks the user's Go bin directory.
            System.getenv("GOBIN")?.takeIf { it.isNotBlank() }?.let { add(Path(it)) }
            System.getenv("GOPATH")?.takeIf { it.isNotBlank() }?.let { gp ->
                gp.split(File.pathSeparatorChar).forEach { add(Path(it, "bin")) }
            }
            add(Path(System.getProperty("user.home"), "go", "bin"))
        }
        return candidates.map { it.resolve(exeName) }.firstOrNull { it.isRegularFile() && it.isExecutable() }
    }
}
