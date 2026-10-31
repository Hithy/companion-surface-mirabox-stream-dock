import { devicesAsync } from 'node-hid'
import type { Device } from 'node-hid'
import type { DiscoveredDock, HidDeviceInfo, WatchOptions } from './types.js'
import { findModel } from './models/index.js'
import { EventEmitter } from 'node:events'

/** The vendor interface uses usage page 0xffa0 (65440), usage 1. */
const VENDOR_USAGE_PAGE = 0xffa0
const VENDOR_USAGE = 0x01

function toInfo(device: Device): HidDeviceInfo {
	return {
		path: device.path ?? '',
		vendorId: device.vendorId,
		productId: device.productId,
		serialNumber: device.serialNumber,
		product: device.product,
		manufacturer: device.manufacturer,
		interface: device.interface ?? -1,
		usagePage: device.usagePage,
		usage: device.usage,
	}
}

/**
 * Score a HID interface: the vendor (control) interface is the one we can write to,
 * secondary interfaces expose keyboard/mouse emulation and must not be opened.
 */
function interfaceScore(device: Device): number {
	if (device.usagePage === VENDOR_USAGE_PAGE && device.usage === VENDOR_USAGE) return 100
	if (device.interface === 0) return 50
	if (device.interface === -1 || device.interface === undefined) return 10
	return -1
}

export async function listDocks(options: { includeAllInterfaces?: boolean } = {}): Promise<DiscoveredDock[]> {
	const devices = await devicesAsync()

	const best = new Map<string, { device: Device; score: number; model: NonNullable<ReturnType<typeof findModel>> }>()

	for (const device of devices) {
		const model = findModel(device.vendorId, device.productId)
		if (!model) continue

		const score = interfaceScore(device)
		if (score < 0 && !options.includeAllInterfaces) continue

		const path = device.path ?? `${device.vendorId}:${device.productId}:${device.serialNumber ?? ''}`
		const current = best.get(path)
		if (!current || score > current.score) {
			best.set(path, { device, score: Math.max(score, 0), model })
		}
	}

	return Array.from(best.values())
		.sort((a, b) => (a.device.serialNumber ?? '').localeCompare(b.device.serialNumber ?? ''))
		.map(({ device, model }) => ({ model, info: toInfo(device) }))
}

/** Find the first connected device, optionally filtered by model id or serial number. */
export async function findDock(
	filter: { modelId?: string; serialNumber?: string } = {},
): Promise<DiscoveredDock | undefined> {
	const docks = await listDocks()
	return docks.find(
		(dock) =>
			(!filter.modelId || dock.model.id === filter.modelId) &&
			(!filter.serialNumber || dock.info.serialNumber === filter.serialNumber),
	)
}

export interface DockWatcherEvents {
	attach: [DiscoveredDock]
	detach: [DiscoveredDock]
	error: [Error]
}

export interface DockWatcher extends EventEmitter<DockWatcherEvents> {
	stop(): void
}

/**
 * Polls the HID bus and emits `attach`/`detach` events. node-hid has no hotplug support,
 * polling is the portable option.
 */
export function watchDocks(options: WatchOptions = {}): DockWatcher {
	const intervalMs = options.intervalMs ?? 2000
	const emitter = new EventEmitter<DockWatcherEvents>() as DockWatcher
	let known = new Map<string, DiscoveredDock>()
	let stopped = false
	let timer: NodeJS.Timeout | undefined

	const poll = async (): Promise<void> => {
		if (stopped) return
		try {
			const docks = await listDocks({ includeAllInterfaces: options.includeAllInterfaces })
			const current = new Map(docks.map((dock) => [dock.info.path, dock]))

			for (const [path, dock] of current) {
				if (!known.has(path)) emitter.emit('attach', dock)
			}
			for (const [path, dock] of known) {
				if (!current.has(path)) emitter.emit('detach', dock)
			}

			known = current
		} catch (error) {
			emitter.emit('error', error as Error)
		}
	}

	emitter.stop = () => {
		stopped = true
		if (timer) clearInterval(timer)
		emitter.removeAllListeners()
	}

	// Prime the known list so already connected devices are not reported as `attach`.
	void listDocks({ includeAllInterfaces: options.includeAllInterfaces })
		.then((docks) => {
			known = new Map(docks.map((dock) => [dock.info.path, dock]))
			if (stopped) return
			timer = setInterval(() => void poll(), intervalMs)
			timer.unref?.()
		})
		.catch((error: unknown) => emitter.emit('error', error as Error))

	return emitter
}
