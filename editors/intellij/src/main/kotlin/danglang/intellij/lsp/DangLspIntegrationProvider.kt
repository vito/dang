package danglang.intellij.lsp

import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.notification.NotificationAction
import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.options.ShowSettingsUtil
import com.intellij.openapi.diagnostic.logger
import com.intellij.openapi.project.Project
import com.intellij.openapi.roots.ProjectRootManager
import com.intellij.openapi.util.Key
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.platform.lsp.api.LspClient
import com.intellij.platform.lsp.api.LspClientManager
import com.intellij.platform.lsp.api.LspIntegrationProvider
import com.intellij.platform.lsp.api.ProjectWideLspClientDescriptor
import com.intellij.platform.lsp.api.customization.LspCodeActionsSupport
import com.intellij.platform.lsp.api.customization.LspCustomization
import com.intellij.platform.lsp.api.customization.LspDocumentHighlightsSupport
import com.intellij.platform.lsp.api.customization.LspRenameSupport
import com.intellij.platform.lsp.api.customization.LspSemanticTokensSupport
import com.intellij.platform.lsp.api.lsWidget.LspClientWidgetItem
import com.intellij.psi.PsiFile
import danglang.intellij.settings.DangConfigurable
import danglang.intellij.settings.DangSettings
import java.nio.file.Path

private fun VirtualFile.isDang() = extension == "dang"

class DangLspIntegrationProvider : LspIntegrationProvider {
    override fun fileOpened(
        project: Project,
        file: VirtualFile,
        clientStarter: LspIntegrationProvider.LspClientStarter,
    ) {
        if (!file.isDang()) return
        val settings = DangSettings.getInstance().state
        if (!settings.lspEnabled) return

        val dang = Executables.findDang(settings.dangPath)
        if (dang == null && !settings.dangPath.isNullOrBlank()) {
            notifyMissingBinary(project, settings.dangPath)
            return
        }
        val roots = rootCandidates(project)
        val mise = if (settings.useMise) Executables.findMise(settings.misePath) else null
        if (settings.useMise && mise == null && roots.any(DangLaunch::hasMiseConfig)) {
            notifyMiseMissing(project, settings.misePath)
        }
        val launch = DangLaunch.resolve(dang, mise, roots, settings.logFile)
        if (launch == null) {
            notifyMissingBinary(project, settings.dangPath)
            return
        }
        clientStarter.ensureClientStarted(DangLspClientDescriptor(project, launch))
    }

    /** Where to look for a mise config and run the server: the project's base directory, then its content roots. */
    private fun rootCandidates(project: Project): List<Path> =
        (listOfNotNull(project.basePath?.let { Path.of(it) }) +
            ProjectRootManager.getInstance(project).contentRoots.filter { it.isInLocalFileSystem }.map { it.toNioPath() })
            .distinct()

    override fun createWidgetItem(lspClient: LspClient, currentFile: VirtualFile?): LspClientWidgetItem =
        LspClientWidgetItem(lspClient, currentFile, settingsPageClass = DangConfigurable::class.java)
}

private class DangLspClientDescriptor(
    project: Project,
    private val launch: DangLaunch,
) : ProjectWideLspClientDescriptor(project, "Dang") {
    override fun isSupportedFile(file: VirtualFile) = file.isDang()

    override fun getLanguageId(file: VirtualFile) = "dang"

    // Some LSP features default to running only for plain-text and TextMate
    // files, assuming any real language brings its own. Dang's PSI is flat and
    // all semantics come from the server, so opt in.
    override val lspCustomization = object : LspCustomization() {
        override val renameCustomizer = object : LspRenameSupport() {
            override fun shouldRunRename(psiFile: PsiFile) = true
        }
        override val codeActionsCustomizer = object : LspCodeActionsSupport() {
            override fun shouldRunRefactoring(psiFile: PsiFile) = true
        }
        override val documentHighlightsCustomizer = object : LspDocumentHighlightsSupport() {
            override fun shouldAskServerForDocumentHighlights(psiFile: PsiFile) = true
        }
        override val semanticTokensCustomizer = object : LspSemanticTokensSupport() {
            override fun shouldAskServerForSemanticTokens(psiFile: PsiFile) = true
        }
    }

    override fun createCommandLine(): GeneralCommandLine {
        LOG.info("Starting Dang language server: ${launch.command.joinToString(" ")} (in ${launch.workingDirectory})")
        return GeneralCommandLine(launch.command)
            // With mise, the root holding the mise config, which mise reads
            // from the working directory. Otherwise any existing project
            // directory: the server takes its workspace from rootUri, but the
            // process won't launch in a directory that doesn't exist.
            .withWorkingDirectory(launch.workingDirectory)
            // Use the user's login-shell environment so tools dang shells out
            // to (dagger, direnv) and DAGGER_* variables are visible even when
            // the IDE was launched from a desktop launcher.
            .withParentEnvironmentType(GeneralCommandLine.ParentEnvironmentType.CONSOLE)
            .withCharset(Charsets.UTF_8)
    }

    private companion object {
        val LOG = logger<DangLspClientDescriptor>()
    }
}

fun restartDangLspServers(project: Project) {
    project.putUserData(MISSING_BINARY_NOTIFIED, null)
    project.putUserData(MISSING_MISE_NOTIFIED, null)
    LspClientManager.getInstance(project).stopAndRestartClientsIfNeeded(DangLspIntegrationProvider::class.java)
}

private val MISSING_BINARY_NOTIFIED = Key.create<Boolean>("dang.missingBinaryNotified")
private val MISSING_MISE_NOTIFIED = Key.create<Boolean>("dang.missingMiseNotified")

private fun notifyMiseMissing(project: Project, configured: String?) {
    if (project.getUserData(MISSING_MISE_NOTIFIED) == true) return
    project.putUserData(MISSING_MISE_NOTIFIED, true)

    val content = if (configured.isNullOrBlank()) {
        "This project has a mise config, but <code>mise</code> was not found on PATH, so the language server " +
            "uses the tools on PATH instead of the project's pinned versions (e.g. a different dagger, " +
            "which starts a different engine). Install mise or set its path."
    } else {
        "The configured <code>mise</code> binary does not exist: <code>$configured</code>. " +
            "The language server uses the tools on PATH instead of the project's pinned versions."
    }
    NotificationGroupManager.getInstance()
        .getNotificationGroup("Dang")
        .createNotification("Dang language server not using mise", content, NotificationType.WARNING)
        .addAction(
            NotificationAction.createSimpleExpiring("Configure…") {
                ShowSettingsUtil.getInstance().showSettingsDialog(project, DangConfigurable::class.java)
            },
        )
        .notify(project)
}

private fun notifyMissingBinary(project: Project, configured: String?) {
    if (project.getUserData(MISSING_BINARY_NOTIFIED) == true) return
    project.putUserData(MISSING_BINARY_NOTIFIED, true)

    val content = if (configured.isNullOrBlank()) {
        "The <code>dang</code> binary was not found on PATH. Install it with " +
            "<code>go install github.com/vito/dang/v2/cmd/dang@latest</code> or set its path."
    } else {
        "The configured <code>dang</code> binary does not exist: <code>$configured</code>"
    }
    NotificationGroupManager.getInstance()
        .getNotificationGroup("Dang")
        .createNotification("Dang language server not started", content, NotificationType.WARNING)
        .addAction(
            NotificationAction.createSimpleExpiring("Configure…") {
                ShowSettingsUtil.getInstance().showSettingsDialog(project, DangConfigurable::class.java)
            },
        )
        .notify(project)
}
