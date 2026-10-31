import type { DockButtonInfo, DockKeyInfo, StreamDockModel } from './types.js'

export interface ParsedInput {
	/** Raw device code (byte 9). */
	code: number
	pressed: boolean
	key?: DockKeyInfo
	button?: DockButtonInfo
}

/**
 * Parse an input report.
 *
 * Layout observed on the M18 (6603:1012), 512 byte report:
 *
 * ```
 * 0  41 'A'
 * 1  43 'C'
 * 2  4b 'K'
 * 3  00
 * 4  00
 * 5  4f 'O'
 * 6  4b 'K'
 * 7  00
 * 8  00
 * 9  <key code>     0x01..0x0f = LCD keys, 0x25/0x30/0x31 = bottom buttons
 * 10 <state>        0x01 = pressed, 0x00 = released
 * 11 ...            padding
 * ```
 *
 * Note that the `ACK` prefix is *not* only used for command acknowledgements on these
 * devices — the key events use it too, which is why the prefix must not be filtered out.
 */
export function parseInputReport(model: StreamDockModel, data: Uint8Array): ParsedInput | null {
	if (data.length < 11) return null

	const code = data[9]
	const state = data[10]
	if (code === 0) return null

	const key = model.keys.find((candidate) => candidate.inputId === code)
	const button = key ? undefined : model.buttons.find((candidate) => candidate.code === code)
	if (!key && !button) return null

	return { code, pressed: state !== 0, key, button }
}
