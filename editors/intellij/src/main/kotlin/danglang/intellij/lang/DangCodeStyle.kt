package danglang.intellij.lang

import com.intellij.application.options.IndentOptionsEditor
import com.intellij.application.options.SmartIndentOptionsEditor
import com.intellij.lang.Language
import com.intellij.psi.codeStyle.CodeStyleSettingsCustomizable
import com.intellij.psi.codeStyle.CommonCodeStyleSettings
import com.intellij.psi.codeStyle.LanguageCodeStyleSettingsProvider
import com.intellij.psi.impl.source.codeStyle.SemanticEditorPosition.SyntaxElement
import com.intellij.psi.impl.source.codeStyle.lineIndent.JavaLikeLangLineIndentProvider
import com.intellij.psi.impl.source.codeStyle.lineIndent.JavaLikeLangLineIndentProvider.JavaLikeElement
import com.intellij.psi.tree.IElementType
import danglang.intellij.lang.DangTokenTypes as T

/** Indents on Enter from the tokens around the caret: inside {}, (), []. */
class DangLineIndentProvider : JavaLikeLangLineIndentProvider() {
    override fun isSuitableForLanguage(language: Language) = language.isKindOf(DangLanguage)

    override fun mapType(tokenType: IElementType): SyntaxElement? = MAPPING[tokenType]

    private companion object {
        val MAPPING: Map<IElementType, SyntaxElement> = mapOf(
            T.WHITE_SPACE to JavaLikeElement.Whitespace,
            T.COMMENT to JavaLikeElement.LineComment,
            T.LBRACE to JavaLikeElement.BlockOpeningBrace,
            T.RBRACE to JavaLikeElement.BlockClosingBrace,
            T.LPAREN to JavaLikeElement.LeftParenthesis,
            T.RPAREN to JavaLikeElement.RightParenthesis,
            T.LBRACKET to JavaLikeElement.ArrayOpeningBracket,
            T.RBRACKET to JavaLikeElement.ArrayClosingBracket,
            T.COMMA to JavaLikeElement.Comma,
            T.SEMICOLON to JavaLikeElement.Semicolon,
        )
    }
}

/** Indentation settings (2 spaces by default, like `dang fmt`). */
class DangCodeStyleSettingsProvider : LanguageCodeStyleSettingsProvider() {
    override fun getLanguage() = DangLanguage

    override fun customizeDefaults(commonSettings: CommonCodeStyleSettings, indentOptions: CommonCodeStyleSettings.IndentOptions) {
        indentOptions.INDENT_SIZE = 2
        indentOptions.CONTINUATION_INDENT_SIZE = 2
        indentOptions.TAB_SIZE = 2
    }

    override fun getIndentOptionsEditor(): IndentOptionsEditor = SmartIndentOptionsEditor()

    override fun customizeSettings(consumer: CodeStyleSettingsCustomizable, settingsType: SettingsType) {}

    override fun getCodeSample(settingsType: SettingsType) = """
        pub greet(name: String!): String! {
          if (name == "") {
            raise "who?"
          }
          "hello, ${'$'}{name}!"
        }
    """.trimIndent()
}
