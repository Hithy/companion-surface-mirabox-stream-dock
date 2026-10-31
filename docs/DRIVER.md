# Stream Dock driver (standalone)

> 中文版（面向上层应用开发）：[DRIVER.zh-CN.md](./DRIVER.zh-CN.md)

A small, dependency-light driver for Mirabox / VSDinside / Ajazz "Stream Dock" hardware.
It talks to the device over USB HID directly and can be embedded into any Node.js
application — no Companion required.

The code lives in `src/driver/`, the same repository also contains a Companion _surface_
module (`src/main.ts`, `src/instance.ts`) that uses its own copy of the protocol. The two
do not depend on each other:

```
src/driver/                  <- reusable driver (this document)
  types.ts                   public types
  protocol.ts                HID framing + CRT command set
  image.ts                   resize / rotate / fit / JPEG encoding
  hid.ts                     discovery (node-hid) + hotplug polling
  dock.ts                    StreamDockDevice: high level API + events
  index.ts                   public entry point
  models/m18.ts              M18 hardware description (PIDs, layout, LEDs)
  models/index.ts            model registry (M18 + all models known to Companion)

src/main.ts, src/instance.ts Companion surface module (unchanged)
src/streamdock.ts            protocol copy used by the Companion module
```

## Why two protocol implementations?

The Companion module was written against the Companion surface API and has some
Companion-specific behaviour (icon rotation, LED modes, pincode map). The driver is the
same wire protocol with a generic API. Keeping them apart means the Companion integration
keeps working exactly as before while an application can consume `driver/` on its own.

## Build

```bash
yarn            # or: npm install --legacy-peer-deps
yarn build      # emits dist/ (ESM + .d.ts)
```

Requirements: Node.js 22 (the repo pins `^22.18`), `node-hid` and `@julusian/jpeg-turbo`
ship prebuilt binaries for Windows/macOS/Linux.

Consume it from another package:

```jsonc
// package.json of your application
{ "dependencies": { "companion-surface-mirabox-stream-dock": "file:../companion-surface-mirabox-stream-dock" } }
```

```ts
import { StreamDockDevice } from 'companion-surface-mirabox-stream-dock/driver'
```

## Quick start

```ts
import { StreamDockDevice } from './dist/driver/index.js'

const dock = await StreamDockDevice.open() // first connected device

dock.on('keyDown', async (event) => {
	console.log(event.kind, event.index, event.id, event.name)
})

const rgb = new Uint8Array(dock.imageFormat.width * dock.imageFormat.height * 3).fill(0x40)
await dock.drawKey(0, { data: rgb, width: dock.imageFormat.width, height: dock.imageFormat.height, channels: 3 })
await dock.setBrightness(80)
await dock.setAllLeds([0, 40, 90])

// Ctrl+C handler
await dock.close() // clears the screen and puts the device to sleep
```

Two runnable examples are included:

```bash
yarn probe list                     # enumerate devices
yarn probe info --no-shutdown       # model, capabilities, firmware, key map
yarn probe listen 20                # print key/button events (--raw for hex)
yarn probe selftest                 # offline input-report parser checks
yarn probe pattern --hold 20        # draw a verification pattern on all keys
yarn probe led-rainbow --hold 10    # 24 LEDs, one colour each
yarn demo                           # animated reference application
```

The vendor software ("Stream Dock") must be fully closed, otherwise the HID device is
busy. Only one process may use the device at a time (no Companion, no vendor app).

### Package manager note

The repository pins `packageManager: yarn@4.18.1`. If `yarn` is Yarn 1.x (or Corepack is
not enabled) the scripts fail before doing anything. Either enable Yarn 4
(`npm i -g @yarnpkg/cli-dist@4.18.1` and then `yarn install`), or use npm, which also
works because the scripts no longer rely on Yarn's `run` helper:

```bash
npm install --legacy-peer-deps
npm run probe -- pattern --hold 30      # note the `--` before script arguments
npm run probe -- listen 20
npm run demo
npm run build
```

## API

### Discovery

