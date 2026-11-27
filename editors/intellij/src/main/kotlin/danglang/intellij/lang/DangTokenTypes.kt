package danglang.intellij.lang

import com.intellij.psi.TokenType
import com.intellij.psi.tree.IElementType
import com.intellij.psi.tree.TokenSet

class DangTokenType(debugName: String) : IElementType(debugName, DangLanguage)

object DangTokenTypes {
    @JvmField val WHITE_SPACE: IElementType = TokenType.WHITE_SPACE
    @JvmField val BAD_CHARACTER: IElementType = TokenType.BAD_CHARACTER

    @JvmField val COMMENT = DangTokenType("COMMENT")

    /** String delimiters and literal content, including heredoc-style tags. */
    @JvmField val STRING = DangTokenType("STRING")
    @JvmField val STRING_ESCAPE = DangTokenType("STRING_ESCAPE")
    @JvmField val INVALID_ESCAPE = DangTokenType("INVALID_ESCAPE")
    @JvmField val INTERPOLATION_START = DangTokenType("INTERPOLATION_START") // ${
    @JvmField val INTERPOLATION_END = DangTokenType("INTERPOLATION_END") // }

    @JvmField val NUMBER = DangTokenType("NUMBER")
    @JvmField val KEYWORD = DangTokenType("KEYWORD")
    @JvmField val DECLARATION_KEYWORD = DangTokenType("DECLARATION_KEYWORD") // let, pub
    @JvmField val CONSTANT = DangTokenType("CONSTANT") // true, false, null
    @JvmField val SELF = DangTokenType("SELF")
    @JvmField val BUILTIN = DangTokenType("BUILTIN")
    @JvmField val TYPE_NAME = DangTokenType("TYPE_NAME")
    @JvmField val IDENTIFIER = DangTokenType("IDENTIFIER")
    @JvmField val DIRECTIVE = DangTokenType("DIRECTIVE") // @name

    @JvmField val OPERATOR = DangTokenType("OPERATOR")
    @JvmField val LPAREN = DangTokenType("LPAREN")
    @JvmField val RPAREN = DangTokenType("RPAREN")
    @JvmField val LBRACKET = DangTokenType("LBRACKET")
    @JvmField val RBRACKET = DangTokenType("RBRACKET")
    @JvmField val LBRACE = DangTokenType("LBRACE")
    @JvmField val RBRACE = DangTokenType("RBRACE")
    @JvmField val COMMA = DangTokenType("COMMA")
    @JvmField val DOT = DangTokenType("DOT")
    @JvmField val SEMICOLON = DangTokenType("SEMICOLON")
    @JvmField val PIPE = DangTokenType("PIPE")

    @JvmField val COMMENTS = TokenSet.create(COMMENT)
    @JvmField val STRINGS = TokenSet.create(STRING)
    @JvmField val WHITE_SPACES = TokenSet.create(WHITE_SPACE)
}
