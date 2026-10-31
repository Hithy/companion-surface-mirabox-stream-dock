import type { DockButtonInfo, DockKeyInfo, Rotation, StreamDockCapabilities, StreamDockModel } from '../types.js'
import { AllModels } from '../../models/list.js'
import type { StreamDockModelDefinition } from '../../models/list.js'
import { M18_MODELS } from './m18.js'

/**
 * The M18 definitions in `src/models/M18V3.ts` are Companion specific (they only describe
 * what Companion needs). The driver keeps its own M18 descriptor so it can carry the
 * additional hardware details (LED count, both product ids, panel resolution).
 */
const SPECIAL_MODELS: StreamDockModel[] = M18_MODELS

function mapRotation(iconRotation: number): Rotation {
	// `iconRotation` in the Companion models uses 0/90/-90/180/270. The Companion renderer
	// maps 90 to a 270 deg clockwise rotation (and -90 to 90), 270 is never applied.
	// These values are best effort, use `tools/m18-probe.mts --test-pattern` to verify.
	if (iconRotation === 90) return 270
	if (iconRotation === -90) return 90
	if (iconRotation === 180) return 180
	return 0
}

function toDriverModel(def: StreamDockModelDefinition): StreamDockModel | null {
	const lcds = def.outputs.filter((output) => output.type === 'lcd')
	if (lcds.length === 0) return null

	const leds = def.outputs.filter((output) => output.type === 'led')

	// Sort row major, so that key index follows the physical ordering.
	const sortedLcds = [...lcds].sort((a, b) => a.row - b.row || a.column - b.column)

	const keys: DockKeyInfo[] = sortedLcds.map((output, index) => {
		// The input code and the image slot id can differ (see the M18/293V3 slot remap),
		// take the button that sits on the same grid position as the input code.
		const input = def.inputs.find(
			(candidate) =>
				candidate.row === output.row &&
				candidate.column === output.column &&
				(candidate.type === 'button' || candidate.type === 'push'),
		)

		return {
			index,
			inputId: input?.id ?? output.id,
			slot: output.id,
			name: output.name,
			row: output.row,
			column: output.column,
			id: `${output.row}/${output.column}`,
		}
	})

	// Plain buttons are inputs that are not covered by an LCD and are not rotary/swipe controls.
	const buttons: DockButtonInfo[] = def.inputs
		.filter((input) => {
			if (input.type !== 'button' && input.type !== 'push') return false
			return !lcds.some((output) => output.row === input.row && output.column === input.column)
		})
		.sort((a, b) => a.row - b.row || a.column - b.column)
		.map((input, index) => ({
			index,
			code: input.id,
			name: input.name,
			row: input.row,
			column: input.column,
			id: `${input.row}/${input.column}`,
		}))

	const firstLcd = sortedLcds[0]
	const capabilities: StreamDockCapabilities = {
		keyUp: true,
		ledColors: leds.length > 0,
		ledBrightness: leds.length > 0,
		sleep: true,
		bootLogo: false,
		modes: false,
		firmwareVersion: process.platform !== 'win32',
	}

	return {
		id: def.productName
			.toLowerCase()
			.replace(/[^a-z0-9]+/g, '-')
			.replace(/(^-|-$)/g, ''),
		name: def.productName,
		usbIds: def.usbIds,
		packetSize: def.packetSize ?? 1024,
		image: {
			width: firstLcd.resolutionx,
			height: firstLcd.resolutiony,
			maxBytes: 10240,
			rotation: mapRotation(def.iconRotation),
			mirrorX: false,
			mirrorY: false,
		},
		keys,
		buttons,
		ledCount: leds.length,
		capabilities,
	}
}

function buildModelList(): StreamDockModel[] {
	const models = [...SPECIAL_MODELS]
	const coveredUsbIds = new Set(
		SPECIAL_MODELS.flatMap((model) =>
			model.usbIds.flatMap((id) => id.productIds.map((pid) => `${id.vendorId}:${pid}`)),
		),
	)

	for (const def of AllModels) {
		// The M18 models are described above with the extra hardware details, skip every
		// Companion definition whose USB ids are already fully covered.
		const allIdsKnown = def.usbIds.every((id) =>
			id.productIds.every((pid) => coveredUsbIds.has(`${id.vendorId}:${pid}`)),
		)
		if (allIdsKnown) continue

		const model = toDriverModel(def)
		if (!model) continue

		// Do not register the same USB id twice.
		const ids = model.usbIds
			.map((id) => ({
				vendorId: id.vendorId,
				productIds: id.productIds.filter((pid) => !coveredUsbIds.has(`${id.vendorId}:${pid}`)),
			}))
			.filter((id) => id.productIds.length > 0)
		if (ids.length === 0) continue

		for (const id of ids) {
			for (const pid of id.productIds) coveredUsbIds.add(`${id.vendorId}:${pid}`)
		}

		models.push({ ...model, usbIds: ids })
	}

	// Several revisions share a product name (e.g. three HSV 293S variants), make the ids unique.
	const usedIds = new Set<string>()
	for (const model of models) {
		let id = model.id
		if (usedIds.has(id)) {
			const suffix = model.usbIds.flatMap((usbId) => usbId.productIds.map((pid) => pid.toString(16))).join('-')
			id = `${model.id}-${suffix}`
		}
		usedIds.add(id)
		model.id = id
	}

	return models
}

/** Every model the driver knows about. */
export const DRIVER_MODELS: StreamDockModel[] = buildModelList()

export function findModel(vendorId: number, productId: number): StreamDockModel | undefined {
	return DRIVER_MODELS.find((model) =>
		model.usbIds.some((id) => id.vendorId === vendorId && id.productIds.includes(productId)),
	)
}

export function getModel(modelId: string): StreamDockModel | undefined {
	return DRIVER_MODELS.find((model) => model.id === modelId)
}

export function findModelByProductName(name: string): StreamDockModel | undefined {
	const needle = name.toLowerCase()
	return DRIVER_MODELS.find((model) => model.name.toLowerCase() === needle)
}
