import jpg from '@julusian/jpeg-turbo'
import type { DrawImageOptions, Rgb, Rotation, StreamDockImageFormat } from './types.js'
import type { RawImage } from './types.js'

/** Packed RGB image used internally. */
export interface RgbImage {
	data: Uint8Array
	width: number
	height: number
}

const DEFAULT_BACKGROUND: Rgb = [0, 0, 0]

function clamp(value: number, min: number, max: number): number {
	return Math.min(max, Math.max(min, value))
}

function createCanvas(width: number, height: number, background: Rgb): RgbImage {
	const data = new Uint8Array(width * height * 3)
	for (let i = 0; i < data.length; i += 3) {
		data[i] = background[0]
		data[i + 1] = background[1]
		data[i + 2] = background[2]
	}
	return { data, width, height }
}

/** Convert any supported input into tightly packed RGB, compositing alpha over `background`. */
export function toRgbImage(input: RawImage, background: Rgb = DEFAULT_BACKGROUND): RgbImage {
	const { width, height, channels } = input
	if (!Number.isInteger(width) || !Number.isInteger(height) || width <= 0 || height <= 0) {
		throw new Error(`Invalid image size ${width}x${height}`)
	}
	const expected = width * height * channels
	if (input.data.length < expected) {
		throw new Error(
			`Image buffer is too small: got ${input.data.length} bytes, expected at least ${expected} for ${width}x${height}x${channels}`,
		)
	}

	if (channels === 3) {
		return { data: input.data.subarray(0, expected), width, height }
	}

	const [br, bg, bb] = background
	const data = new Uint8Array(width * height * 3)
	for (let source = 0, target = 0; target < data.length; source += 4, target += 3) {
		const alpha = input.data[source + 3] / 255
		data[target] = Math.round(input.data[source] * alpha + br * (1 - alpha))
		data[target + 1] = Math.round(input.data[source + 1] * alpha + bg * (1 - alpha))
		data[target + 2] = Math.round(input.data[source + 2] * alpha + bb * (1 - alpha))
	}
	return { data, width, height }
}

export function mirrorImage(image: RgbImage, mirrorX: boolean, mirrorY: boolean): RgbImage {
	if (!mirrorX && !mirrorY) return image

	const { width, height, data } = image
	const out = new Uint8Array(data.length)
	for (let y = 0; y < height; y++) {
		const sourceY = mirrorY ? height - 1 - y : y
		for (let x = 0; x < width; x++) {
			const sourceX = mirrorX ? width - 1 - x : x
			const source = (sourceY * width + sourceX) * 3
			const target = (y * width + x) * 3
			out[target] = data[source]
			out[target + 1] = data[source + 1]
			out[target + 2] = data[source + 2]
		}
	}
	return { data: out, width, height }
}

export function rotateImage(image: RgbImage, rotation: Rotation): RgbImage {
	if (rotation === 0) return image

	const { width, height, data } = image
	const outWidth = rotation === 180 ? width : height
	const outHeight = rotation === 180 ? height : width
	const out = new Uint8Array(data.length)

	for (let y = 0; y < outHeight; y++) {
		for (let x = 0; x < outWidth; x++) {
			let sourceX: number
			let sourceY: number
			switch (rotation) {
				case 90: // clockwise
					sourceX = y
					sourceY = height - 1 - x
					break
				case 180:
					sourceX = width - 1 - x
					sourceY = height - 1 - y
					break
				case 270: // clockwise
					sourceX = width - 1 - y
					sourceY = x
					break
				default:
					sourceX = x
					sourceY = y
			}
			const source = (sourceY * width + sourceX) * 3
			const target = (y * outWidth + x) * 3
			out[target] = data[source]
			out[target + 1] = data[source + 1]
			out[target + 2] = data[source + 2]
		}
	}

	return { data: out, width: outWidth, height: outHeight }
}

function resizeBilinear(image: RgbImage, width: number, height: number): RgbImage {
	const { data, width: sourceWidth, height: sourceHeight } = image
	const out = new Uint8Array(width * height * 3)

	const scaleX = sourceWidth / width
	const scaleY = sourceHeight / height

	for (let y = 0; y < height; y++) {
		const sourceY = clamp((y + 0.5) * scaleY - 0.5, 0, sourceHeight - 1)
		const y0 = Math.floor(sourceY)
		const y1 = Math.min(sourceHeight - 1, y0 + 1)
		const fy = sourceY - y0

		for (let x = 0; x < width; x++) {
			const sourceX = clamp((x + 0.5) * scaleX - 0.5, 0, sourceWidth - 1)
			const x0 = Math.floor(sourceX)
			const x1 = Math.min(sourceWidth - 1, x0 + 1)
			const fx = sourceX - x0

			for (let channel = 0; channel < 3; channel++) {
				const p00 = data[(y0 * sourceWidth + x0) * 3 + channel]
				const p10 = data[(y0 * sourceWidth + x1) * 3 + channel]
				const p01 = data[(y1 * sourceWidth + x0) * 3 + channel]
				const p11 = data[(y1 * sourceWidth + x1) * 3 + channel]

				const top = p00 + (p10 - p00) * fx
				const bottom = p01 + (p11 - p01) * fx
				out[(y * width + x) * 3 + channel] = Math.round(top + (bottom - top) * fy)
			}
		}
	}

	return { data: out, width, height }
}

