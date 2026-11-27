package danglang.intellij.lang

import com.intellij.lang.Language
import com.intellij.openapi.fileTypes.LanguageFileType
import com.intellij.openapi.util.IconLoader
import javax.swing.Icon

object DangLanguage : Language("Dang") {
    private fun readResolve(): Any = DangLanguage
}

object DangFileType : LanguageFileType(DangLanguage) {
    override fun getName() = "Dang"
    override fun getDescription() = "Dang"
    override fun getDefaultExtension() = "dang"
    override fun getIcon(): Icon = DangIcons.FILE

    private fun readResolve(): Any = DangFileType
}

object DangIcons {
    @JvmField
    val FILE: Icon = IconLoader.getIcon("/icons/dang.svg", DangIcons::class.java)
}
