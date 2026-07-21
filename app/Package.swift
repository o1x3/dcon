// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "DconDesktop",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "Dcon",
            path: "Sources/Dcon"
        ),
        .testTarget(
            name: "DconTests",
            dependencies: ["Dcon"],
            path: "Tests/DconTests"
        ),
    ]
)