```ts
StreamDockDevice.list(): Promise<DiscoveredDock[]>   // { model, info } per device
StreamDockDevice.open(options?): Promise<StreamDockDevice>
listDocks(options?): Promise<DiscoveredDock[]>
findDock({ modelId?, serialNumber? }): Promise<DiscoveredDock | undefined>
watchDocks({ intervalMs? }): DockWatcher             // 'attach' | 'detach' | 'error'
```

`open()` options: `path`, `serialNumber`, `index` (default 0), `heartbeatMs` (default
`8000`, `0` disables), `autoRefresh` (default `true`).

### Properties

| Property                                | Description                                                                             |
| --------------------------------------- | --------------------------------------------------------------------------------------- |
| `model`                                 | `StreamDockModel` (keys, buttons, LED count, image format, capabilities)                |
| `info`                                  | HID device description (`path`, `serialNumber`, `vendorId`, …)                          |
| `imageFormat`                           | **mutable** `{ width, height, maxBytes, rotation, mirrorX, mirrorY }` used when drawing |
| `keys` / `buttons`                      | readonly layout descriptors                                                             |
| `isOpen`, `serialNumber`, `productName` | convenience getters                                                                     |

### Drawing

```ts
drawKey(index, image: RawImage, options?: DrawImageOptions): Promise<void>
drawKeyJpeg(index, jpeg: Uint8Array, options?: { refresh?: boolean }): Promise<void>
drawKeys(entries: { index, image, options? }[], options?: { refresh?: boolean }): Promise<void>
clearKey(index, options?): Promise<void>
clearAll(options?): Promise<void>
flush(): Promise<void>          // STP
```

`drawKeys()` encodes everything first and sends one batch with a single refresh — use it
for animation frames instead of calling `drawKey()` in a loop.

`RawImage` is tightly packed, row-major, top-left origin:

```ts
{ width: number, height: number, channels: 3 | 4, data: Uint8Array }
```

`DrawImageOptions`: `rotation` (`0|90|180|270`), `mirrorX`, `mirrorY`, `fit`
(`contain` default, `cover`, `stretch`), `background` (padding / alpha compositing),
`quality` (JPEG, default 90, lowered automatically until the payload fits `maxBytes`),
`noRefresh`.

Data flow: `RawImage → mirror → rotate → scale → fit/pad → JPEG (≤ maxBytes) →
BAT + chunked payload → STP`.

### LEDs and brightness

```ts
setBrightness(percent: 0..100): Promise<void>
setLedBrightness(percent: 0..100): Promise<void>
setLedColors(colors: [r, g, b][]): Promise<void>   // exactly model.ledCount entries
setLed(index, rgb): Promise<void>                  // one LED, others keep their colour
updateLeds({ [index]: rgb }): Promise<void>        // several LEDs at once
setAllLeds(rgb): Promise<void>
ledColors: ReadonlyArray<[r, g, b]>                // last state sent to the device
```

The driver keeps the LED state in memory, so `setLed()` / `updateLeds()` only need the
changes — the full `SETLB` payload is rebuilt and sent for you.

The M18 has 24 individually addressable RGB LEDs (`SETLB` + 24 × RGB). Values are sent
as-is; the Companion module additionally applies a `pow(x, 0.75)` gamma curve, the driver
does not.

### Events

```ts
dock.on('keyDown', (event) => {})
dock.on('keyUp', (event) => {})
dock.on('report', (buffer) => {}) // raw HID input reports
dock.on('error', (error) => {})
dock.on('close', () => {})
```

`event` = `{ kind: 'key' | 'button', index, id, name, row, column, code }`.
`id` is `"row/column"`, the same control id the Companion surface module uses, so a
Companion layout can be ported 1:1. `index` is 0-based within `keys` or `buttons`.

The M18 reports press **and** release for every control. Repeated identical reports are
filtered out; if a device only reports presses, `keyUp` never fires.

### Escape hatches

```ts
sendCommand([0x53, 0x54, 0x50]) // raw CRT command, queued
setMode(mode) // MOD
readFirmwareVersion() // undefined on Windows
```

### Concurrency and lifecycle

