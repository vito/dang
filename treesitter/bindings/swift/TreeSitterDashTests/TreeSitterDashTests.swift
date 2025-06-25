import XCTest
import SwiftTreeSitter
import TreeSitterDash

final class TreeSitterDashTests: XCTestCase {
    func testCanLoadGrammar() throws {
        let parser = Parser()
        let language = Language(language: tree_sitter_dash())
        XCTAssertNoThrow(try parser.setLanguage(language),
                         "Error loading Dash grammar")
    }
}
