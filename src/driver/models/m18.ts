import type { DockButtonInfo, DockKeyInfo, StreamDockModel, StreamDockCapabilities } from '../types.js'

/**
 * Mirabox Stream Dock M18.
 *
 * Hardware layout (confirmed by the vendor, by the Companion surface module and by the
 * independent OpenDeck plugin):
 *   - 3 rows x 5 columns of 64x64 LCD keys  (keys 1..15)
 *   - 1 row with 3 physical buttons below the grid (no display, keys "16..18")
 *
 * Protocol notes:
 *   - protocol v3 => 1024 byte reports (report id + 1024 byte payload), press *and* release events
 *   - key codes in input reports are 0x01..0x0f for the LCD keys
 *   - image slot ids are NOT the same as the key codes, see `slot` below
 *   - 24 individually addressable RGB LEDs behind the keys (`SETLB` + 24*3 bytes)
 */
const M18_LED_COUNT = 24

/** Image slot id per key index (row major, top-left first). */
const M18_SLOTS = [0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x01, 0x02, 0x03, 0x04, 0x05]

/** Physical bottom buttons: left, middle, right. */
const M18_BUTTONS: Array<[code: number, column: number]> = [
	[0x25, 1],
	[0x30, 2],
	[0x31, 3],
]

const M18_CAPABILITIES: StreamDockCapabilities = {
	keyUp: true,
	ledColors: true,
	ledBrightness: true,
	sleep: true,
	// The M18 has no boot logo screen, the command exists but is useless.
	bootLogo: false,
	modes: false,
	// Reading the firmware version uses a HID feature report which node-hid cannot do on Windows.
	firmwareVersion: process.platform !== 'win32',
}

function createM18Model(options: {
	id: string
	name: string
	usbIds: StreamDockModel['usbIds']
	/** Some OEM units use 512 byte reports, override if the hardware requires it. */
	packetSize?: number
	/** Panel resolution, override for OEM variants. */
	imageSize?: number
}): StreamDockModel {
	// 64x64 is the real panel resolution: 60x60 ends up misaligned on hardware (verified with
	// `tools/m18-probe.mts pattern`). Other implementations (mirajazz/OpenDeck) also use 64x64.
	const imageSize = options.imageSize ?? 64

	const keys: DockKeyInfo[] = []
	for (let index = 0; index < 15; index++) {
		const row = Math.floor(index / 5)
		const column = index % 5
		keys.push({
			index,
			inputId: index + 1,
			slot: M18_SLOTS[index],
			name: `Key ${index + 1}`,
			row,
			column,
			id: `${row}/${column}`,
		})
	}

	const buttons: DockButtonInfo[] = M18_BUTTONS.map(([code, column], index) => ({
		index,
		code,
		name: `Button ${index + 1}`,
		row: 3,
		column,
		id: `3/${column}`,
	}))

	return {
		id: options.id,
		name: options.name,
		usbIds: options.usbIds,
		packetSize: options.packetSize ?? 1024,
		image: {
			width: imageSize,
			height: imageSize,
			maxBytes: 10240,
			// Verified on hardware (M18 EN, 6603:1012) with `tools/m18-probe.mts pattern`:
			// 64x64, no rotation, no mirroring, key N shows N with the white marker top-left.
			rotation: 0,
			mirrorX: false,
			mirrorY: false,
		},
		keys,
		buttons,
		ledCount: M18_LED_COUNT,
		capabilities: M18_CAPABILITIES,
	}
}

/** Mirabox branded M18, EN firmware (0x1012) and CN firmware (0x1009). */
export const M18_MODEL: StreamDockModel = createM18Model({
	id: 'm18',
	name: 'Mirabox Stream Dock M18',
	usbIds: [{ vendorId: 0x6603, productIds: [0x1012, 0x1009] }],
})

/**
 * VSDinside sells the same 15 LCD keys + 3 buttons layout under the M18 name.
 * It enumerates as "HOTSPOTEKUSB HID DEMO" with a different vendor id.
 */
export const VSDINSIDE_M18_MODEL: StreamDockModel = createM18Model({
	id: 'vsdinside-m18',
	name: 'VSDinside Stream Dock M18',
	usbIds: [{ vendorId: 0x5548, productIds: [0x1000] }],
})

export const M18_MODELS = [M18_MODEL, VSDINSIDE_M18_MODEL]
