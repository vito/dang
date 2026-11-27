package danglang.intellij.lang

import com.intellij.extapi.psi.ASTWrapperPsiElement
import com.intellij.extapi.psi.PsiFileBase
import com.intellij.lang.ASTNode
import com.intellij.lang.ParserDefinition
import com.intellij.lang.PsiBuilder
import com.intellij.lang.PsiParser
import com.intellij.openapi.project.Project
import com.intellij.psi.FileViewProvider
import com.intellij.psi.PsiElement
import com.intellij.psi.tree.IElementType
import com.intellij.psi.tree.IFileElementType

class DangFile(viewProvider: FileViewProvider) : PsiFileBase(viewProvider, DangLanguage) {
    override fun getFileType() = DangFileType
    override fun toString() = "Dang file"
}

/**
 * A flat PSI: the file is a sequence of tokens. Semantics (completion,
 * navigation, references, diagnostics) come from the language server; the
 * PSI only needs to exist for the editor's lexer-based features (comments,
 * brace matching, word selection).
 */
class DangParserDefinition : ParserDefinition {
    override fun createLexer(project: Project?) = DangLexer()
    override fun createParser(project: Project?) = PsiParser { root: IElementType, builder: PsiBuilder ->
        val marker = builder.mark()
        while (!builder.eof()) builder.advanceLexer()
        marker.done(root)
        builder.treeBuilt
    }
    override fun getFileNodeType() = FILE
    override fun getWhitespaceTokens() = DangTokenTypes.WHITE_SPACES
    override fun getCommentTokens() = DangTokenTypes.COMMENTS
    override fun getStringLiteralElements() = DangTokenTypes.STRINGS
    override fun createElement(node: ASTNode): PsiElement = ASTWrapperPsiElement(node)
    override fun createFile(viewProvider: FileViewProvider) = DangFile(viewProvider)

    private companion object {
        val FILE = IFileElementType(DangLanguage)
    }
}
