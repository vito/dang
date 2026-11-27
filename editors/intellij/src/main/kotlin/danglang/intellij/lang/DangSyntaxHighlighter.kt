package danglang.intellij.lang

import com.intellij.openapi.editor.DefaultLanguageHighlighterColors as D
import com.intellij.openapi.editor.HighlighterColors
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.editor.colors.TextAttributesKey.createTextAttributesKey
import com.intellij.openapi.fileTypes.SyntaxHighlighter
import com.intellij.openapi.fileTypes.SyntaxHighlighterBase
import com.intellij.openapi.fileTypes.SyntaxHighlighterFactory
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.psi.tree.IElementType
import danglang.intellij.lang.DangTokenTypes as T

object DangColors {
    val KEYWORD = createTextAttributesKey("DANG_KEYWORD", D.KEYWORD)
    val CONSTANT = createTextAttributesKey("DANG_CONSTANT", D.KEYWORD)
    val SELF = createTextAttributesKey("DANG_SELF", D.KEYWORD)
    val NUMBER = createTextAttributesKey("DANG_NUMBER", D.NUMBER)
    val STRING = createTextAttributesKey("DANG_STRING", D.STRING)
    val STRING_ESCAPE = createTextAttributesKey("DANG_STRING_ESCAPE", D.VALID_STRING_ESCAPE)
    val INVALID_ESCAPE = createTextAttributesKey("DANG_INVALID_ESCAPE", D.INVALID_STRING_ESCAPE)
    val INTERPOLATION = createTextAttributesKey("DANG_INTERPOLATION", D.KEYWORD)
    val COMMENT = createTextAttributesKey("DANG_COMMENT", D.LINE_COMMENT)
    val TYPE_NAME = createTextAttributesKey("DANG_TYPE_NAME", D.CLASS_NAME)
    val BUILTIN = createTextAttributesKey("DANG_BUILTIN", D.PREDEFINED_SYMBOL)
    val DIRECTIVE = createTextAttributesKey("DANG_DIRECTIVE", D.METADATA)
    val IDENTIFIER = createTextAttributesKey("DANG_IDENTIFIER", D.IDENTIFIER)
    val OPERATOR = createTextAttributesKey("DANG_OPERATOR", D.OPERATION_SIGN)
    val PARENTHESES = createTextAttributesKey("DANG_PARENTHESES", D.PARENTHESES)
    val BRACKETS = createTextAttributesKey("DANG_BRACKETS", D.BRACKETS)
    val BRACES = createTextAttributesKey("DANG_BRACES", D.BRACES)
    val COMMA = createTextAttributesKey("DANG_COMMA", D.COMMA)
    val DOT = createTextAttributesKey("DANG_DOT", D.DOT)
    val SEMICOLON = createTextAttributesKey("DANG_SEMICOLON", D.SEMICOLON)
    val BAD_CHARACTER = createTextAttributesKey("DANG_BAD_CHARACTER", HighlighterColors.BAD_CHARACTER)
}

class DangSyntaxHighlighter : SyntaxHighlighterBase() {
    override fun getHighlightingLexer() = DangLexer()

    override fun getTokenHighlights(tokenType: IElementType): Array<TextAttributesKey> =
        pack(ATTRIBUTES[tokenType])

    private companion object {
        val ATTRIBUTES: Map<IElementType, TextAttributesKey> = mapOf(
            T.KEYWORD to DangColors.KEYWORD,
            T.DECLARATION_KEYWORD to DangColors.KEYWORD,
            T.CONSTANT to DangColors.CONSTANT,
            T.SELF to DangColors.SELF,
            T.NUMBER to DangColors.NUMBER,
            T.STRING to DangColors.STRING,
            T.STRING_ESCAPE to DangColors.STRING_ESCAPE,
            T.INVALID_ESCAPE to DangColors.INVALID_ESCAPE,
            T.INTERPOLATION_START to DangColors.INTERPOLATION,
            T.INTERPOLATION_END to DangColors.INTERPOLATION,
            T.COMMENT to DangColors.COMMENT,
            T.TYPE_NAME to DangColors.TYPE_NAME,
            T.BUILTIN to DangColors.BUILTIN,
            T.DIRECTIVE to DangColors.DIRECTIVE,
            T.IDENTIFIER to DangColors.IDENTIFIER,
            T.OPERATOR to DangColors.OPERATOR,
            T.LPAREN to DangColors.PARENTHESES,
            T.RPAREN to DangColors.PARENTHESES,
            T.LBRACKET to DangColors.BRACKETS,
            T.RBRACKET to DangColors.BRACKETS,
            T.LBRACE to DangColors.BRACES,
            T.RBRACE to DangColors.BRACES,
            T.COMMA to DangColors.COMMA,
            T.DOT to DangColors.DOT,
            T.SEMICOLON to DangColors.SEMICOLON,
            T.PIPE to DangColors.OPERATOR,
            T.BAD_CHARACTER to DangColors.BAD_CHARACTER,
        )
    }
}

class DangSyntaxHighlighterFactory : SyntaxHighlighterFactory() {
    override fun getSyntaxHighlighter(project: Project?, virtualFile: VirtualFile?): SyntaxHighlighter =
        DangSyntaxHighlighter()
}
