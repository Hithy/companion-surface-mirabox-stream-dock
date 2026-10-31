/**
 * Standalone driver for Mirabox / VSDinside / Ajazz "Stream Dock" devices.
 *
 * The driver talks to the hardware directly via USB HID and has no dependency on
 * Companion, so it can be embedded in any Node.js application.
 *
 * ```ts
 * import { StreamDockDevice } from './dist/driver/index.js'
 *
 * const dock = await StreamDockDevice.open() // first connected device
 * console.log(dock.model.name, dock.keys.length, dock.buttons.length)
 *
 * dock.on('keyDown', async (event) => {
 *   console.log(event.name)
 *   const { width, height } = dock.imageFormat
 *   const rgb = new Uint8Array(width * height * 3).fill(0x30)
 *   await dock.drawKey(event.index, { data: rgb, width, height, channels: 3 })
 * })
 *
 * await dock.setBrightness(80)
 * await dock.setAllLeds([0, 40, 90])
 * ```
 *
 * See `docs/DRIVER.md` for the full API and protocol notes.
 */

export { StreamDockDevice } from './dock.js'
export type { KeyImageEntry } from './dock.js'

export { StreamDockProtocol, Opcodes } from './protocol.js'
export type { HidWriter, ImageFrame, ProtocolOptions } from './protocol.js'

export { parseInputReport } from './input.js'
export type { ParsedInput } from './input.js'

export { listDocks, findDock, watchDocks } from './hid.js'
export type { DockWatcher, DockWatcherEvents } from './hid.js'

export { DRIVER_MODELS, findModel, findModelByProductName, getModel } from './models/index.js'
export { M18_MODEL, VSDINSIDE_M18_MODEL, M18_MODELS } from './models/m18.js'

export { encodeJpeg, prepareImage, resizeImage, rotateImage, mirrorImage, toRgbImage, blitImage } from './image.js'
export type { RgbImage } from './image.js'

export type {
	DockButtonInfo,
	DockEvents,
	DockKeyEvent,
	DockKeyInfo,
	DiscoveredDock,
	DrawImageOptions,
	HidDeviceInfo,
	ImageFit,
	ModelUsbId,
	OpenOptions,
	RawImage,
	Rgb,
	Rotation,
	StreamDockCapabilities,
	StreamDockImageFormat,
	StreamDockModel,
	WatchOptions,
} from './types.js'
