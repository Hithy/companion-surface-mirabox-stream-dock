/**
 * Low level Stream Dock HID protocol.
 *
 * Command set reverse engineered from the vendor software and cross checked against:
 *  - the Companion surface module (`src/streamdock.ts` in this repository)
 *  - mirajazz / OpenDeck device plugins (protocol v1..v3 documentation)
 *
 * Every report is a 1024 (or 512) byte payload prefixed with a `0x00` report id.
 * Commands are `CRT` (0x43 0x52 0x54) followed by a 3 letter opcode.
 */

export interface HidWriter {
	write(data: Buffer): Promise<number | void>
}

export interface ImageFrame {
	/** Image slot id (`DockKeyInfo.slot`). */
	slot: number
	/** JPEG payload. */
	jpeg: Uint8Array
}

const CMD_PREFIX = [0x43, 0x52, 0x54, 0x00, 0x00]

/** Opcodes, without the `CRT` prefix. */
export const Opcodes = {
	wake: [0x44, 0x49, 0x53], // DIS
	sleep: [0x48, 0x41, 0x4e], // HAN
	refresh: [0x53, 0x54, 0x50], // STP
	keepAlive: [0x43, 0x4f, 0x4e, 0x4e, 0x45, 0x43, 0x54], // CONNECT
	shutdown: [0x43, 0x4c, 0x45, 0x00, 0x00, 0x44, 0x43], // CLE .. DC
	brightness: (percent: number) => [0x4c, 0x49, 0x47, 0x00, 0x00, percent], // LIG
	ledBrightness: (percent: number) => [0x4c, 0x42, 0x4c, 0x49, 0x47, percent], // LBLIG
	clear: (slot: number) => [0x43, 0x4c, 0x45, 0x00, 0x00, 0x00, slot], // CLE
	mode: (mode: number) => [0x4d, 0x4f, 0x44, 0x00, 0x00, 0x30 + mode], // MOD
	qucmd: (bytes: number[]) => [0x51, 0x55, 0x43, 0x4d, 0x44, ...bytes], // QUCMD
	bootLogo: [0x4c, 0x4f, 0x47, 0x00, 0x11, 0x94, 0x00, 0x01], // LOG
} as const

export interface ProtocolOptions {
	writer: HidWriter
	packetSize: number
	/** Timeout for a single HID write, defaults to 5000ms. */
	writeTimeoutMs?: number
}

/** Serializes every HID write so that heartbeats can never interleave with image chunks. */
export class StreamDockProtocol {
	readonly #writer: HidWriter
	readonly #packetSize: number
	readonly #writeTimeoutMs: number
	#queue: Promise<void> = Promise.resolve()

	constructor(options: ProtocolOptions) {
		this.#writer = options.writer
		this.#packetSize = options.packetSize
		this.#writeTimeoutMs = options.writeTimeoutMs ?? 5000
	}

	get packetSize(): number {
		return this.#packetSize
	}

