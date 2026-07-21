// Renders a vector approximation of the Dcon app icon (macOS Big Sur+ style):
// deep slate→ocean gradient, soft isometric microVM cube, cyan terminal caret.
// Master artwork lives at app/Assets/AppIcon-1024.png; prefer packing that via
//   python3 app/scripts/generate_icon.py
// when regenerating AppIcon.icns. This script is a code-only fallback on macOS.
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

    // Background: deep ink → ocean blue (no hard glass split).
    let gradient = NSGradient(colors: [
        NSColor(calibratedRed: 0.04, green: 0.07, blue: 0.13, alpha: 1), // bottom
        NSColor(calibratedRed: 0.11, green: 0.37, blue: 0.66, alpha: 1), // top
    ])!
    path.setClip()
    gradient.draw(in: tile, angle: 90)

    // Soft top sheen (continuous, not a midline gloss band).
    let sheen = NSGradient(colors: [
        NSColor(calibratedWhite: 1, alpha: 0.16),
        NSColor(calibratedWhite: 1, alpha: 0),
    ])!
    sheen.draw(
        in: NSRect(x: tile.minX, y: tile.midY, width: tile.width, height: tile.height / 2),
        angle: 90
    )

    // Drop shadow under cube.
    let shadow = NSShadow()
    shadow.shadowColor = NSColor(calibratedRed: 0.01, green: 0.03, blue: 0.08, alpha: 0.55)
    shadow.shadowBlurRadius = size * 0.04
    shadow.shadowOffset = NSSize(width: 0, height: -size * 0.01)
    shadow.set()

    let cx = tile.midX
    let cy = tile.midY + size * 0.01
    let u = size * 0.175
    let v = size * 0.10
    let h = size * 0.235

    let T = NSPoint(x: cx, y: cy + h * 0.55)
    let L = NSPoint(x: cx - u, y: cy + h * 0.55 - v)
    let R = NSPoint(x: cx + u, y: cy + h * 0.55 - v)
    let M = NSPoint(x: cx, y: cy + h * 0.55 - 2 * v)
    let BL = NSPoint(x: cx - u, y: cy + h * 0.55 - v - h)
    let BR = NSPoint(x: cx + u, y: cy + h * 0.55 - v - h)
    let B = NSPoint(x: cx, y: cy + h * 0.55 - 2 * v - h)

    func fill(_ pts: [NSPoint], _ color: NSColor) {
        let p = NSBezierPath()
        p.move(to: pts[0])
        for pt in pts.dropFirst() { p.line(to: pt) }
        p.close()
        color.setFill()
        p.fill()
    }

    // Faces: top lightest, left mid, right darkest.
    fill([T, R, M, L], NSColor(calibratedRed: 0.96, green: 0.97, blue: 0.99, alpha: 1))
    // Clear shadow so faces stay crisp.
    NSShadow().set()
    fill([L, M, B, BL], NSColor(calibratedRed: 0.85, green: 0.89, blue: 0.94, alpha: 1))
    fill([M, R, BR, B], NSColor(calibratedRed: 0.72, green: 0.80, blue: 0.87, alpha: 1))

    // Cyan terminal caret on left face.
    let fx = (L.x + M.x + B.x + BL.x) / 4
    let fy = (L.y + M.y + B.y + BL.y) / 4
    let w = size * 0.045
    let hh = size * 0.075
    let t = size * 0.018
    let caret = NSBezierPath()
    caret.move(to: NSPoint(x: fx - w * 0.5, y: fy + hh))
    caret.line(to: NSPoint(x: fx + w * 0.8, y: fy))
    caret.line(to: NSPoint(x: fx - w * 0.5, y: fy - hh))
    caret.line(to: NSPoint(x: fx - w * 0.5 + t, y: fy - hh + t * 0.9))
    caret.line(to: NSPoint(x: fx + w * 0.35, y: fy))
    caret.line(to: NSPoint(x: fx - w * 0.5 + t, y: fy + hh - t * 0.9))
    caret.close()
    NSColor(calibratedRed: 0.37, green: 0.92, blue: 0.83, alpha: 1).setFill()
    caret.fill()

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