/** Area average, used when downscaling (avoids the aliasing of nearest neighbour). */
function resizeBox(image: RgbImage, width: number, height: number): RgbImage {
	const { data, width: sourceWidth, height: sourceHeight } = image
	const out = new Uint8Array(width * height * 3)

	for (let y = 0; y < height; y++) {
		const yStart = Math.floor((y * sourceHeight) / height)
		const yEnd = Math.max(yStart + 1, Math.ceil(((y + 1) * sourceHeight) / height))

		for (let x = 0; x < width; x++) {
			const xStart = Math.floor((x * sourceWidth) / width)
			const xEnd = Math.max(xStart + 1, Math.ceil(((x + 1) * sourceWidth) / width))

			let r = 0
			let g = 0
			let b = 0
			let count = 0

			for (let sy = yStart; sy < yEnd && sy < sourceHeight; sy++) {
				for (let sx = xStart; sx < xEnd && sx < sourceWidth; sx++) {
					const offset = (sy * sourceWidth + sx) * 3
					r += data[offset]
					g += data[offset + 1]
					b += data[offset + 2]
					count++
				}
			}

			const target = (y * width + x) * 3
			out[target] = Math.round(r / count)
			out[target + 1] = Math.round(g / count)
			out[target + 2] = Math.round(b / count)
		}
	}

	return { data: out, width, height }
}

export function resizeImage(image: RgbImage, width: number, height: number): RgbImage {
	if (image.width === width && image.height === height) return image
	if (image.width > width && image.height > height) return resizeBox(image, width, height)
	return resizeBilinear(image, width, height)
}

export function blitImage(target: RgbImage, source: RgbImage, offsetX: number, offsetY: number): void {
	const startX = Math.max(0, offsetX)
	const startY = Math.max(0, offsetY)
	const endX = Math.min(target.width, offsetX + source.width)
	const endY = Math.min(target.height, offsetY + source.height)

	for (let y = startY; y < endY; y++) {
		for (let x = startX; x < endX; x++) {
			const sourceOffset = ((y - offsetY) * source.width + (x - offsetX)) * 3
			const targetOffset = (y * target.width + x) * 3
			target.data[targetOffset] = source.data[sourceOffset]
			target.data[targetOffset + 1] = source.data[sourceOffset + 1]
			target.data[targetOffset + 2] = source.data[sourceOffset + 2]
		}
	}
}

/**
 * Apply mirror/rotation/fit and return a packed RGB buffer of exactly
 * `format.width * format.height * 3` bytes.
 */
export function prepareImage(input: RawImage, format: StreamDockImageFormat, options: DrawImageOptions = {}): RgbImage {
	const background = options.background ?? DEFAULT_BACKGROUND
	const rotation = options.rotation ?? format.rotation
	const mirrorX = options.mirrorX ?? format.mirrorX
	const mirrorY = options.mirrorY ?? format.mirrorY
	const fit = options.fit ?? 'contain'

	let image = toRgbImage(input, background)
	image = mirrorImage(image, mirrorX, mirrorY)
	image = rotateImage(image, rotation)

	if (fit === 'stretch') {
		return resizeImage(image, format.width, format.height)
	}

	const scale =
		fit === 'cover'
			? Math.max(format.width / image.width, format.height / image.height)
			: Math.min(format.width / image.width, format.height / image.height)
	const scaledWidth = Math.max(1, Math.round(image.width * scale))
	const scaledHeight = Math.max(1, Math.round(image.height * scale))
	const resized = resizeImage(image, scaledWidth, scaledHeight)

	if (scaledWidth <= format.width && scaledHeight <= format.height) {
		const canvas = createCanvas(format.width, format.height, background)
		blitImage(
			canvas,
			resized,
			Math.floor((format.width - resized.width) / 2),
			Math.floor((format.height - resized.height) / 2),
		)
		return canvas
	}

	// cover: crop the overflowing part, keeping the image centred.
	const canvas = createCanvas(format.width, format.height, background)
	const offsetX = Math.floor((resized.width - format.width) / 2)
	const offsetY = Math.floor((resized.height - format.height) / 2)
	for (let y = 0; y < format.height; y++) {
		const sourceOffset = ((offsetY + y) * resized.width + offsetX) * 3
		const targetOffset = y * format.width * 3
		canvas.data.set(resized.data.subarray(sourceOffset, sourceOffset + format.width * 3), targetOffset)
	}
	return canvas
}

/**
 * Encode to JPEG, lowering the quality until the payload fits into `format.maxBytes`.
 * Uses 4:2:0 chroma subsampling as a last resort.
 */
export async function encodeJpeg(image: RgbImage, format: StreamDockImageFormat, startQuality = 90): Promise<Buffer> {
	const raw = Buffer.from(image.data.buffer, image.data.byteOffset, image.data.byteLength)
	let quality = clamp(Math.round(startQuality), 1, 100)
	const subsampling = [jpg.SAMP_422, jpg.SAMP_420] as const

	let encoded: Buffer = Buffer.alloc(0)
	for (const sampling of subsampling) {
		for (let attempt = 0; attempt < 10; attempt++) {
			encoded = await jpg.compress(raw, {
				format: jpg.FORMAT_RGB,
				width: image.width,
				height: image.height,
				subsampling: sampling,
				quality,
			})
			if (encoded.length <= format.maxBytes) return encoded

			if (quality <= 10) break
			quality = Math.max(10, quality - 10)
		}
	}

	throw new Error(
		`Could not compress the key image below ${format.maxBytes} bytes (got ${encoded.length} bytes at ${image.width}x${image.height})`,
	)
}
