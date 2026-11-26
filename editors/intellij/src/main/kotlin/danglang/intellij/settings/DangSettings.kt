package danglang.intellij.settings

import com.intellij.openapi.components.BaseState
import com.intellij.openapi.components.Service
import com.intellij.openapi.components.SimplePersistentStateComponent
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage
import com.intellij.openapi.components.service

class DangSettingsState : BaseState() {
    var lspEnabled by property(true)

    /** Path to the `dang` binary; blank means look it up on PATH. */
    var dangPath by string("")

    /** Passed as `--lsp-log-file`; blank means log to stderr (IDE log). */
    var logFile by string("")
}

@Service(Service.Level.APP)
@State(name = "DangSettings", storages = [Storage("dang.xml")])
class DangSettings : SimplePersistentStateComponent<DangSettingsState>(DangSettingsState()) {
    companion object {
        fun getInstance(): DangSettings = service()
    }
}
