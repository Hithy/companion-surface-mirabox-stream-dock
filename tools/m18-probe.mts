#!/usr/bin/env node
/**
 * Interactive hardware probe / demo for Stream Dock devices (M18 first).
 *
 *   npx tsx tools/m18-probe.mts list
 *   npx tsx tools/m18-probe.mts info
 *   npx tsx tools/m18-probe.mts listen 15
 *   npx tsx tools/m18-probe.mts pattern --size 64 --hold 15
 *   npx tsx tools/m18-probe.mts led-rainbow
 *   npx tsx tools/m18-probe.mts brightness 60
 *
 * The vendor software has to be closed, otherwise the HID device cannot be opened.
 */
import { M18_MODEL, StreamDockDevice, listDocks, parseInputReport } from '../src/driver/index.js'
import type { RawImage, Rgb, StreamDockImageFormat } from '../src/driver/index.js'

const DIGITS: Record<string, string[]> = {
	'0': ['111', '101', '101', '101', '111'],
	'1': ['010', '110', '010', '010', '111'],
	'2': ['111', '001', '111', '100', '111'],
	'3': ['111', '001', '111', '001', '111'],
	'4': ['101', '101', '111', '001', '001'],
	'5': ['111', '100', '111', '001', '111'],
	'6': ['111', '100', '111', '101', '111'],
	'7': ['111', '001', '001', '001', '001'],
	'8': ['111', '101', '111', '101', '111'],
	'9': ['111', '101', '111', '001', '111'],
}

