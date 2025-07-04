import PackageDescription

let package = Package(
    name: "TreeSitterBind",
    platforms: [.macOS(.v10_13), .iOS(.v11)],
    products: [
        .library(name: "TreeSitterBind", targets: ["TreeSitterBind"]),
    ],
    dependencies: [],
    targets: [
        .target(name: "TreeSitterBind",
                path: ".",
                exclude: [
                    "binding.gyp",
                    "bindings",
                    "Cargo.toml",
                    "examples",
                    "test",
                    "grammar.js",
                    "LICENSE",
                    "package.json",
                    "README.md",
                    "script",
                    "src/grammar.json",
                    "src/node-types.json",
                ],
                sources: [
                    "src/parser.c",
                ],
                resources: [
                    .copy("queries")
                ],
                publicHeadersPath: "bindings/swift",
                cSettings: [.headerSearchPath("src")])
    ]
)
