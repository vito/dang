package danglang.intellij.lsp

import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.notification.NotificationAction
import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.options.ShowSettingsUtil
import com.intellij.openapi.project.Project
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
import kotlin.io.path.isDirectory

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

        val binary = DangBinary.find(settings.dangPath)
        if (binary == null) {
            notifyMissingBinary(project, settings.dangPath)
            return
        }
        clientStarter.ensureClientStarted(DangLspClientDescriptor(project, binary.toString(), settings.logFile))
    }

    override fun createWidgetItem(lspClient: LspClient, currentFile: VirtualFile?): LspClientWidgetItem =
        LspClientWidgetItem(lspClient, currentFile, settingsPageClass = DangConfigurable::class.java)
}

private class DangLspClientDescriptor(
    project: Project,
    private val binary: String,
    private val logFile: String?,
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

    override fun createCommandLine(): GeneralCommandLine =
        GeneralCommandLine(binary, "--lsp")
            .apply { if (!logFile.isNullOrBlank()) addParameters("--lsp-log-file", logFile) }
            .withWorkingDirectory(workingDirectory())
            // Use the user's login-shell environment so tools dang shells out
            // to (dagger, direnv) and DAGGER_* variables are visible even when
            // the IDE was launched from a desktop launcher.
            .withParentEnvironmentType(GeneralCommandLine.ParentEnvironmentType.CONSOLE)
            .withCharset(Charsets.UTF_8)

    // The server takes its workspace from rootUri/workspaceFolders; the
    // working directory only needs to exist, or the process won't launch.
    private fun workingDirectory(): Path? =
        (listOfNotNull(project.basePath?.let { Path.of(it) }) +
            roots.filter { it.isInLocalFileSystem }.map { it.toNioPath() })
            .firstOrNull { it.isDirectory() }
}

fun restartDangLspServers(project: Project) {
    project.putUserData(MISSING_BINARY_NOTIFIED, null)
    LspClientManager.getInstance(project).stopAndRestartClientsIfNeeded(DangLspIntegrationProvider::class.java)
}

private val MISSING_BINARY_NOTIFIED = Key.create<Boolean>("dang.missingBinaryNotified")

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
