/**
 * Public types for the standalone Stream Dock driver.
 *
 * This module intentionally has no dependency on `@companion-surface/base`, so it can be
 * consumed by any Node.js application (Electron, CLI, service, ...).
 */

export type Rgb = [number, number, number]

/** Pixel data layout accepted by the driver. `data` is tightly packed, row-major, top-left origin. */
export interface RawImage {
	data: Uint8Array
	width: number
	height: number
	/** 3 = RGB, 4 = RGBA. Alpha is composited over the background colour. */
	channels: 3 | 4
}

export type Rotation = 0 | 90 | 180 | 270

/** How an image is fitted into the (usually square) key area. */
export type ImageFit = 'contain' | 'cover' | 'stretch'

export interface DrawImageOptions {
	/** Rotation applied to the source image before it is fitted and encoded. Defaults to the model value. */
	rotation?: Rotation
	/** Mirror the source image horizontally. */
	mirrorX?: boolean
	/** Mirror the source image vertically. */
	mirrorY?: boolean
	/** Fitting mode, defaults to `contain` (letterboxed on the background colour). */
	fit?: ImageFit
	/** Background colour used for padding and for transparent source pixels. Defaults to black. */
	background?: Rgb
	/** Starting JPEG quality, defaults to 90 and is lowered automatically to fit the size limit. */
	quality?: number
	/** Do not send the refresh (STP) command yet. Call {@link StreamDockDevice.flush} later. */
	noRefresh?: boolean
}

export interface ModelUsbId {
	vendorId: number
	productIds: number[]
}

export interface DockKeyInfo {
	/** 0-based index in {@link StreamDockModel.keys}. */
	index: number
	/** Key code reported by the device in input reports (byte 9). */
	inputId: number
	/** Image slot id used by the `BAT` command. */
	slot: number
	name: string
	row: number
	column: number
	/** Stable control id, `"row/column"` (same convention as the Companion surface module). */
	id: string
}

export interface DockButtonInfo {
	/** 0-based index in {@link StreamDockModel.buttons}. */
	index: number
	/** Button code reported by the device in input reports (byte 9). */
	code: number
	name: string
	row: number
	column: number
	/** Stable control id, `"row/column"` (same convention as the Companion surface module). */
	id: string
}

export interface StreamDockCapabilities {
	/** The device reports both press and release events (protocol v3). */
	keyUp: boolean
	/** `SETLB` accepts one RGB triple per LED. */
	ledColors: boolean
	/** `LBLIG` sets the LED brightness independently of the display brightness. */
	ledBrightness: boolean
	/** `HAN` can be used to put the device to sleep. */
	sleep: boolean
	/** `LOG` can be used to replace the boot logo. */
	bootLogo: boolean
	/** `MOD` can be used to switch device modes. */
	modes: boolean
	/** The firmware version can be read back (not available on Windows). */
	firmwareVersion: boolean
}

export interface StreamDockImageFormat {
	/** Panel resolution of a single key in pixels. */
	width: number
	height: number
	/** Maximum JPEG payload per key accepted by the device. */
	maxBytes: number
	/** Default rotation applied to incoming images. */
	rotation: Rotation
	mirrorX: boolean
	mirrorY: boolean
}

export interface StreamDockModel {
	/** Stable model id, e.g. `m18`. */
	id: string
	/** Human readable product name. */
	name: string
	usbIds: ModelUsbId[]
	/** HID report payload size (without the leading report id). */
	packetSize: number
	image: StreamDockImageFormat
	keys: DockKeyInfo[]
	buttons: DockButtonInfo[]
	/** Number of individually addressable LEDs, 0 if the device has none. */
	ledCount: number
	capabilities: StreamDockCapabilities
}

/** Subset of the node-hid device description that the driver exposes. */
export interface HidDeviceInfo {
	path: string
	vendorId: number
	productId: number
	serialNumber?: string
	product?: string
	manufacturer?: string
	interface: number
	usagePage?: number
	usage?: number
}

export interface DiscoveredDock {
	/** Model that matched the USB id. */
	model: StreamDockModel
	info: HidDeviceInfo
}

export interface DockKeyEvent {
	kind: 'key' | 'button'
	/** Index within `model.keys` or `model.buttons`. */
	index: number
	/** `"row/column"`, matches the Companion surface control id. */
	id: string
	name: string
	row: number
	column: number
	/** Raw device code that produced the event. */
	code: number
}

export interface DockEvents {
	/** A LCD key (or a plain button) was pressed. */
	keyDown: [DockKeyEvent]
	/** A LCD key (or a plain button) was released. */
	keyUp: [DockKeyEvent]
	/** Raw input report, useful while reverse engineering. */
	report: [Buffer]
	/** Non fatal error, e.g. a failed heartbeat. */
	error: [Error]
	/** The device handle was closed. */
	close: []
}

export interface OpenOptions {
	/** Open a specific device by HID path. */
	path?: string
	/** Open a specific device by serial number. */
	serialNumber?: string
	/** Open the n-th discovered device (default 0). */
	index?: number
	/** Heartbeat interval in ms, `0` disables the heartbeat. Defaults to 8000. */
	heartbeatMs?: number
	/** Send the refresh (STP) command right after a draw, defaults to true. */
	autoRefresh?: boolean
	/** Also consider secondary HID interfaces (keyboard emulation), needed for diagnostics. */
	includeAllInterfaces?: boolean
}

export interface WatchOptions {
	/** Poll interval in ms, defaults to 2000. */
	intervalMs?: number
	/** Include secondary HID interfaces (keyboard emulation). Defaults to false. */
	includeAllInterfaces?: boolean
}
