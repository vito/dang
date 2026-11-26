package danglang.intellij.lang

import com.intellij.lexer.LexerBase
import com.intellij.psi.tree.IElementType
import danglang.intellij.lang.DangTokenTypes as T

/**
 * Highlighting lexer for Dang, mirroring the VS Code TextMate grammar
 * (editors/vscode/syntaxes/dang.tmLanguage.json).
 *
 * Strings can contain `${...}` interpolations, which contain code, which can
 * contain strings, so the lexer keeps a small stack of modes. The editor
 * restarts lexing from arbitrary token boundaries using the state int, so the
 * whole stack is packed into it: up to [MAX_DEPTH] frames of 6 bits each
 * (3-bit kind, 3-bit parameter). Deeper nesting is clamped, which only
 * affects highlighting of absurdly nested code.
 */
class DangLexer : LexerBase() {
    private var buffer: CharSequence = ""
    private var bufferEnd = 0
    private var tokenStart = 0
    private var tokenEnd = 0
    private var tokenType: IElementType? = null
    private var tokenStartState = 0
    private var state = 0 // state after the current token

    override fun start(buffer: CharSequence, startOffset: Int, endOffset: Int, initialState: Int) {
        this.buffer = buffer
        this.bufferEnd = endOffset
        this.tokenEnd = startOffset
        this.state = initialState
        advance()
    }

    override fun getState() = tokenStartState
    override fun getTokenType() = tokenType
    override fun getTokenStart() = tokenStart
    override fun getTokenEnd() = tokenEnd
    override fun getBufferSequence() = buffer
    override fun getBufferEnd() = bufferEnd

    override fun advance() {
        tokenStart = tokenEnd
        tokenStartState = state
        if (tokenStart >= bufferEnd) {
            tokenType = null
            return
        }
        val stack = Stack(state)
        val top = stack.top()
        tokenType = when (top.kind) {
            DOUBLE, TRIPLE_DOUBLE, TRIPLE_SINGLE, BACKTICK, FENCE -> lexStringContent(stack, top)
            else -> lexCode(stack, top)
        }
        state = stack.encode()
    }

    // --- code ------------------------------------------------------------

    private fun lexCode(stack: Stack, top: Frame): IElementType {
        var i = tokenStart
        val c = buffer[i]
        when {
            c.isWhitespace() -> {
                while (i < bufferEnd && buffer[i].isWhitespace()) i++
                return token(i, T.WHITE_SPACE)
            }
            c == '#' -> {
                while (i < bufferEnd && buffer[i] != '\n') i++
                return token(i, T.COMMENT)
            }
            startsWith(i, "\"\"\"") -> {
                stack.push(Frame(TRIPLE_DOUBLE, 0))
                return token(skipLineTag(i + 3), T.STRING)
            }
            startsWith(i, "'''") -> {
                stack.push(Frame(TRIPLE_SINGLE, 0))
                return token(skipLineTag(i + 3), T.STRING)
            }
            startsWith(i, "```") -> {
                var n = 0
                while (i + n < bufferEnd && buffer[i + n] == '`') n++
                stack.push(Frame(FENCE, (n - 3).coerceAtMost(MAX_PARAM)))
                return token(skipTag(i + n), T.STRING)
            }
            c == '`' -> {
                stack.push(Frame(BACKTICK, 0))
                return token(i + 1, T.STRING)
            }
            c == '"' -> {
                stack.push(Frame(DOUBLE, 0))
                return token(i + 1, T.STRING)
            }
            c == '\'' -> {
                // Single-quoted strings are single-line, with no escapes or
                // interpolation.
                i++
                while (i < bufferEnd && buffer[i] != '\'' && buffer[i] != '\n') i++
                if (i < bufferEnd && buffer[i] == '\'') i++
                return token(i, T.STRING)
            }
            c.isDigit() -> {
                while (i < bufferEnd && buffer[i].isDigit()) i++
                return token(i, T.NUMBER)
            }
            c == '@' && i + 1 < bufferEnd && buffer[i + 1].isIdentStart() -> {
                i++
                while (i < bufferEnd && buffer[i].isIdentPart()) i++
                return token(i, T.DIRECTIVE)
            }
            c.isIdentStart() -> {
                while (i < bufferEnd && buffer[i].isIdentPart()) i++
                return token(i, classifyWord(buffer.subSequence(tokenStart, i).toString()))
            }
            c == '{' -> {
                if (top.kind == INTERPOLATION) stack.replaceTop(Frame(INTERPOLATION, (top.param + 1).coerceAtMost(MAX_PARAM)))
                return token(i + 1, T.LBRACE)
            }
            c == '}' -> {
                if (top.kind == INTERPOLATION) {
                    if (top.param == 0) {
                        stack.pop()
                        return token(i + 1, T.INTERPOLATION_END)
                    }
                    stack.replaceTop(Frame(INTERPOLATION, top.param - 1))
                }
                return token(i + 1, T.RBRACE)
            }
            c == '(' -> return token(i + 1, T.LPAREN)
            c == ')' -> return token(i + 1, T.RPAREN)
            c == '[' -> return token(i + 1, T.LBRACKET)
            c == ']' -> return token(i + 1, T.RBRACKET)
            c == ',' -> return token(i + 1, T.COMMA)
            c == '.' -> return token(i + 1, T.DOT)
            c == ';' -> return token(i + 1, T.SEMICOLON)
            c == '|' -> return token(i + 1, T.PIPE)
        }
        for (op in OPERATORS) {
            if (startsWith(i, op)) return token(i + op.length, T.OPERATOR)
        }
        return token(i + 1, T.BAD_CHARACTER)
    }

