import { EventEmitter } from 'node:events'
import { HIDAsync } from 'node-hid'
import { StreamDockProtocol } from './protocol.js'
import type { ImageFrame } from './protocol.js'
import { encodeJpeg, prepareImage } from './image.js'
import { listDocks } from './hid.js'
import { parseInputReport } from './input.js'
import type {
	DockButtonInfo,
	DockEvents,
	DockKeyEvent,
	DockKeyInfo,
	DiscoveredDock,
	DrawImageOptions,
	HidDeviceInfo,
	OpenOptions,
	RawImage,
	Rgb,
	StreamDockImageFormat,
	StreamDockModel,
} from './types.js'

export interface KeyImageEntry {
	/** 0-based key index (`model.keys[i].index`). */
	index: number
	image: RawImage
	options?: DrawImageOptions
}

function describeOpenError(path: string, error: unknown): Error {
	const reason = error instanceof Error ? error.message : String(error)
	return new Error(
		`Could not open the Stream Dock at ${path}: ${reason}\n` +
			'Hints: make sure the vendor software (Stream Dock) is fully closed, the device is not ' +
			'used by Companion, and that you have permission to access the HID device (udev rules on Linux).',
	)
}

/**
 * A single Stream Dock device.
 *
 * ```ts
 * const dock = await StreamDockDevice.open()
 * dock.on('keyDown', (event) => console.log(event.name))
 * await dock.drawKey(0, { data: rgb, width: 64, height: 64, channels: 3 })
 * ```
 */
export class StreamDockDevice extends EventEmitter<DockEvents> {
	readonly model: StreamDockModel
	readonly info: HidDeviceInfo
	/** Image format used for drawing, mutable so an application can tune resolution/rotation. */
	imageFormat: StreamDockImageFormat

	readonly #hid: HIDAsync
	readonly #protocol: StreamDockProtocol
	readonly #autoRefresh: boolean
	readonly #pressedCodes = new Set<number>()
	readonly #debug = !!process.env.STREAMDOCK_DEBUG
	#ledColors: Rgb[]

	#heartbeat: NodeJS.Timeout | undefined
	#initialized = false
	#closed = false

