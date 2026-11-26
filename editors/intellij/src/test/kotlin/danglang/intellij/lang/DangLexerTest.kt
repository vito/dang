package danglang.intellij.lang

import com.google.gson.JsonParser
import com.intellij.psi.tree.IElementType
import junit.framework.TestCase
import java.io.File
import danglang.intellij.lang.DangTokenTypes as T

class DangLexerTest : TestCase() {
    private data class Token(val type: IElementType, val text: String, val start: Int, val state: Int)

    private fun lex(text: String, start: Int = 0, state: Int = 0): List<Token> {
        val lexer = DangLexer()
        lexer.start(text, start, text.length, state)
        return buildList {
            while (lexer.tokenType != null) {
                add(Token(lexer.tokenType!!, text.substring(lexer.tokenStart, lexer.tokenEnd), lexer.tokenStart, lexer.state))
                lexer.advance()
            }
        }
    }

    private fun tokens(text: String) = lex(text).filter { it.type != T.WHITE_SPACE }.map { it.type to it.text }

    fun testDeclarations() {
        assertEquals(
            listOf(
                T.DECLARATION_KEYWORD to "pub", T.IDENTIFIER to "shout", T.LPAREN to "(", T.IDENTIFIER to "msg",
                T.OPERATOR to ":", T.TYPE_NAME to "String", T.OPERATOR to "!", T.RPAREN to ")", T.OPERATOR to ":",
                T.TYPE_NAME to "String", T.OPERATOR to "!", T.LBRACE to "{", T.IDENTIFIER to "msg",
                T.OPERATOR to "+", T.STRING to "\"", T.STRING to "!", T.STRING to "\"", T.RBRACE to "}",
            ),
            tokens("pub shout(msg: String!): String! { msg + \"!\" }"),
        )
    }

    fun testWordsAndMisc() {
        assertEquals(
            listOf(
                T.KEYWORD to "if", T.CONSTANT to "true", T.KEYWORD to "or", T.CONSTANT to "null", T.SELF to "self",
                T.DOT to ".", T.IDENTIFIER to "x", T.BUILTIN to "print", T.NUMBER to "42", T.DIRECTIVE to "@deprecated",
                T.OPERATOR to "??", T.OPERATOR to "=>", T.OPERATOR to "==", T.PIPE to "|", T.COMMENT to "# hi",
            ),
            tokens("if true or null self.x print 42 @deprecated ?? => == | # hi"),
        )
    }

    fun testEscapes() {
        assertEquals(
            listOf(
                T.STRING to "\"", T.STRING to "a", T.STRING_ESCAPE to "\\n", T.STRING_ESCAPE to "\\u00e9",
                T.INVALID_ESCAPE to "\\q", T.STRING to "\"",
            ),
            tokens("\"a\\n\\u00e9\\q\""),
        )
    }

    fun testNestedInterpolation() {
        assertEquals(
            listOf(
                T.STRING to "\"", T.STRING to "a ", T.INTERPOLATION_START to "\${", T.IDENTIFIER to "f",
                T.LPAREN to "(", T.LBRACE to "{", T.IDENTIFIER to "k", T.OPERATOR to ":", T.STRING to "\"",
                T.STRING to "x", T.INTERPOLATION_START to "\${", T.NUMBER to "1", T.INTERPOLATION_END to "}",
                T.STRING to "\"", T.RBRACE to "}", T.RPAREN to ")", T.INTERPOLATION_END to "}", T.STRING to " b",
                T.STRING to "\"", T.IDENTIFIER to "after",
            ),
            tokens("\"a \${f({k: \"x\${1}\"})} b\" after"),
        )
    }

    fun testUnterminatedStringEndsAtLineEnd() {
        assertEquals(
            listOf(T.STRING to "\"", T.STRING to "oops", T.KEYWORD to "return"),
            tokens("\"oops\nreturn"),
        )
    }

    fun testMultiLineStrings() {
        assertEquals(
            listOf(T.STRING to "\"\"\"sh", T.STRING to "\necho ", T.INTERPOLATION_START to "\${", T.IDENTIFIER to "x",
                T.INTERPOLATION_END to "}", T.STRING to "\n", T.STRING to "\"\"\"", T.IDENTIFIER to "y"),
            tokens("\"\"\"sh\necho \${x}\n\"\"\" y"),
        )
        // ''' has no interpolation or escapes.
        assertEquals(
            listOf(T.STRING to "'''", T.STRING to "\${x} \\n", T.STRING to "'''"),
            tokens("'''\${x} \\n'''"),
        )
        // A fence closes only on the same number of backticks; \${ is literal.
        assertEquals(
            listOf(T.STRING to "````md", T.STRING to "\n```\n", T.STRING to "\\\${no} ",
                T.INTERPOLATION_START to "\${", T.IDENTIFIER to "yes", T.INTERPOLATION_END to "}", T.STRING to "\n",
                T.STRING to "````"),
            tokens("````md\n```\n\\\${no} \${yes}\n````"),
        )
        assertEquals(listOf(T.STRING to "'single'"), tokens("'single'"))
    }

    /**
     * The editor re-lexes from the start of any token using the state saved
     * there; that has to produce exactly the same tokens as lexing from the top.
     */
    fun testRestartFromAnyTokenGivesSameTokens() {
        val text = """
            pub f(x: String!): String! {
              let s = "a ${'$'}{x + "b ${'$'}{ {k: 1} }"} \n"
              ""${'"'}sh
              echo ${'$'}{s}
              ""${'"'}
              ```md
              `nested` ${'$'}{ `t ${'$'}{x}` }
              ```
              '''raw ${'$'}{x}'''
              # done
            }
        """.trimIndent()
        val full = lex(text)
        assertEquals(text, full.joinToString("") { it.text })
        for ((i, token) in full.withIndex()) {
            assertEquals("restart at token $i (${token.type} '${token.text}')", full.drop(i), lex(text, token.start, token.state))
        }
    }

    fun testWordListsMatchTextMateGrammar() {
        val path = System.getProperty("dang.tmLanguage") ?: return println("dang.tmLanguage not set; skipping")
        val repo = JsonParser.parseString(File(path).readText()).asJsonObject.getAsJsonObject("repository")
        fun words(rule: String): Set<String> {
            val match = repo.getAsJsonObject(rule).get("match").asString
            val alternation = Regex("""\\b\((.*)\)\\b""").find(match)?.groupValues?.get(1) ?: error("unexpected regex for $rule: $match")
            return alternation.split('|').toSet()
        }
        assertEquals(words("keyword-control"), DangLexer.KEYWORDS)
        assertEquals(words("keyword"), DangLexer.DECLARATION_KEYWORDS)
        assertEquals(words("boolean") + words("null"), DangLexer.CONSTANTS)
        assertEquals(words("builtin-function"), DangLexer.BUILTINS)
        assertEquals(words("self"), setOf("self"))
    }
}