    private fun classifyWord(word: String): IElementType = when {
        word in KEYWORDS -> T.KEYWORD
        word in DECLARATION_KEYWORDS -> T.DECLARATION_KEYWORD
        word in CONSTANTS -> T.CONSTANT
        word == "self" -> T.SELF
        word in BUILTINS -> T.BUILTIN
        word[0].isUpperCase() -> T.TYPE_NAME
        else -> T.IDENTIFIER
    }

    // --- strings ---------------------------------------------------------

    private fun lexStringContent(stack: Stack, top: Frame): IElementType {
        var i = tokenStart
        val kind = top.kind

        // Closing delimiter.
        val closer = closer(top)
        if (startsWith(i, closer)) {
            stack.pop()
            return token(i + closer.length, T.STRING)
        }
        // Interpolation.
        if (kind != TRIPLE_SINGLE && startsWith(i, "\${")) {
            stack.push(Frame(INTERPOLATION, 0))
            return token(i + 2, T.INTERPOLATION_START)
        }
        if (kind == DOUBLE && buffer[i] == '\n') {
            // Unterminated: double-quoted strings end at the line end.
            stack.pop()
            return lexCode(stack, stack.top())
        }
        // Escapes, in double-quoted strings only.
        if ((kind == DOUBLE || kind == TRIPLE_DOUBLE) && buffer[i] == '\\') {
            val escapeEnd = matchEscape(i)
            if (escapeEnd > 0) return token(escapeEnd, T.STRING_ESCAPE)
            if (kind == DOUBLE && i + 1 < bufferEnd && buffer[i + 1] != '\n') return token(i + 2, T.INVALID_ESCAPE)
        }
        // Plain content up to the next interesting character.
        i++
        if ((kind == BACKTICK || kind == FENCE) && buffer[i - 1] == '\\' && startsWith(i, "\${")) i += 2 // \${ is literal
        while (i < bufferEnd) {
            val c = buffer[i]
            if (startsWith(i, closer)) break
            if (kind != TRIPLE_SINGLE && c == '$' && i + 1 < bufferEnd && buffer[i + 1] == '{') break
            if (kind == DOUBLE && c == '\n') break
            if ((kind == DOUBLE || kind == TRIPLE_DOUBLE) && c == '\\') break
            if ((kind == BACKTICK || kind == FENCE) && c == '\\') break
            i++
        }
        return token(i, T.STRING)
    }

    private fun closer(frame: Frame): String = when (frame.kind) {
        DOUBLE -> "\""
        TRIPLE_DOUBLE -> "\"\"\""
        TRIPLE_SINGLE -> "'''"
        BACKTICK -> "`"
        FENCE -> "`".repeat(frame.param + 3)
        else -> error("not a string frame: $frame")
    }

