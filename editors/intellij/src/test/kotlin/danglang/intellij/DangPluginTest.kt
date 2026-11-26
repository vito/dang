package danglang.intellij

import com.intellij.openapi.actionSystem.ActionManager
import com.intellij.openapi.actionSystem.ex.ActionUtil
import com.intellij.openapi.actionSystem.impl.SimpleDataContext
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.editor.ex.EditorEx
import com.intellij.openapi.module.ModuleTypeManager
import com.intellij.platform.lsp.api.LspClient
import com.intellij.platform.lsp.api.LspClientManager
import com.intellij.platform.lsp.api.LspServerState
import com.intellij.platform.lsp.api.customization.LspCodeActionsSupport
import com.intellij.platform.lsp.api.customization.LspDocumentHighlightsSupport
import com.intellij.platform.lsp.api.customization.LspRenameSupport
import com.intellij.testFramework.PlatformTestUtil
import com.intellij.testFramework.PsiTestUtil
import com.intellij.testFramework.TestActionEvent
import com.intellij.testFramework.UsefulTestCase
import com.intellij.testFramework.fixtures.CodeInsightTestFixture
import com.intellij.testFramework.fixtures.IdeaTestFixtureFactory
import com.intellij.testFramework.fixtures.impl.TempDirTestFixtureImpl
import danglang.intellij.lang.DangColors
import danglang.intellij.lang.DangFile
import danglang.intellij.lang.DangFileType
import danglang.intellij.lsp.DangLspIntegrationProvider
import danglang.intellij.settings.DangSettings
import org.eclipse.lsp4j.CompletionParams
import org.eclipse.lsp4j.HoverParams
import org.eclipse.lsp4j.Position

class DangPluginTest : UsefulTestCase() {
    private lateinit var fixture: CodeInsightTestFixture

    override fun setUp() {
        super.setUp()
        // A heavy fixture on a real temp dir, so files have file:// URIs the
        // language server can read.
        val builder = IdeaTestFixtureFactory.getFixtureFactory().createFixtureBuilder(name)
        fixture = IdeaTestFixtureFactory.getFixtureFactory()
            .createCodeInsightFixture(builder.fixture, TempDirTestFixtureImpl())
        fixture.setUp()
        // The LSP client only serves files under the project's content roots.
        PsiTestUtil.addModule(
            fixture.project,
            ModuleTypeManager.getInstance().defaultModuleType,
            "dang",
            fixture.tempDirFixture.getFile("")!!,
        )
    }

    override fun tearDown() {
        try {
            LspClientManager.getInstance(fixture.project).stopClients(DangLspIntegrationProvider::class.java)
            fixture.tearDown()
        } catch (e: Throwable) {
            addSuppressedException(e)
        } finally {
            super.tearDown()
        }
    }

    private val source = """
        pub greeting: String! = "hello"

        pub shout(msg: String!): String! {
          msg + "!"
        }

        print(shout(greeting))
    """.trimIndent() + "\n"

    fun testFileTypeAndHighlighting() {
        val file = fixture.configureByText("main.dang", source).virtualFile
        assertEquals(DangFileType, file.fileType)
        assertInstanceOf(fixture.file, DangFile::class.java)
        assertHighlighted("pub", DangColors.KEYWORD)
        assertHighlighted("\"hello\"", DangColors.STRING)
        assertHighlighted("String!", DangColors.TYPE_NAME)
    }

    fun testCommentToggling() {
        fixture.configureByText("main.dang", "<caret>let x = 1\n")
        fixture.performEditorAction("CommentByLineComment")
        fixture.checkResult("# let x = 1\n")
    }

    fun testEnterAfterBraceClosesAndIndents() {
        fixture.configureByText("main.dang", "pub f(): Int! {<caret>")
        fixture.type("\n")
        fixture.checkResult("pub f(): Int! {\n  <caret>\n}")
    }


    /** Checks the editor's actual highlighter, not just the lexer. */
    private fun assertHighlighted(text: String, key: TextAttributesKey) {
        val editor = fixture.editor as EditorEx
        val iterator = editor.highlighter.createIterator(editor.document.text.indexOf(text))
        assertTrue(
            "'$text' should be highlighted as $key, got ${iterator.textAttributesKeys.toList()} (${iterator.tokenType})",
            key in iterator.textAttributesKeys,
        )
    }

