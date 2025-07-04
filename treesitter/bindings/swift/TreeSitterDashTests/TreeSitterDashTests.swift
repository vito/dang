import XCTest
import SwiftTreeSitter
import TreeSitterBind

final class TreeSitterBindTests: XCTestCase {
    func testCanLoadGrammar() throws {
        let parser = Parser()
        let language = Language(language: tree_sitter_bind())
        XCTAssertNoThrow(try parser.setLanguage(language),
                         "Error loading Bind grammar")
    }
}
