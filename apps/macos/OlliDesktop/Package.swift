// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "OlliDesktop",
    platforms: [.macOS(.v14)],
    products: [.executable(name: "OlliDesktop", targets: ["OlliDesktop"])],
    targets: [
        .executableTarget(
            name: "OlliDesktop",
            path: "Sources/OlliDesktop"
        )
    ]
)