    fun testLanguageServer() {
        val binary = System.getProperty("dang.binary")
        if (binary.isNullOrBlank()) {
            println("DANG_BINARY not set; skipping language server test")
            return
        }
        DangSettings.getInstance().state.dangPath = binary

        val file = fixture.configureByText("main.dang", source).virtualFile

        val client = waitForClient()
        val caps = client.initializeResult!!.capabilities

        // Features the platform only enables for plain-text/TextMate files by
        // default must be opted in for the Dang language.
        val customization = client.descriptor.lspCustomization
        val psiFile = fixture.file
        assertTrue((customization.renameCustomizer as LspRenameSupport).shouldRunRename(psiFile))
        assertTrue((customization.codeActionsCustomizer as LspCodeActionsSupport).shouldRunRefactoring(psiFile))
        assertTrue(
            (customization.documentHighlightsCustomizer as LspDocumentHighlightsSupport).shouldAskServerForDocumentHighlights(psiFile),
        )
        assertNotNull(caps.hoverProvider)
        assertNotNull(caps.completionProvider)

        val doc = client.getDocumentIdentifier(file)
        // Hover over `shout` in `print(shout(greeting))` (line 6, col 7).
        val hover = client.sendRequestSync { it.textDocumentService.hover(HoverParams(doc, Position(6, 7))) }
        val hoverText = hover?.contents?.right?.value.orEmpty()
        assertTrue("unexpected hover: $hover", "shout" in hoverText && "String!" in hoverText)

        val completion = client.sendRequestSync {
            it.textDocumentService.completion(CompletionParams(doc, Position(6, 6)))
        }
        val labels = completion?.let { if (it.isLeft) it.left else it.right.items }.orEmpty().map { it.label }
        assertTrue("expected 'shout' in completions: $labels", "shout" in labels)

        // Through the IDE's own completion, which goes via the LSP client.
        fixture.editor.caretModel.moveToOffset(source.indexOf("shout(greeting)") + 2)
        val lookups = fixture.completeBasic()?.map { it.lookupString }.orEmpty()
        assertTrue("expected 'shout' in IDE completion: $lookups", lookups.any { it.startsWith("shout") })

        // Tools | Restart Dang Language Server brings up a fresh server.
        val action = ActionManager.getInstance().getAction("danglang.RestartLanguageServer")
        assertNotNull("restart action registered", action)
        val event = TestActionEvent.createTestEvent(action, SimpleDataContext.getProjectContext(fixture.project))
        ActionUtil.updateAction(action, event)
        assertTrue(event.presentation.isEnabledAndVisible)
        ActionUtil.performAction(action, event)
        val manager = LspClientManager.getInstance(fixture.project)
        val deadline = System.currentTimeMillis() + 60_000
        while (manager.getClients(DangLspIntegrationProvider::class.java).none { it !== client && it.state == LspServerState.Running }) {
            if (System.currentTimeMillis() > deadline) fail("language server was not restarted")
            PlatformTestUtil.dispatchAllEventsInIdeEventQueue()
            Thread.sleep(100)
        }
    }

    private fun waitForClient(): LspClient {
        val manager = LspClientManager.getInstance(fixture.project)
        // The test FileEditorManager doesn't fire the events that normally
        // trigger LspIntegrationProvider.fileOpened.
        manager.startClientsIfNeeded(DangLspIntegrationProvider::class.java)
        val deadline = System.currentTimeMillis() + 60_000
        while (System.currentTimeMillis() < deadline) {
            PlatformTestUtil.dispatchAllEventsInIdeEventQueue()
            manager.getClients(DangLspIntegrationProvider::class.java).firstOrNull()?.let {
                if (it.state == LspServerState.Running) return it
                if (it.state == LspServerState.ShutdownUnexpectedly) fail("language server exited unexpectedly")
            }
            Thread.sleep(100)
        }
        fail("language server did not start: ${manager.getClients(DangLspIntegrationProvider::class.java)}")
        error("unreachable")
    }
}