All HID writes go through a promise queue, so a heartbeat can never be interleaved with
image chunks. `drawKeys()` is a single queue unit: 15 keys = 16 `BAT` commands + data +
one `STP`, sent back to back.

On `open()` the driver sends `DIS` (wake) and starts an 8 s heartbeat (`CONNECT`) — Stream
Dock firmware reboots if it does not receive one. Every later command reuses the same
initialised handle.

`close({ shutdown: true })` (default) sends `CLE .. DC` + `HAN` and closes the handle. Use
`close({ shutdown: false })` to leave the current picture on the device.

## Models

`DRIVER_MODELS` contains:

- `M18_MODEL` — `6603:1012` (EN) and `6603:1009` (CN), 15 LCD keys, 3 buttons, 24 LEDs,
  64×64 panel, 1024 byte reports.
- `VSDINSIDE_M18_MODEL` — `5548:1000`, identical layout, different vendor id.
- One model per stream dock definition in `src/models/*.ts` (293V3, N4, N3, XL, AKP03E,
  AKP05E, N1, S15, CN002, …), generated by `src/driver/models/index.ts`. Keys, slots and
  input codes are derived from those definitions; rotary encoders and swipe gestures are
  **not** exposed yet (only keys and plain buttons).

To add a device, add a `StreamDockModel` object (or a `src/models/*.ts` definition).

### M18 key map

| Key        | index | input code    | image slot    |
| ---------- | ----- | ------------- | ------------- |
| top-left   | 0     | `0x01`        | `0x0b`        |
| top row, … | 1–4   | `0x02`–`0x05` | `0x0c`–`0x0f` |
| middle row | 5–9   | `0x06`–`0x0a` | `0x06`–`0x0a` |
| bottom row | 10–14 | `0x0b`–`0x0f` | `0x01`–`0x05` |

Bottom buttons (no display, outside the 5×3 grid): `0x25` (left, `3/1`), `0x30`
(middle, `3/2`), `0x31` (right, `3/3`). The image slot ids are **not** the key codes —
this remap is confirmed by two independent implementations and is where a wrong layout
usually comes from.

## Protocol reference

Every report is `0x00` (report id) + payload, zero padded to `packetSize` (1024 for
protocol v3, 512 for older devices). Commands are `CRT` (`43 52 54`) + a 3 byte opcode.

| Opcode    | Bytes                            | Meaning                                  |
| --------- | -------------------------------- | ---------------------------------------- |
| `DIS`     | `44 49 53`                       | wake / display on                        |
| `HAN`     | `48 41 4e`                       | sleep                                    |
| `STP`     | `53 54 50`                       | refresh (commit drawn images)            |
| `CONNECT` | `43 4f 4e 4e 45 43 54`           | heartbeat, must be sent periodically     |
| `BAT`     | `42 41 54` + `u32 size` + `slot` | next report(s) contain a JPEG for `slot` |
| `CLE`     | `43 4c 45 00 00 00 slot`         | clear one key (`slot = 0xff`: all)       |
| `LIG`     | `4c 49 47 00 00 pct`             | display brightness 0–100                 |
| `LBLIG`   | `4c 42 4c 49 47 pct`             | LED brightness 0–100                     |
| `SETLB`   | `53 45 54 4c 42` + RGB…          | one RGB triple per LED                   |
| `MOD`     | `4d 4f 44 00 00 30+mode`         | switch device mode                       |
| `QUCMD`   | `51 55 43 4d 44` + bytes         | vendor config (e.g. N4 Pro vibration)    |
| `LOG`     | `4c 4f 47 00 11 94 00 01`        | boot logo, followed by raw BGR data      |

Input reports (device → host) are **512 bytes** on the M18 and use the same `ACK … OK`
prefix as command acknowledgements:

```
0..2  'ACK'    3..4  00 00    5..6  'OK'    7..8  00 00
9     key code (0x01..0x0f = LCD keys, 0x25/0x30/0x31 = bottom buttons)
10    state (0x01 = pressed, 0x00 = released)    11.. padding
```

