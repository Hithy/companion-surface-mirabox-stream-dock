#!/usr/bin/env node
/**
 * Reference "upper layer application" built on top of the Stream Dock driver.
 *
 * Run with the vendor software closed:
 *
 *   npx tsx tools/m18-demo.mts            # first connected device
 *   npx tsx tools/m18-demo.mts --serial 08A903783D06
 *
 * Shows:
 *   - batched key drawing (only the changed keys are re-sent)
 *   - key / button events
 *   - per LED colours
 *   - graceful shutdown on Ctrl+C
 */
import { StreamDockDevice } from '../src/driver/index.js'
import type { Rgb, RawImage } from '../src/driver/index.js'

const WIDTH = 64
const HEIGHT = 64

function solid(color: Rgb): RawImage {
	const data = new Uint8Array(WIDTH * HEIGHT * 3)
	for (let i = 0; i < data.length; i += 3) {
		data[i] = color[0]
		data[i + 1] = color[1]
		data[i + 2] = color[2]
	}
	return { width: WIDTH, height: HEIGHT, channels: 3, data }
}

function hsv(h: number, s = 1, v = 1): Rgb {
	const c = v * s
	const hp = (((h % 360) + 360) % 360) / 60
	const x = c * (1 - Math.abs((hp % 2) - 1))
	let r = 0
	let g = 0
	let b = 0
	if (hp < 1) [r, g, b] = [c, x, 0]
	else if (hp < 2) [r, g, b] = [x, c, 0]
	else if (hp < 3) [r, g, b] = [0, c, x]
	else if (hp < 4) [r, g, b] = [0, x, c]
	else if (hp < 5) [r, g, b] = [x, 0, c]
	else [r, g, b] = [c, 0, x]
	const m = v - c
	return [Math.round((r + m) * 255), Math.round((g + m) * 255), Math.round((b + m) * 255)]
}

async function main(): Promise<void> {
	const serialIndex = process.argv.indexOf('--serial')
	const dock = await StreamDockDevice.open(serialIndex >= 0 ? { serialNumber: process.argv[serialIndex + 1] } : {})

	console.log(`Connected: ${dock.model.name} (${dock.serialNumber ?? 'no serial'})`)

	// The panel resolution is part of the model but can be tuned per application.
	dock.imageFormat = { ...dock.imageFormat, width: WIDTH, height: HEIGHT }

	const off = solid([0, 0, 0])
	const dim = solid([8, 8, 14])
	const hot = solid([240, 240, 255])
	const pressed = solid([0, 220, 120])

	// Paint the idle state once.
	await dock.drawKeys(dock.keys.map((key) => ({ index: key.index, image: dim })))
	await dock.setBrightness(90)
	await dock.setLedBrightness(80)

	let head = 0
	let paused = false
	const pressedUntil = new Map<number, number>()

	const cleanup = async (): Promise<void> => {
		clearInterval(timer)
		dock.removeAllListeners()
		await dock.close()
		console.log('\nClosed.')
		process.exit(0)
	}

	const timer = setInterval(() => {
		if (paused) return

		const now = Date.now()
		const previous = head
		head = (head + 1) % dock.keys.length

		const updates = [
			{ index: previous, image: dim },
			{ index: head, image: hot },
		]
		for (const [index, until] of pressedUntil) {
			if (until > now) updates.push({ index, image: pressed })
			else pressedUntil.delete(index)
		}

		dock.drawKeys(updates).catch((error: Error) => console.error('draw failed:', error.message))

		const ledColors = Array.from({ length: dock.model.ledCount }, (_, index) =>
			hsv(((index - head * 2) * 360) / Math.max(1, dock.model.ledCount), 0.9, index % 6 === head % 6 ? 1 : 0.25),
		)
		dock.setLedColors(ledColors).catch((error: Error) => console.error('led failed:', error.message))
	}, 120)
	timer.unref?.()

	dock.on('keyDown', (event) => {
		console.log(`keyDown ${event.name} (${event.id})`)
		if (event.kind === 'key') pressedUntil.set(event.index, Date.now() + 300)
	})

	dock.on('keyUp', (event) => {
		console.log(`keyUp   ${event.name} (${event.id})`)
	})

	dock.on('error', (error) => console.error('error:', error.message))

	// The three physical buttons below the LCD grid are not part of keys[].
	for (const button of dock.buttons) {
		console.log(`button ${button.name} -> ${button.id} (code 0x${button.code.toString(16)})`)
	}

	dock.on('keyDown', (event) => {
		if (event.kind !== 'button') return
		paused = !paused
		console.log(paused ? 'paused' : 'resumed')
	})

	process.on('SIGINT', () => void cleanup())
	process.on('SIGTERM', () => void cleanup())

	console.log('Animation running, press Ctrl+C to stop.')
}

main().catch((error: unknown) => {
	console.error(error instanceof Error ? error.message : error)
	process.exitCode = 1
})
