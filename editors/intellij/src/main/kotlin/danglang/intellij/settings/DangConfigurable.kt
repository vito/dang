package danglang.intellij.settings

import com.intellij.openapi.fileChooser.FileChooserDescriptorFactory
import com.intellij.openapi.options.BoundConfigurable
import com.intellij.openapi.project.ProjectManager
import com.intellij.openapi.ui.DialogPanel
import com.intellij.ui.dsl.builder.AlignX
import com.intellij.ui.dsl.builder.bindSelected
import com.intellij.ui.dsl.builder.bindText
import com.intellij.ui.dsl.builder.panel
import danglang.intellij.lsp.DangBinary
import danglang.intellij.lsp.restartDangLspServers

class DangConfigurable : BoundConfigurable("Dang") {
    private val state get() = DangSettings.getInstance().state

    override fun createPanel(): DialogPanel = panel {
        row {
            checkBox("Enable language server")
                .bindSelected({ state.lspEnabled }, { state.lspEnabled = it })
        }
        row("Path to dang:") {
            textFieldWithBrowseButton(
                FileChooserDescriptorFactory.singleFile().withTitle("Select dang Binary"),
            )
                .align(AlignX.FILL)
                .bindText({ state.dangPath.orEmpty() }, { state.dangPath = it.trim() })
                .comment(
                    DangBinary.find(null)?.let { "Leave empty to use $it" }
                        ?: "Leave empty to search PATH (dang was not found there)",
                )
        }
        row("LSP log file:") {
            textFieldWithBrowseButton(
                FileChooserDescriptorFactory.singleFile().withTitle("Select LSP Log File"),
            )
                .align(AlignX.FILL)
                .bindText({ state.logFile.orEmpty() }, { state.logFile = it.trim() })
                .comment("Optional; passed to dang as --lsp-log-file")
        }
    }

    override fun apply() {
        super.apply()
        ProjectManager.getInstance().openProjects.forEach(::restartDangLspServers)
    }
}
