import PackageDescription

let package = Package(
    name: "TreeSitterBass",
    platforms: [.macOS(.v10_13), .iOS(.v11)],
    products: [
        .library(name: "TreeSitterBass", targets: ["TreeSitterBass"]),
    ],
    dependencies: [],
    targets: [
        .target(name: "TreeSitterBass",
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
