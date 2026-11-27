package danglang.intellij.lang

import com.intellij.openapi.options.colors.AttributesDescriptor
import com.intellij.openapi.options.colors.ColorDescriptor
import com.intellij.openapi.options.colors.ColorSettingsPage

class DangColorSettingsPage : ColorSettingsPage {
    override fun getDisplayName() = "Dang"
    override fun getIcon() = DangIcons.FILE
    override fun getHighlighter() = DangSyntaxHighlighter()
    override fun getAdditionalHighlightingTagToDescriptorMap() = null
    override fun getColorDescriptors(): Array<ColorDescriptor> = ColorDescriptor.EMPTY_ARRAY
    override fun getAttributeDescriptors() = DESCRIPTORS

    override fun getDemoText() = """
        # Build and test a Go project
        @experimental
        type Builder {
          pub source: Directory!

          pub build(goos: String! = "linux"): Container! {
            let base = container.from("golang:1.25")
            base.withDirectory("/src", source)
              .withEnvVariable("GOOS", goos)
              .withExec(["go", "build", "-o", "/out/app", "./..."])
          }

          pub greet(name: String!): String! {
            if (name == "" or self.source == null) {
              raise "who?\n"
            }
            print("hello, ${'$'}{name}! \q")
            ""${'"'}
            Multi-line, ${'$'}{name}.
            ""${'"'}
          }
        }

        let retries = 3
        let ok = true
    """.trimIndent()

    private companion object {
        val DESCRIPTORS = arrayOf(
            AttributesDescriptor("Keyword", DangColors.KEYWORD),
            AttributesDescriptor("Constant (true, false, null)", DangColors.CONSTANT),
            AttributesDescriptor("self", DangColors.SELF),
            AttributesDescriptor("Number", DangColors.NUMBER),
            AttributesDescriptor("String//Text", DangColors.STRING),
            AttributesDescriptor("String//Escape sequence", DangColors.STRING_ESCAPE),
            AttributesDescriptor("String//Invalid escape sequence", DangColors.INVALID_ESCAPE),
            AttributesDescriptor("String//Interpolation braces", DangColors.INTERPOLATION),
            AttributesDescriptor("Comment", DangColors.COMMENT),
            AttributesDescriptor("Type name", DangColors.TYPE_NAME),
            AttributesDescriptor("Built-in function", DangColors.BUILTIN),
            AttributesDescriptor("Directive", DangColors.DIRECTIVE),
            AttributesDescriptor("Identifier", DangColors.IDENTIFIER),
            AttributesDescriptor("Operator", DangColors.OPERATOR),
            AttributesDescriptor("Braces and Operators//Parentheses", DangColors.PARENTHESES),
            AttributesDescriptor("Braces and Operators//Brackets", DangColors.BRACKETS),
            AttributesDescriptor("Braces and Operators//Braces", DangColors.BRACES),
            AttributesDescriptor("Braces and Operators//Comma", DangColors.COMMA),
            AttributesDescriptor("Braces and Operators//Dot", DangColors.DOT),
            AttributesDescriptor("Braces and Operators//Semicolon", DangColors.SEMICOLON),
            AttributesDescriptor("Bad character", DangColors.BAD_CHARACTER),
        )
    }
}