    /** Matches the grammar's string-escape; returns the end offset, or -1. */
    private fun matchEscape(i: Int): Int {
        if (i + 1 >= bufferEnd) return -1
        val c = buffer[i + 1]
        if (c in "abfnrtv\"\\/$") return i + 2
        fun hex(count: Int) = if (i + 2 + count <= bufferEnd && (0 until count).all { buffer[i + 2 + it].isHexDigit() }) i + 2 + count else -1
        return when (c) {
            'x' -> hex(2)
            'u' -> hex(4)
            'U' -> hex(8)
            in '0'..'7' -> if (i + 4 <= bufferEnd && (1..3).all { buffer[i + it] in '0'..'7' }) i + 4 else -1
            else -> -1
        }
    }

    /** A heredoc tag after a triple quote counts only if it ends the line. */
    private fun skipLineTag(i: Int): Int {
        val end = skipTag(i)
        return if (end > i && (end == bufferEnd || buffer[end] == '\n')) end else i
    }

    private fun skipTag(i: Int): Int {
        if (i >= bufferEnd || !buffer[i].isLetter()) return i
        var j = i + 1
        while (j < bufferEnd && (buffer[j].isLetterOrDigit() || buffer[j] == '_' || buffer[j] == '-')) j++
        return j
    }

    // --- helpers ---------------------------------------------------------

    private fun token(end: Int, type: IElementType): IElementType {
        tokenEnd = end
        return type
    }

    private fun startsWith(i: Int, s: String): Boolean {
        if (i + s.length > bufferEnd) return false
        for (k in s.indices) if (buffer[i + k] != s[k]) return false
        return true
    }

    private fun Char.isIdentStart() = this == '_' || this in 'a'..'z' || this in 'A'..'Z'
    private fun Char.isIdentPart() = isIdentStart() || this in '0'..'9'
    private fun Char.isHexDigit() = this in '0'..'9' || this in 'a'..'f' || this in 'A'..'F'

    private data class Frame(val kind: Int, val param: Int)

    private class Stack(encoded: Int) {
        private val frames = ArrayList<Frame>(MAX_DEPTH)

        // Bottom frame in the lowest bits. Every frame kind is non-zero, so
        // an empty stack encodes as 0, the initial state.
        init {
            var s = encoded
            while (s != 0) {
                frames.add(Frame(s and 7, (s shr 3) and 7))
                s = s ushr 6
            }
        }

        fun top() = frames.lastOrNull() ?: Frame(CODE, 0)
        fun push(frame: Frame) { if (frames.size < MAX_DEPTH) frames.add(frame) }
        fun pop() { if (frames.isNotEmpty()) frames.removeAt(frames.lastIndex) }
        fun replaceTop(frame: Frame) { if (frames.isNotEmpty()) frames[frames.lastIndex] = frame }

        fun encode(): Int {
            var s = 0
            for (frame in frames.asReversed()) s = (s shl 6) or (frame.param shl 3) or frame.kind
            return s
        }
    }

    companion object {
        private const val CODE = 0
        private const val DOUBLE = 1
        private const val TRIPLE_DOUBLE = 2
        private const val TRIPLE_SINGLE = 3
        private const val BACKTICK = 4
        private const val FENCE = 5 // ``` (param = backtick count - 3)
        private const val INTERPOLATION = 6 // ${ (param = open brace depth)
        private const val MAX_PARAM = 7
        private const val MAX_DEPTH = 5

        // Keep in sync with dang.tmLanguage.json (checked by a test).
        val KEYWORDS = setOf(
            "type", "interface", "union", "implements", "enum", "scalar", "if", "else", "break", "continue",
            "case", "assert", "directive", "on", "import", "new", "and", "or", "rescue", "raise", "return",
        )
        val DECLARATION_KEYWORDS = setOf("let", "pub")
        val CONSTANTS = setOf("true", "false", "null")
        val BUILTINS = setOf("print", "loop")
        val OPERATORS = listOf("==", "!=", "<=", ">=", "+=", "??", "=>", "<", ">", "=", "!", "&", "+", "-", "*", "/", "%", ":", "?")
    }
}
