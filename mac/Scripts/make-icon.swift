import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

// Draws the app icon: πορθμός, a strait — the narrow channel between two
// shores, and the crossing over it. Two headlands almost touch; a bright wake
// runs between them, left to right, the way the cursor does.
//
// Everything is proportional to `size` so the same drawing works at 16pt and
// 1024pt. Detail that would turn to mud at 16pt is deliberately absent.

let outputDirectory = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "."

func colour(_ r: Double, _ g: Double, _ b: Double, _ a: Double = 1) -> CGColor {
    CGColor(srgbRed: r / 255, green: g / 255, blue: b / 255, alpha: a)
}

/// macOS icons sit inside the canvas with a margin and a squircle-ish corner.
func iconPath(in rect: CGRect) -> CGPath {
    CGPath(roundedRect: rect, cornerWidth: rect.width * 0.2237,
           cornerHeight: rect.height * 0.2237, transform: nil)
}

func drawIcon(size: Double) -> CGImage? {
    let space = CGColorSpace(name: CGColorSpace.sRGB)!
    guard let ctx = CGContext(
        data: nil, width: Int(size), height: Int(size), bitsPerComponent: 8,
        bytesPerRow: 0, space: space,
        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
    ) else { return nil }

    ctx.interpolationQuality = .high
    ctx.setShouldAntialias(true)

    let margin = size * 0.094
    let body = CGRect(x: margin, y: margin, width: size - margin * 2, height: size - margin * 2)

    // Water: a deep sea gradient, lighter towards the top.
    ctx.saveGState()
    ctx.addPath(iconPath(in: body))
    ctx.clip()
    let sea = CGGradient(colorsSpace: space, colors: [
        colour(46, 150, 205), colour(20, 96, 150), colour(10, 56, 96),
    ] as CFArray, locations: [0, 0.55, 1])!
    ctx.drawLinearGradient(sea, start: CGPoint(x: 0, y: body.maxY),
                           end: CGPoint(x: 0, y: body.minY), options: [])

    let w = body.width, h = body.height
    let x0 = body.minX, y0 = body.minY

    // Two pale headlands reach in from either side and very nearly touch. The
    // land is the light mass and the water the dark one, because at 16pt the
    // eye reads the bright shape first and that shape should be the strait.
    let land = CGGradient(colorsSpace: space, colors: [
        colour(238, 234, 223), colour(206, 199, 184),
    ] as CFArray, locations: [0, 1])!

    func headland(onLeft: Bool) {
        // The inner coast runs from `shore` at top and bottom out towards the
        // middle, narrowest at mid-height. `pinch` is a control point, not the
        // coast itself: a cubic reaches only three quarters of the way to it,
        // so 0.487 puts the actual shoreline at 0.44 and leaves a channel
        // 12% of the icon wide.
        let shore = onLeft ? 0.30 : 0.70
        let pinch = onLeft ? 0.487 : 0.513
        let outer = onLeft ? -0.06 : 1.06

        let path = CGMutablePath()
        path.move(to: CGPoint(x: x0 + w * outer, y: y0 - h * 0.06))
        path.addLine(to: CGPoint(x: x0 + w * outer, y: y0 + h * 1.06))
        path.addLine(to: CGPoint(x: x0 + w * shore, y: y0 + h * 1.06))
        path.addCurve(
            to: CGPoint(x: x0 + w * shore, y: y0 - h * 0.06),
            control1: CGPoint(x: x0 + w * pinch, y: y0 + h * 0.78),
            control2: CGPoint(x: x0 + w * pinch, y: y0 + h * 0.22)
        )
        path.closeSubpath()

        ctx.saveGState()
        ctx.addPath(path)
        ctx.clip()
        ctx.drawLinearGradient(land, start: CGPoint(x: 0, y: body.maxY),
                               end: CGPoint(x: 0, y: body.minY), options: [])
        ctx.restoreGState()

        // A darker waterline where the land meets the sea.
        ctx.addPath(path)
        ctx.setStrokeColor(colour(120, 140, 150, 0.55))
        ctx.setLineWidth(size * 0.008)
        ctx.strokePath()
    }
    headland(onLeft: true)
    headland(onLeft: false)

    // The crossing: a route drawn over land and water alike, running through
    // the narrows and out the far side.
    let midY = y0 + h * 0.5
    let track = size * 0.05
    ctx.saveGState()
    ctx.addPath(CGPath(roundedRect:
        CGRect(x: x0 + w * 0.13, y: midY - track / 2, width: w * 0.60, height: track),
        cornerWidth: track / 2, cornerHeight: track / 2, transform: nil))
    ctx.setShadow(offset: .zero, blur: size * 0.05, color: colour(10, 30, 50, 0.45))
    ctx.setFillColor(colour(255, 255, 255))
    ctx.fillPath()
    ctx.restoreGState()

    // Arrowhead: which way the crossing goes.
    let tip = CGPoint(x: x0 + w * 0.88, y: midY)
    let back = size * 0.115
    let spread = size * 0.095
    let head = CGMutablePath()
    head.move(to: tip)
    head.addLine(to: CGPoint(x: tip.x - back, y: midY + spread))
    head.addLine(to: CGPoint(x: tip.x - back * 0.66, y: midY))
    head.addLine(to: CGPoint(x: tip.x - back, y: midY - spread))
    head.closeSubpath()
    ctx.saveGState()
    ctx.setShadow(offset: .zero, blur: size * 0.05, color: colour(10, 30, 50, 0.45))
    ctx.addPath(head)
    ctx.setFillColor(colour(255, 255, 255))
    ctx.fillPath()
    ctx.restoreGState()

    ctx.restoreGState()

    // A soft top edge highlight, the way Apple's own icons catch the light.
    ctx.saveGState()
    ctx.addPath(iconPath(in: body))
    ctx.setStrokeColor(colour(255, 255, 255, 0.18))
    ctx.setLineWidth(size * 0.006)
    ctx.strokePath()
    ctx.restoreGState()

    return ctx.makeImage()
}

func write(_ image: CGImage, to path: String) {
    let url = URL(fileURLWithPath: path)
    guard let dest = CGImageDestinationCreateWithURL(
        url as CFURL, UTType.png.identifier as CFString, 1, nil
    ) else { return }
    CGImageDestinationAddImage(dest, image, nil)
    CGImageDestinationFinalize(dest)
}

// The set macOS expects inside an .iconset.
let variants: [(name: String, pixels: Double)] = [
    ("icon_16x16", 16), ("icon_16x16@2x", 32),
    ("icon_32x32", 32), ("icon_32x32@2x", 64),
    ("icon_128x128", 128), ("icon_128x128@2x", 256),
    ("icon_256x256", 256), ("icon_256x256@2x", 512),
    ("icon_512x512", 512), ("icon_512x512@2x", 1024),
]

for variant in variants {
    guard let image = drawIcon(size: variant.pixels) else {
        FileHandle.standardError.write(Data("failed to draw \(variant.name)\n".utf8))
        exit(1)
    }
    write(image, to: "\(outputDirectory)/\(variant.name).png")
}
print("wrote \(variants.count) icon sizes to \(outputDirectory)")