function hsvToRgb(h: number, s: number, v: number): Rgb {
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

function createImage(width: number, height: number): RawImage {
	return { width, height, channels: 3, data: new Uint8Array(width * height * 3) }
}

function fill(image: RawImage, color: Rgb): void {
	for (let i = 0; i < image.data.length; i += 3) {
		image.data[i] = color[0]
		image.data[i + 1] = color[1]
		image.data[i + 2] = color[2]
	}
}

function rect(image: RawImage, x0: number, y0: number, size: number, color: Rgb): void {
	for (let y = y0; y < Math.min(y0 + size, image.height); y++) {
		for (let x = x0; x < Math.min(x0 + size, image.width); x++) {
			const offset = (y * image.width + x) * 3
			image.data[offset] = color[0]
			image.data[offset + 1] = color[1]
			image.data[offset + 2] = color[2]
		}
	}
}

function drawText(image: RawImage, text: string, scale: number, color: Rgb): void {
	const glyphWidth = 3 * scale
	const gap = scale
	const width = text.length * glyphWidth + (text.length - 1) * gap
	const height = 5 * scale
	const startX = Math.floor((image.width - width) / 2)
	const startY = Math.floor((image.height - height) / 2)

	let cursor = startX
	for (const character of text) {
		const glyph = DIGITS[character]
		if (!glyph) continue
		for (let row = 0; row < 5; row++) {
			for (let column = 0; column < 3; column++) {
				if (glyph[row][column] !== '1') continue
				rect(image, cursor + column * scale, startY + row * scale, scale, color)
			}
		}
		cursor += glyphWidth + gap
	}
}

/**
 * Test tile: coloured background + key number + four corner markers.
 * TL white, TR red, BL green, BR blue -> reveals rotation and mirroring at a glance.
 */
function createTestTile(index: number, format: StreamDockImageFormat): RawImage {
	const image = createImage(format.width, format.height)
	fill(image, hsvToRgb((index * 24) % 360, 0.55, 0.45))

	const marker = Math.max(3, Math.round(Math.min(format.width, format.height) / 8))
	rect(image, 0, 0, marker, [255, 255, 255])
	rect(image, format.width - marker, 0, marker, [255, 0, 0])
	rect(image, 0, format.height - marker, marker, [0, 255, 0])
	rect(image, format.width - marker, format.height - marker, marker, [0, 0, 255])

	const scale = Math.max(2, Math.floor(Math.min(format.width, format.height) / 18))
	drawText(image, String(index + 1), scale, [255, 255, 255])

	return image
}

function createSolidTile(color: Rgb, format: StreamDockImageFormat): RawImage {
	const image = createImage(format.width, format.height)
	fill(image, color)
	return image
}

function parseColor(value: string | undefined): Rgb | undefined {
	if (!value) return undefined
	const hex = value.replace(/^#/, '')
	if (!/^[0-9a-fA-F]{6}$/.test(hex)) throw new Error(`Invalid colour "${value}", expected #rrggbb`)
	return [parseInt(hex.slice(0, 2), 16), parseInt(hex.slice(2, 4), 16), parseInt(hex.slice(4, 6), 16)]
}

interface Args {
	command: string
	positional: string[]
	flags: Map<string, string | true>
}

function parseArgs(argv: string[]): Args {
	const flags = new Map<string, string | true>()
	const positional: string[] = []

	for (let i = 0; i < argv.length; i++) {
		const arg = argv[i]
		if (arg.startsWith('--')) {
			const name = arg.slice(2)
			const next = argv[i + 1]
			if (!next || next.startsWith('--')) {
				flags.set(name, true)
			} else if (/^-?\d+$/.test(next) || !next.startsWith('-')) {
				flags.set(name, next)
				i++
			} else {
				flags.set(name, true)
			}
		} else {
			positional.push(arg)
		}
	}

	return { command: positional.shift() ?? 'list', positional, flags }
}

function flagNumber(args: Args, name: string): number | undefined {
	const value = args.flags.get(name)
	if (value === undefined) return undefined
	const parsed = Number(value)
	if (!Number.isFinite(parsed)) throw new Error(`--${name} expects a number`)
	return parsed
}

function printUsage(): void {
	console.log(`
Stream Dock probe

  npx tsx tools/m18-probe.mts <command> [options]

Commands
  list                        list connected devices (default)
  info                        open the device, print model + firmware
  listen [seconds]            print key/button events (default 15s, add --raw for hex)
  selftest                    offline check of the input report parser (no device needed)
  pattern                     draw numbered test tiles on every key
  solid <#rrggbb>             fill every key with one colour
  clear                       clear all keys
  led <#rrggbb>               set every LED
  led-rainbow                 rainbow over all LEDs
  led-scan                    light one LED at a time (verifies count + index order)
  led-off                     switch the LEDs off
  brightness <0-100>          display brightness
  led-brightness <0-100>      LED brightness
  wake | sleep                send DIS / HAN

Options
  --index <n>                 open the n-th device (default 0)
  --path <path>               open a device by HID path
  --serial <sn>               open a device by serial number
  --size <px>                 override the panel resolution (M18 = 64)
  --rotate <0|90|180|270>     override the image rotation
  --mirror-x / --mirror-y     mirror the image
  --hold <seconds>            keep the device open after drawing (default 10)
  --leds <n>                  override the LED count
  --delay <ms>                led-scan step delay (default 500)
  --raw                       in listen, also dump every raw HID report
  --no-shutdown               do not clear/sleep the device when closing
`)
}

async function applyOverrides(dock: StreamDockDevice, args: Args): Promise<void> {
	const size = flagNumber(args, 'size')
	const rotate = flagNumber(args, 'rotate')
	const leds = flagNumber(args, 'leds')

	if (size !== undefined) dock.imageFormat = { ...dock.imageFormat, width: size, height: size }
	if (rotate !== undefined) {
		if (![0, 90, 180, 270].includes(rotate)) throw new Error('--rotate must be 0, 90, 180 or 270')
		dock.imageFormat = { ...dock.imageFormat, rotation: rotate as 0 | 90 | 180 | 270 }
	}
	if (args.flags.has('mirror-x')) dock.imageFormat = { ...dock.imageFormat, mirrorX: true }
	if (args.flags.has('mirror-y')) dock.imageFormat = { ...dock.imageFormat, mirrorY: true }
	if (leds !== undefined) (dock.model as { ledCount: number }).ledCount = leds
}

async function hold(args: Args): Promise<void> {
	const seconds = flagNumber(args, 'hold') ?? 10
	if (seconds <= 0) return
	await new Promise((resolve) => setTimeout(resolve, seconds * 1000))
}

function runSelftest(): void {
	// Samples captured from an M18 (6603:1012, 512 byte input reports).
	const report = (code: number, state: number): Uint8Array => {
		const buffer = new Uint8Array(512)
		buffer.set([0x41, 0x43, 0x4b, 0x00, 0x00, 0x4f, 0x4b, 0x00, 0x00, code, state])
		return buffer
	}

	const cases: Array<{
		name: string
		data: Uint8Array
		expect: null | { code: number; pressed: boolean; kind: 'key' | 'button' | null; index: number }
	}> = [
		{
			name: 'key 1 down',
			data: report(0x01, 0x01),
			expect: { code: 0x01, pressed: true, kind: 'key', index: 0 },
		},
		{
			name: 'key 1 up',
			data: report(0x01, 0x00),
			expect: { code: 0x01, pressed: false, kind: 'key', index: 0 },
		},
		{
			name: 'key 15 down',
			data: report(0x0f, 0x01),
			expect: { code: 0x0f, pressed: true, kind: 'key', index: 14 },
		},
		{
			name: 'button left down',
			data: report(0x25, 0x01),
			expect: { code: 0x25, pressed: true, kind: 'button', index: 0 },
		},
		{
			name: 'button middle up',
			data: report(0x30, 0x00),
			expect: { code: 0x30, pressed: false, kind: 'button', index: 1 },
		},
		{
			name: 'button right down',
			data: report(0x31, 0x01),
			expect: { code: 0x31, pressed: true, kind: 'button', index: 2 },
		},
		{ name: 'command ack (code 0)', data: report(0x00, 0x00), expect: null },
		{ name: 'unknown code', data: report(0x7f, 0x01), expect: null },
		{ name: 'truncated report', data: report(0x01, 0x01).subarray(0, 10), expect: null },
	]

	let failures = 0
	for (const testCase of cases) {
		const parsed = parseInputReport(M18_MODEL, testCase.data)
		const actual = parsed
			? {
					code: parsed.code,
					pressed: parsed.pressed,
					kind: parsed.key ? ('key' as const) : ('button' as const),
					index: (parsed.key ?? parsed.button)!.index,
				}
			: null
		const ok = JSON.stringify(actual) === JSON.stringify(testCase.expect)
		if (!ok) failures++
		console.log(
			`${ok ? 'ok  ' : 'FAIL'} ${testCase.name.padEnd(22)} -> ${JSON.stringify(actual)}` +
				(ok ? '' : ` (expected ${JSON.stringify(testCase.expect)})`),
		)
	}

	const passed = failures === 0
	console.log(passed ? `All ${cases.length} parser checks passed.` : `${failures} check(s) failed.`)
	if (!passed) process.exitCode = 1
}

async function main(): Promise<void> {
	const args = parseArgs(process.argv.slice(2))
	if (args.flags.has('help')) {
		printUsage()
		return
	}

	if (args.command === 'selftest') {
		// Offline check, must not require a connected device.
		runSelftest()
		return
	}

	if (args.command === 'list') {
		const docks = await listDocks({ includeAllInterfaces: args.flags.has('all') })
		if (docks.length === 0) {
			console.log('No Stream Dock found (is the vendor software running?)')
			return
		}
		for (const dock of docks) {
			console.log(
				`${dock.model.name} [${dock.model.id}]\n` +
					`  vid/pid   ${dock.info.vendorId.toString(16)}:${dock.info.productId.toString(16)}\n` +
					`  serial    ${dock.info.serialNumber ?? '-'}\n` +
					`  interface ${dock.info.interface} usagePage ${dock.info.usagePage ?? '-'}\n` +
					`  keys      ${dock.model.keys.length} (${dock.model.image.width}x${dock.model.image.height})\n` +
					`  buttons   ${dock.model.buttons.length}\n` +
					`  leds      ${dock.model.ledCount}\n` +
					`  path      ${dock.info.path}`,
			)
		}
		return
	}

	const openOptions = {
		index: flagNumber(args, 'index') ?? 0,
		path: args.flags.get('path') as string | undefined,
		serialNumber: args.flags.get('serial') as string | undefined,
		// `--all` also allows opening the secondary (keyboard) interface, for diagnostics.
		includeAllInterfaces: args.flags.has('all'),
	}
	const dock = await StreamDockDevice.open(openOptions)
	await applyOverrides(dock, args)

	const close = async () => dock.close({ shutdown: !args.flags.has('no-shutdown') })

	try {
		switch (args.command) {
			case 'info': {
				console.log(`Model        ${dock.model.name} (${dock.model.id})`)
				console.log(`Serial       ${dock.serialNumber ?? '-'}`)
				console.log(`Protocol     ${dock.model.packetSize} byte reports`)
				console.log(
					`Panel        ${dock.imageFormat.width}x${dock.imageFormat.height}, max ${dock.imageFormat.maxBytes} bytes/key`,
				)
				console.log(`Capabilities ${JSON.stringify(dock.model.capabilities)}`)
				console.log(`Firmware     ${(await dock.readFirmwareVersion()) ?? 'unavailable'}`)
				console.log('Keys:')
				for (const key of dock.model.keys) {
					console.log(
						`  #${key.index + 1} ${key.id} input 0x${key.inputId.toString(16)} slot 0x${key.slot.toString(16)}`,
					)
				}
				console.log('Buttons:')
				for (const button of dock.model.buttons) {
					console.log(`  ${button.name} ${button.id} code 0x${button.code.toString(16)}`)
				}
				break
			}

			case 'listen': {
				const seconds = Number(args.positional[0] ?? 15)
				const verbose = args.flags.has('raw') || !!process.env.STREAMDOCK_DEBUG
				let reports = 0
				let events = 0

				dock.on('keyDown', (event) => {
					events++
					console.log(
						`DOWN  ${event.name.padEnd(9)} kind=${event.kind} id=${event.id} code=0x${event.code.toString(16)}`,
					)
				})
				dock.on('keyUp', (event) => {
					events++
					console.log(
						`UP    ${event.name.padEnd(9)} kind=${event.kind} id=${event.id} code=0x${event.code.toString(16)}`,
					)
				})
				dock.on('report', (data) => {
					reports++
					if (verbose) {
						const head = data.subarray(0, 32).toString('hex')
						console.log(`RAW   len=${data.length} ${head}${data.length > 32 ? ' ...' : ''}`)
					}
				})

				console.log(`Listening for ${seconds}s, press keys/buttons...`)
				await new Promise((resolve) => setTimeout(resolve, seconds * 1000))
				console.log(`Done, ${reports} raw report(s), ${events} event(s) received.`)
				if (reports > 0 && events === 0) {
					console.log('Reports arrived but none could be parsed, re-run with --raw and share the hex.')
				}
				if (reports === 0) {
					console.log(
						[
							'No HID input reports arrived. Check in this order:',
							'  1. press a key *while* the countdown runs (the device stays open)',
							'  2. the vendor app / Companion must not be running',
							'  3. try another interface: `probe list --all`, then `probe listen 20 --path "<path>"`',
						].join('\n'),
					)
				}
				break
			}

			case 'pattern': {
				const entries = dock.model.keys.map((key) => ({
					index: key.index,
					image: createTestTile(key.index, dock.imageFormat),
				}))
				await dock.drawKeys(entries)
				console.log(
					`Drew ${entries.length} test tiles at ${dock.imageFormat.width}x${dock.imageFormat.height} ` +
						`rotation=${dock.imageFormat.rotation}.`,
				)
				console.log(
					'Expected: key N shows the number N, white marker top-left, red top-right, green bottom-left, blue bottom-right.',
				)
				await hold(args)
				break
			}

			case 'solid': {
				const color = parseColor(args.positional[0] ?? '#3050ff')!
				const entries = dock.model.keys.map((key) => ({
					index: key.index,
					image: createSolidTile(color, dock.imageFormat),
				}))
				await dock.drawKeys(entries)
				console.log(`Filled ${entries.length} keys with rgb(${color.join(', ')})`)
				await hold(args)
				break
			}

			case 'clear': {
				await dock.clearAll()
				console.log('Cleared all keys')
				break
			}

			case 'led': {
				const color = parseColor(args.positional[0] ?? '#3050ff')!
				await dock.setAllLeds(color)
				console.log(`Set ${dock.model.ledCount} LEDs to rgb(${color.join(', ')})`)
				await hold(args)
				break
			}

			case 'led-scan': {
				const delay = flagNumber(args, 'delay') ?? 500
				const count = dock.model.ledCount
				const black = Array.from({ length: count }, () => [0, 0, 0] as const)

				console.log(`Scanning ${count} LEDs, one at a time (${delay}ms each). Watch the device.`)
				console.log('Expected: every step lights exactly ONE different LED, and all visible LEDs are covered.')

				for (let index = 0; index < count; index++) {
					const colors = black.map((color, position) => (position === index ? ([255, 255, 255] as const) : color))
					await dock.setLedColors(colors)
					process.stdout.write(`LED ${String(index).padStart(2, ' ')}  `)
					await new Promise((resolve) => setTimeout(resolve, delay))
				}

				await dock.setLedColors(black)
				console.log(
					'Done. If some steps lit nothing, the real count is lower; if one step lit several LEDs, it is higher.',
				)
				console.log(`Change the count with: npm run probe -- led-scan --leds <n>`)
				break
			}

			case 'led-rainbow': {
				const colors: Rgb[] = Array.from({ length: dock.model.ledCount }, (_, index) =>
					hsvToRgb((index * 360) / Math.max(1, dock.model.ledCount), 1, 1),
				)
				await dock.setLedColors(colors)
				console.log(`Set ${colors.length} LEDs to a rainbow`)
				await hold(args)
				break
			}

			case 'led-off': {
				await dock.setAllLeds([0, 0, 0])
				console.log('LEDs off')
				break
			}

			case 'brightness': {
				const value = Number(args.positional[0] ?? 100)
				await dock.setBrightness(value)
				console.log(`Display brightness ${value}%`)
				break
			}

			case 'led-brightness': {
				const value = Number(args.positional[0] ?? 100)
				await dock.setLedBrightness(value)
				console.log(`LED brightness ${value}%`)
				break
			}

			case 'wake': {
				await dock.wake()
				console.log('Sent DIS')
				break
			}

			case 'sleep': {
				await dock.sleep()
				console.log('Sent HAN')
				break
			}

			default: {
				printUsage()
				process.exitCode = 1
			}
		}
	} finally {
		await close()
	}
}

main().catch((error: unknown) => {
	console.error(error instanceof Error ? error.message : error)
	process.exitCode = 1
})