	private constructor(descriptor: DiscoveredDock, hid: HIDAsync, options: OpenOptions) {
		super()

		this.model = descriptor.model
		this.info = descriptor.info
		this.imageFormat = { ...descriptor.model.image }
		this.#hid = hid
		this.#autoRefresh = options.autoRefresh ?? true
		this.#ledColors = Array.from({ length: descriptor.model.ledCount }, () => [0, 0, 0] as Rgb)
		this.#protocol = new StreamDockProtocol({
			writer: {
				write: async (data) => hid.write(data),
			},
			packetSize: descriptor.model.packetSize,
		})

		this.#hid.on('data', (data: Buffer) => this.#handleReport(data))
		this.#hid.on('error', (error: Error) => this.emit('error', error))

		const heartbeatMs = options.heartbeatMs ?? 8000
		if (heartbeatMs > 0) {
			this.#heartbeat = setInterval(() => {
				this.#protocol.keepAlive().catch((error: Error) => this.emit('error', error))
			}, heartbeatMs)
			this.#heartbeat.unref?.()
		}
	}

	// ---------------------------------------------------------------- discovery

	/** All connected Stream Dock devices known to the driver. */
	static async list(): Promise<DiscoveredDock[]> {
		return listDocks()
	}

	/** Open a device. Without any filter the first discovered device is used. */
	static async open(options: OpenOptions = {}): Promise<StreamDockDevice> {
		const docks = await listDocks({ includeAllInterfaces: options.includeAllInterfaces })

		let descriptor: DiscoveredDock | undefined
		if (options.path) {
			descriptor = docks.find((dock) => dock.info.path === options.path)
			if (!descriptor) throw new Error(`No Stream Dock found at path ${options.path}`)
		} else if (options.serialNumber) {
			descriptor = docks.find((dock) => dock.info.serialNumber === options.serialNumber)
			if (!descriptor) throw new Error(`No Stream Dock found with serial number ${options.serialNumber}`)
		} else {
			descriptor = docks[options.index ?? 0]
			if (!descriptor) {
				throw new Error('No Stream Dock found. Make sure the device is connected and the vendor software is closed.')
			}
		}

		// node-hid only treats the first argument as a path when it is the *only* argument,
		// so never pass an options object here.
		const hid = await HIDAsync.open(descriptor.info.path).catch((error: unknown) => {
			throw describeOpenError(descriptor.info.path, error)
		})

		const device = new StreamDockDevice(descriptor, hid, options)
		try {
			// Wake the device once, otherwise some firmware revisions do not report inputs.
			await device.wake()
		} catch (error) {
			await device.close({ shutdown: false })
			throw error
		}
		return device
	}

	// ------------------------------------------------------------------- state

	get isOpen(): boolean {
		return !this.#closed
	}

	get serialNumber(): string | undefined {
		return this.info.serialNumber
	}

	get productName(): string {
		return this.model.name
	}

	get keys(): readonly DockKeyInfo[] {
		return this.model.keys
	}

	get buttons(): readonly DockButtonInfo[] {
		return this.model.buttons
	}

	// -------------------------------------------------------------- life cycle

	/** Wake the display. Called automatically before the first command. */
	async wake(): Promise<void> {
		await this.#ensure()
	}

	/** Put the device to sleep without closing the HID handle. */
	async sleep(): Promise<void> {
		await this.#protocol.sleep()
	}

	/**
	 * Send the heartbeat (`CONNECT`) that keeps the device from rebooting.
	 * Usually not needed, the driver does this automatically.
	 */
	async keepAlive(): Promise<void> {
		await this.#protocol.keepAlive()
	}

	async readFirmwareVersion(): Promise<string | undefined> {
		try {
			const buffer = await this.#hid.getFeatureReport(0x01, 32)
			return buffer.toString('utf8').replace(/\0+$/, '')
		} catch {
			// Not supported by every firmware/OS combination (Windows).
			return undefined
		}
	}

	/**
	 * Close the device. By default the device is shut down (screen cleared, then put to
	 * sleep) like the vendor software does.
	 */
	async close(options: { shutdown?: boolean } = {}): Promise<void> {
		if (this.#closed) return
		this.#closed = true

		if (this.#heartbeat) clearInterval(this.#heartbeat)
		this.#heartbeat = undefined

		try {
			if (options.shutdown ?? true) await this.#protocol.shutdown()
		} catch (error) {
			this.emit('error', error as Error)
		}

		await this.#hid.close().catch(() => undefined)
		this.emit('close')
	}

	// ------------------------------------------------------------------ drawing

	/** Draw a single key and refresh the display. */
	async drawKey(index: number, image: RawImage, options: DrawImageOptions = {}): Promise<void> {
		await this.drawKeys([{ index, image, options }], { refresh: options.noRefresh ? false : undefined })
	}

	/** Draw a single pre-encoded JPEG (must already have the panel resolution). */
	async drawKeyJpeg(index: number, jpeg: Uint8Array, options: { refresh?: boolean } = {}): Promise<void> {
		const key = this.#key(index)
		if (jpeg.length > this.imageFormat.maxBytes) {
			throw new Error(`JPEG is too large: ${jpeg.length} bytes, limit is ${this.imageFormat.maxBytes}`)
		}
		await this.#ensure()
		await this.#protocol.sendImages([{ slot: key.slot, jpeg }], options.refresh ?? this.#autoRefresh)
	}

	/**
	 * Draw several keys at once. All images are encoded first and then sent as one batch
	 * with a single refresh, which is much faster than drawing key by key.
	 */
	async drawKeys(entries: KeyImageEntry[], options: { refresh?: boolean } = {}): Promise<void> {
		if (entries.length === 0) return
		await this.#ensure()

		const frames: ImageFrame[] = []
		for (const entry of entries) {
			const key = this.#key(entry.index)
			const prepared = prepareImage(entry.image, this.imageFormat, entry.options)
			const jpeg = await encodeJpeg(prepared, this.imageFormat, entry.options?.quality)
			frames.push({ slot: key.slot, jpeg })
		}

		await this.#protocol.sendImages(frames, options.refresh ?? this.#autoRefresh)
	}

	/** Send the refresh (`STP`) command, commits images drawn with `refresh: false`. */
	async flush(): Promise<void> {
		await this.#protocol.refresh()
	}

	/** Clear a single key. */
	async clearKey(index: number, options: { refresh?: boolean } = {}): Promise<void> {
		const key = this.#key(index)
		await this.#ensure()
		await this.#protocol.clearSlot(key.slot)
		if (options.refresh ?? this.#autoRefresh) await this.#protocol.refresh()
	}

	/** Clear every key. */
	async clearAll(options: { refresh?: boolean } = {}): Promise<void> {
		await this.#ensure()
		await this.#protocol.clearAll()
		if (options.refresh ?? this.#autoRefresh) await this.#protocol.refresh()
	}

	// --------------------------------------------------------------- brightness

	/** Display brightness, 0..100. */
	async setBrightness(percent: number): Promise<void> {
		await this.#ensure()
		await this.#protocol.setBrightness(percent)
	}

	/** LED brightness, 0..100 (only on models with `capabilities.ledBrightness`). */
	async setLedBrightness(percent: number): Promise<void> {
		if (!this.model.capabilities.ledBrightness || this.model.ledCount === 0) {
			throw new Error(`${this.model.name} has no LED backlight`)
		}
		await this.#ensure()
		await this.#protocol.setLedBrightness(percent)
	}

	// -------------------------------------------------------------------- LEDs

	/** Current LED colours as last sent to the device (M18: 24 entries). */
	get ledColors(): ReadonlyArray<Rgb> {
		return this.#ledColors
	}

	/** Set the colour of every LED individually. Requires exactly `model.ledCount` entries. */
	async setLedColors(colors: ReadonlyArray<readonly [number, number, number]>): Promise<void> {
		if (this.model.ledCount === 0) throw new Error(`${this.model.name} has no addressable LEDs`)
		if (colors.length !== this.model.ledCount) {
			throw new Error(`Expected ${this.model.ledCount} LED colours, got ${colors.length}`)
		}
		await this.#ensure()
		this.#ledColors = colors.map((color) => [color[0], color[1], color[2]] as Rgb)
		await this.#protocol.setLedColors(this.#ledColors)
	}

	/** Set one LED, the other LEDs keep their current colour. */
	async setLed(index: number, color: Rgb): Promise<void> {
		if (index < 0 || index >= this.model.ledCount) {
			throw new Error(`LED index ${index} out of range (0..${this.model.ledCount - 1})`)
		}
		const next = this.#ledColors.map((current, position) => (position === index ? color : current))
		await this.setLedColors(next)
	}

	/** Set several LEDs at once, everything else keeps its current colour. */
	async updateLeds(colors: Record<number, Rgb>): Promise<void> {
		const next = [...this.#ledColors]
		for (const [index, color] of Object.entries(colors)) {
			const position = Number(index)
			if (position < 0 || position >= this.model.ledCount) {
				throw new Error(`LED index ${position} out of range (0..${this.model.ledCount - 1})`)
			}
			next[position] = color
		}
		await this.setLedColors(next)
	}

	/** Set all LEDs to the same colour. */
	async setAllLeds(color: Rgb): Promise<void> {
		await this.setLedColors(Array.from({ length: this.model.ledCount }, () => color))
	}

	// ------------------------------------------------------------------- escape

	/** Send a raw `CRT` command, e.g. `[0x53, 0x54, 0x50]` for a refresh. */
	async sendCommand(command: readonly number[]): Promise<void> {
		await this.#ensure()
		await this.#protocol.sendCommand(command)
	}

	/** Switch device mode (`MOD`). Model specific, only meaningful for devices with `capabilities.modes`. */
	async setMode(mode: number): Promise<void> {
		await this.#ensure()
		await this.#protocol.setMode(mode)
	}

	// ---------------------------------------------------------------- internals

	#key(index: number): DockKeyInfo {
		const key = this.model.keys[index]
		if (!key) throw new Error(`Key index ${index} out of range (0..${this.model.keys.length - 1})`)
		return key
	}

	async #ensure(): Promise<void> {
		if (this.#closed) throw new Error('The device is closed')
		if (this.#initialized) return
		this.#initialized = true
		await this.#protocol.wake()
	}

	#handleReport(data: Buffer): void {
		this.emit('report', data)

		const input = parseInputReport(this.model, data)
		if (!input) {
			if (this.#debug && data.length >= 11) {
				console.debug(`[streamdock] unhandled report code=0x${data[9].toString(16)} state=${data[10]}`)
			}
			return
		}

		const { code, pressed, key, button } = input
		if (pressed === this.#pressedCodes.has(code)) return // some devices repeat the last state
		if (pressed) this.#pressedCodes.add(code)
		else this.#pressedCodes.delete(code)

		const event: DockKeyEvent = key
			? {
					kind: 'key',
					index: key.index,
					id: key.id,
					name: key.name,
					row: key.row,
					column: key.column,
					code,
				}
			: {
					kind: 'button',
					index: button!.index,
					id: button!.id,
					name: button!.name,
					row: button!.row,
					column: button!.column,
					code,
				}

		this.emit(pressed ? 'keyDown' : 'keyUp', event)
	}
}
