// Renders the Dcon app icon: a macOS-style rounded square with a blue-slate
// gradient and the shippingbox symbol, exported as an .icns.
//
// Usage: swift app/scripts/generate-icon.swift app/Assets/AppIcon.icns
import AppKit

let outPath = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "AppIcon.icns"

func drawIcon(size: CGFloat) -> NSImage {
    let img = NSImage(size: NSSize(width: size, height: size))
    img.lockFocus()
    defer { img.unlockFocus() }

    // Apple icon-grid: the tile fills ~80% of the canvas, corner radius ~22.4%.
    let inset = size * 0.10
    let tile = NSRect(x: inset, y: inset, width: size - 2 * inset, height: size - 2 * inset)
    let radius = tile.width * 0.224
    let path = NSBezierPath(roundedRect: tile, xRadius: radius, yRadius: radius)

    // Background gradient: deep slate to blue.
    let gradient = NSGradient(colors: [
        NSColor(calibratedRed: 0.09, green: 0.14, blue: 0.24, alpha: 1),
        NSColor(calibratedRed: 0.13, green: 0.34, blue: 0.65, alpha: 1),
    ])!
    path.setClip()
    gradient.draw(in: tile, angle: 90)

    // Subtle top sheen.
    let sheen = NSGradient(colors: [
        NSColor(calibratedWhite: 1, alpha: 0.18),
        NSColor(calibratedWhite: 1, alpha: 0),
    ])!
    sheen.draw(in: NSRect(x: tile.minX, y: tile.midY, width: tile.width, height: tile.height / 2), angle: 90)

    // Glyph: shippingbox, white, centered.
    let config = NSImage.SymbolConfiguration(pointSize: size * 0.42, weight: .medium)
    if let symbol = NSImage(systemSymbolName: "shippingbox.fill", accessibilityDescription: nil)?
        .withSymbolConfiguration(config) {
        let tinted = NSImage(size: symbol.size)
        tinted.lockFocus()
        NSColor.white.set()
        let r = NSRect(origin: .zero, size: symbol.size)
        symbol.draw(in: r)
        r.fill(using: .sourceAtop)
        tinted.unlockFocus()

        let glyphW = tile.width * 0.56
        let glyphH = glyphW * (tinted.size.height / tinted.size.width)
        let glyphRect = NSRect(
            x: tile.midX - glyphW / 2,
            y: tile.midY - glyphH / 2,
            width: glyphW, height: glyphH
        )
        tinted.draw(in: glyphRect, from: .zero, operation: .sourceOver, fraction: 0.96)
    }

    return img
}

func pngData(_ image: NSImage, pixels: Int) -> Data {
    let rep = NSBitmapImageRep(
        bitmapDataPlanes: nil, pixelsWide: pixels, pixelsHigh: pixels,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0
    )!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    image.draw(in: NSRect(x: 0, y: 0, width: pixels, height: pixels))
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}

let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("DconIcon.iconset")
try? FileManager.default.removeItem(at: tmp)
try FileManager.default.createDirectory(at: tmp, withIntermediateDirectories: true)

let master = drawIcon(size: 1024)
for base in [16, 32, 128, 256, 512] {
    try pngData(master, pixels: base).write(to: tmp.appendingPathComponent("icon_\(base)x\(base).png"))
    try pngData(master, pixels: base * 2).write(to: tmp.appendingPathComponent("icon_\(base)x\(base)@2x.png"))
}

let task = Process()
task.executableURL = URL(fileURLWithPath: "/usr/bin/iconutil")
task.arguments = ["-c", "icns", tmp.path, "-o", outPath]
try task.run()
task.waitUntilExit()
try? FileManager.default.removeItem(at: tmp)
print(task.terminationStatus == 0 ? "wrote \(outPath)" : "iconutil failed")
exit(task.terminationStatus)