Do **not** filter on the `ACK` prefix — key events use it too (this was a bug in the first
revision of the driver, `parseInputReport()` in `src/driver/input.ts` now handles it and
`probe selftest` keeps it covered).

Image payloads are sent as plain reports (no `CRT` prefix), `packetSize` bytes each, then
a single `STP` commits the frame. `BAT` takes a 32 bit big-endian size; the M18 rejects
(key) payloads above 10 KB, hence the automatic quality reduction in `image.ts`.

Sources: the command set comes from this repository's Companion module, cross checked
against the independently reverse engineered `mirajazz`/OpenDeck protocol implementations
(`DIS`/`HAN`/`STP`/`BAT`/`CLE`/`LIG`/`LBLIG`/`SETLB`/`MOD`/`QUCMD`/`LOG`). The official
StreamDock **plugin** SDK (`StreamDock-Plugin-SDK`) has no HID layer — plugins talk
WebSocket JSON to the vendor host, which owns the device — so it cannot be used to drive
the hardware. It is still the reference for plugin level concepts (actions, property
inspector, `Knob` controller naming) if you later also ship a StreamDock plugin.

## Verifying the hardware profile

Panel resolution, rotation and the slot remap are the three things that vary between
firmware revisions. For the M18 EN (`6603:1012`) they are verified on hardware:

- image: **64×64**, rotation 0, no mirroring, slot map as in the table above
- input: all 15 LCD keys (`0x01..0x0f`) and all 3 bottom buttons (`0x25/0x30/0x31`)
  report press **and** release → 18 controls, 36 events, `id` = `row/column`
  `tools/m18-probe.mts pattern` draws, for every key, its number and four corner markers:

* white top-left, red top-right, green bottom-left, blue bottom-right
* key `n` must show `n` (1–15)

```bash
yarn probe pattern --hold 30
yarn probe pattern --size 60 --hold 30     # 60 is wrong on this hardware, useful as a control
yarn probe pattern --rotate 90 --hold 30   # try 90/180/270 if the picture is rotated
```

If numbers are permuted, only `M18_SLOTS` in `src/driver/models/m18.ts` needs to change.
If the picture is upside down / mirrored, set `rotation` / `mirrorX` / `mirrorY` in the
same file (or per application via `dock.imageFormat`).

## Troubleshooting

| Symptom                                                      | Cause                                                                                           |
| ------------------------------------------------------------ | ----------------------------------------------------------------------------------------------- |
| `Could not open the Stream Dock … open_device: (0x00000005)` | another process owns the device (vendor software, Companion)                                    |
| `No Stream Dock found`                                       | wrong cable/port, or `interface !== 0` filter did not match (try `probe list` and a `--path`)   |
| Device reboots every ~10 s                                   | heartbeat disabled (`heartbeatMs: 0`)                                                           |
| Image appears on the wrong key                               | slot remap, run the verification pattern                                                        |
| `Could not compress the key image below 10240 bytes`         | very detailed image at low `maxBytes`, use `fit: 'cover'` or a simpler picture                  |
| No `keyUp` events                                            | firmware only reports presses; synthesize the release in the application                        |
| `listen` prints `Done, 0 raw report(s)`                      | no HID data at all: vendor app still running, or no key was pressed during the listen window    |
| `probe list --all` shows a `usagePage 1` interface           | that is the keyboard emulation collection, it is read-only — never open it as a control device  |
| Linux: `permission denied`                                   | add a udev rule for the vendor id (`SUBSYSTEM=="hidraw", ATTRS{idVendor}=="6603", MODE="0666"`) |

## Limitations / next steps

- Rotary encoders, swipe gestures and the N4 Pro touch strip are not modelled yet (the
  protocol events exist, `src/models/*.ts` documents the codes — `rotateLeft`/
  `rotateRight`/`swipeLeft`/`swipeRight`).
- No PNG/JPEG decoding in the driver: feed raw RGB(A). Use `sharp`, `jimp` or a canvas
  implementation in the application layer if you need to load files.
- `LOG` (boot logo) and `MOD` are exposed but untested per model.
- The image slot map for models other than the M18 is derived from the Companion
  definitions; those models have not been verified with this driver.