	async #enqueue<T>(operation: () => Promise<T>): Promise<T> {
		const result = this.#queue.then(operation)
		// Keep the chain alive even if a write fails.
		this.#queue = result.then(
			() => undefined,
			() => undefined,
		)
		return result
	}

	async #writeReport(payload: Uint8Array, offset: number, reportSize: number): Promise<void> {
		const report = Buffer.alloc(reportSize + 1) // leading 0x00 = HID report id
		const length = Math.min(payload.length - offset, reportSize)
		report.set(payload.subarray(offset, offset + length), 1)

		let timer: NodeJS.Timeout | undefined
		const timeout = new Promise<never>((_, reject) => {
			timer = setTimeout(
				() => reject(new Error(`HID write timed out after ${this.#writeTimeoutMs}ms`)),
				this.#writeTimeoutMs,
			)
			timer.unref?.()
		})

		try {
			const written = await Promise.race([this.#writer.write(report), timeout])
			if (typeof written === 'number' && written !== 0 && written !== report.length) {
				throw new Error(`Short HID write: ${written} of ${report.length} bytes`)
			}
		} finally {
			if (timer) clearTimeout(timer)
		}
	}

	/** Send `CRT <command>` padded to the report size. */
	async sendCommand(command: readonly number[] | Uint8Array): Promise<void> {
		const payload = Buffer.concat([Buffer.from(CMD_PREFIX), Buffer.from(command)])
		await this.#enqueue(async () => this.#writeReport(payload, 0, this.#packetSize))
	}

	/** Send a raw payload (no `CRT` prefix) split into report sized chunks. */
	async sendRawData(data: Uint8Array): Promise<void> {
		await this.#enqueue(async () => {
			for (let offset = 0; offset < data.length; offset += this.#packetSize) {
				await this.#writeReport(data, offset, this.#packetSize)
			}
		})
	}

	/**
	 * Send one or more key images. All `BAT` commands and image chunks are sent as one
	 * queued unit, optionally followed by a single `STP` refresh.
	 */
	async sendImages(frames: ImageFrame[], refresh = true): Promise<void> {
		await this.#enqueue(async () => {
			for (const frame of frames) {
				const size = frame.jpeg.length
				if (size === 0) continue

				const command = Buffer.from([
					...CMD_PREFIX,
					0x42,
					0x41,
					0x54, // BAT
					(size >>> 24) & 0xff,
					(size >>> 16) & 0xff,
					(size >>> 8) & 0xff,
					size & 0xff,
					frame.slot,
				])
				await this.#writeReport(command, 0, this.#packetSize)

				for (let offset = 0; offset < size; offset += this.#packetSize) {
					await this.#writeReport(frame.jpeg, offset, this.#packetSize)
				}
			}

			if (refresh) {
				await this.#writeReport(Buffer.from([...CMD_PREFIX, ...Opcodes.refresh]), 0, this.#packetSize)
			}
		})
	}

	async wake(): Promise<void> {
		await this.sendCommand(Opcodes.wake)
	}

	async sleep(): Promise<void> {
		await this.sendCommand(Opcodes.sleep)
	}

	async refresh(): Promise<void> {
		await this.sendCommand(Opcodes.refresh)
	}

	async keepAlive(): Promise<void> {
		await this.sendCommand(Opcodes.keepAlive)
	}

	/** Put the device into its idle state (clear screen, stop refreshing, sleep). */
	async shutdown(): Promise<void> {
		await this.#enqueue(async () => {
			await this.#writeReport(Buffer.from([...CMD_PREFIX, ...Opcodes.shutdown]), 0, this.#packetSize)
			await this.#writeReport(Buffer.from([...CMD_PREFIX, ...Opcodes.sleep]), 0, this.#packetSize)
		})
	}

	async clearAll(): Promise<void> {
		await this.sendCommand(Opcodes.clear(0xff))
	}

	async clearSlot(slot: number): Promise<void> {
		await this.sendCommand(Opcodes.clear(slot))
	}

	async setBrightness(percent: number): Promise<void> {
		await this.sendCommand(Opcodes.brightness(Math.min(100, Math.max(0, Math.round(percent)))))
	}

	async setLedBrightness(percent: number): Promise<void> {
		await this.sendCommand(Opcodes.ledBrightness(Math.min(100, Math.max(0, Math.round(percent)))))
	}

	async setLedColors(colors: ReadonlyArray<readonly [number, number, number]>): Promise<void> {
		const payload: number[] = [0x53, 0x45, 0x54, 0x4c, 0x42] // SETLB
		for (const [r, g, b] of colors) payload.push(r & 0xff, g & 0xff, b & 0xff)
		await this.sendCommand(payload)
	}

	async setMode(mode: number): Promise<void> {
		await this.sendCommand(Opcodes.mode(mode))
	}

	/** Firmware configuration command, used e.g. for the N4 Pro vibration motor. */
	async sendQuCmd(bytes: number[]): Promise<void> {
		await this.sendCommand(Opcodes.qucmd(bytes))
	}

	/** Replace the boot logo (raw BGR payload, device specific resolution). */
	async sendBootLogo(payload: Uint8Array): Promise<void> {
		await this.#enqueue(async () => {
			await this.#writeReport(Buffer.from([...CMD_PREFIX, ...Opcodes.bootLogo]), 0, this.#packetSize)
			for (let offset = 0; offset < payload.length; offset += this.#packetSize) {
				await this.#writeReport(payload, offset, this.#packetSize)
			}
		})
	}
}
