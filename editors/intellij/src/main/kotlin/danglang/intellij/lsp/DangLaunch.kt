package danglang.intellij.lsp

import java.nio.file.Path
import kotlin.io.path.exists
import kotlin.io.path.isDirectory

/**
 * How to start `dang --lsp`: directly, or through `mise exec` when the
 * project pins its tools (notably dagger, whose CLI version decides which
 * engine a session starts) with mise.
 */
internal data class DangLaunch(val command: List<String>, val workingDirectory: Path?, val viaMise: Boolean) {
    companion object {
        /** Files mise reads its configuration from, relative to a directory. */
        val MISE_CONFIG_FILES = listOf(
            "mise.toml", ".mise.toml", "mise.local.toml", ".mise.local.toml",
            ".config/mise.toml", ".config/mise/config.toml", "mise/config.toml", ".mise/config.toml",
        )

        fun hasMiseConfig(dir: Path) = MISE_CONFIG_FILES.any { dir.resolve(it).exists() }

        /**
         * @param dang the dang binary, or null to let mise's environment
         *   resolve `dang` (only possible with [mise]).
         * @param mise the mise binary, or null when mise isn't to be used.
         * @param rootCandidates project roots, in order of preference.
         */
        fun resolve(dang: Path?, mise: Path?, rootCandidates: List<Path>, logFile: String?): DangLaunch? {
            val roots = rootCandidates.filter { it.isDirectory() }
            val miseRoot = if (mise != null) roots.firstOrNull(::hasMiseConfig) else null
            val dangArgs = buildList {
                add(dang?.toString() ?: "dang")
                add("--lsp")
                if (!logFile.isNullOrBlank()) addAll(listOf("--lsp-log-file", logFile))
            }
            return when {
                miseRoot != null -> DangLaunch(listOf(mise.toString(), "exec", "--") + dangArgs, miseRoot, viaMise = true)
                dang != null -> DangLaunch(dangArgs, roots.firstOrNull(), viaMise = false)
                else -> null
            }
        }
    }
}
