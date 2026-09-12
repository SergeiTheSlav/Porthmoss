import CoreGraphics
import Foundation
import ImageIO
import AppKit
import UniformTypeIdentifiers

// The app icon: a river.
//
// The background is Termoss's — the same dark slate squircle, the same
// gradient, the same lit top edge — so the two apps read as a pair on the
// Dock. The artwork follows Termoss's rule too: flat, saturated shapes, no
// gradients inside the glyph.
//
// Everything is proportional to `size`, so one drawing serves 16pt and 1024pt.
// Detail that would turn to mud at 16pt is deliberately absent.

let outputDirectory = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "."

func colour(_ r: Double, _ g: Double, _ b: Double, _ a: Double = 1) -> CGColor {
    CGColor(srgbRed: r / 255, green: g / 255, blue: b / 255, alpha: a)
}

/// Sampled from Termoss's own icon so the family resemblance is exact.
enum Palette {
    static let backgroundTop = colour(0x3D, 0x41, 0x51)
    static let backgroundBottom = colour(0x24, 0x27, 0x32)
    static let rim = colour(0x79, 0x7C, 0x88)
    static let river = colour(0x80, 0x98, 0xE0)
}

func iconPath(in rect: CGRect) -> CGPath {
    CGPath(roundedRect: rect, cornerWidth: rect.width * 0.2237,
           cornerHeight: rect.height * 0.2237, transform: nil)
}

/// A point on a centreline, with the unit tangent there.
typealias Course = (point: CGPoint, tangent: CGPoint)

/// Builds a ribbon by walking a centreline and offsetting perpendicular to it,
/// down one bank and back up the other.
func ribbon(steps: Int = 240,
            course: (Double) -> Course,
            halfWidth: (Double) -> Double) -> CGPath {
    var leftBank: [CGPoint] = []
    var rightBank: [CGPoint] = []
    leftBank.reserveCapacity(steps + 1)
    rightBank.reserveCapacity(steps + 1)

    for step in 0 ... steps {
        let t = Double(step) / Double(steps)
        let (point, tangent) = course(t)
        let normal = CGPoint(x: -tangent.y, y: tangent.x)
        let half = halfWidth(t)
        leftBank.append(CGPoint(x: point.x + normal.x * half, y: point.y + normal.y * half))
        rightBank.append(CGPoint(x: point.x - normal.x * half, y: point.y - normal.y * half))
    }

    // One continuous contour. addLines(between:) would start a fresh subpath
    // per call and leave two disconnected threads rather than a filled band.
    let path = CGMutablePath()
    path.move(to: leftBank[0])
    for point in leftBank.dropFirst() { path.addLine(to: point) }
    for point in rightBank.reversed() { path.addLine(to: point) }
    path.closeSubpath()
    return path
}

/// The main channel: a gentle meander running off the top and bottom edges, so
/// the squircle crops it and the river reads as flowing through rather than
/// beginning and ending inside the icon.
func mainChannel(_ t: Double, in body: CGRect) -> Course {
    let turns = 0.82
    let amplitude = body.width * 0.155
    let phase = -0.5 * Double.pi

    let top = body.maxY + body.height * 0.08
    let drop = body.height * 1.16

    let angle = 2 * Double.pi * turns * t + phase
    let x = body.midX + amplitude * sin(angle)
    let y = top - drop * t

    let dx = amplitude * 2 * Double.pi * turns * cos(angle)
    let dy = -drop
    let length = (dx * dx + dy * dy).squareRoot()
    return (CGPoint(x: x, y: y), CGPoint(x: dx / length, y: dy / length))
}

/// Rivers widen downstream, and that taper is most of what separates a river
/// from a road.
func mainWidth(_ t: Double, in body: CGRect) -> Double {
    body.width * (0.026 + 0.105 * t)
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

    ctx.saveGState()
    ctx.addPath(iconPath(in: body))
    ctx.clip()

    let slate = CGGradient(colorsSpace: space,
                           colors: [Palette.backgroundTop, Palette.backgroundBottom] as CFArray,
                           locations: [0, 1])!
    ctx.drawLinearGradient(slate, start: CGPoint(x: 0, y: body.maxY),
                           end: CGPoint(x: 0, y: body.minY), options: [])

    ctx.setFillColor(Palette.river)
    ctx.addPath(ribbon(
        course: { mainChannel($0, in: body) },
        halfWidth: { mainWidth($0, in: body) }
    ))
    ctx.fillPath()

    ctx.restoreGState()

    // Termoss's lit top edge.
    ctx.saveGState()
    ctx.addPath(iconPath(in: body))
    ctx.clip()
    ctx.addPath(iconPath(in: body.insetBy(dx: size * 0.004, dy: size * 0.004)))
    ctx.setStrokeColor(Palette.rim)
    ctx.setLineWidth(size * 0.009)
    ctx.strokePath()
    ctx.restoreGState()

    return ctx.makeImage()
}

/// Wraps PNGs in an ICO container, so Windows gets the same artwork from the
/// same drawing rather than a second, drifting copy of it. PNG-in-ICO has been
/// supported since Windows Vista.
func writeICO(_ images: [(size: Int, png: Data)], to path: String) {
    var header = Data()
    header.append(contentsOf: [0, 0, 1, 0])                       // reserved, type 1 (icon)
    header.append(contentsOf: withUnsafeBytes(of: UInt16(images.count).littleEndian, Array.init))

    var directory = Data()
    var payload = Data()
    var offset = 6 + images.count * 16

    for image in images {
        // 256 is encoded as 0 in the directory; the field is a single byte.
        let dimension = UInt8(image.size == 256 ? 0 : image.size)
        directory.append(contentsOf: [dimension, dimension, 0, 0])
        directory.append(contentsOf: withUnsafeBytes(of: UInt16(1).littleEndian, Array.init))
        directory.append(contentsOf: withUnsafeBytes(of: UInt16(32).littleEndian, Array.init))
        directory.append(contentsOf: withUnsafeBytes(of: UInt32(image.png.count).littleEndian, Array.init))
        directory.append(contentsOf: withUnsafeBytes(of: UInt32(offset).littleEndian, Array.init))
        offset += image.png.count
        payload.append(image.png)
    }
    try? (header + directory + payload).write(to: URL(fileURLWithPath: path))
}

func pngData(_ image: CGImage) -> Data? {
    let data = NSMutableData()
    guard let dest = CGImageDestinationCreateWithData(
        data, UTType.png.identifier as CFString, 1, nil
    ) else { return nil }
    CGImageDestinationAddImage(dest, image, nil)
    guard CGImageDestinationFinalize(dest) else { return nil }
    return data as Data
}

func write(_ image: CGImage, to path: String) {
    guard let dest = CGImageDestinationCreateWithURL(
        URL(fileURLWithPath: path) as CFURL, UTType.png.identifier as CFString, 1, nil
    ) else { return }
    CGImageDestinationAddImage(dest, image, nil)
    CGImageDestinationFinalize(dest)
}

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

// The Windows agent uses the same drawing for its tray icon, its window and
// the file icon Explorer shows.
if CommandLine.arguments.count > 2 {
    let icoPath = CommandLine.arguments[2]
    var entries: [(size: Int, png: Data)] = []
    for dimension in [16, 24, 32, 48, 64, 128, 256] {
        guard let image = drawIcon(size: Double(dimension)), let png = pngData(image) else { continue }
        entries.append((dimension, png))
    }
    writeICO(entries, to: icoPath)
    print("wrote \(entries.count)-size ICO to \(icoPath)")
}
