# Stream Dock 驱动使用手册（M18 实机验证）

面向**上层应用开发者**的中文文档。读完你应该能：枚举设备、画键、收按键事件、控灯、
写动画、处理多设备和异常。协议与排错细节在英文版 [DRIVER.md](./DRIVER.md) 里。

- 驱动代码：`src/driver/`
- 调试工具：`tools/m18-probe.mts`、`tools/m18-demo.mts`
- Go 版（仅 M18、零依赖）：[`go/README.md`](../go/README.md)
- 验证硬件：Mirabox Stream Dock M18（`6603:1012`），已实机确认图像/按键/24 颗 LED

---

## 1. 这套驱动是什么

```
你的上层应用
   │  import { StreamDockDevice } from 'companion-surface-mirabox-stream-dock/driver'
   ▼
src/driver  ── node-hid ──►  USB HID  ──►  Stream Dock 设备
   │
   └─ 图像管线（缩放/旋转/JPEG 压缩 10KB）+ 命令队列 + 心跳 + 事件解析
```

它**不依赖 Companion**，也不依赖 StreamDock 厂商软件。同一个仓库里还有：

| 部分                                  | 作用                      | 与本驱动的关系                                               |
| ------------------------------------- | ------------------------- | ------------------------------------------------------------ |
| `src/driver/`                         | 独立驱动（本文档）        | 上层应用的唯一入口                                           |
| `src/main.ts`、`src/instance.ts`      | Companion 的 surface 模块 | 独立的一套实现，互不引用                                     |
| `StreamDock-Plugin-SDK`（另一个仓库） | 厂商宿主软件的插件 SDK    | 无 HID 层，插件只通过 WebSocket 与宿主通信，不能用来驱动硬件 |

> 三者都要独占设备：**同一时间只能有一个进程持有设备**，厂商软件（StreamDock / VSD
> Craft）和 Companion 必须全部退出。

---

## 2. 目录结构

```
src/driver/
  index.ts           公共入口（只从这里 import）
  types.ts           全部公共类型
  dock.ts            StreamDockDevice —— 你主要用的类
  protocol.ts        底层命令（CRT 帧、BAT/CLE/STP/LIG/SETLB…）、写队列
  input.ts           parseInputReport()，输入报文解析（纯函数，可离线测试）
  image.ts           镜像/旋转/缩放/留白 → JPEG（自动压到 ≤10KB）
  hid.ts             设备枚举 listDocks()、findDock()、watchDocks() 热插拔
  models/m18.ts      M18 硬件描述（PID、键位、槽位、24 LED、64×64）
  models/index.ts    机型注册表（M18 + 从 Companion 定义自动生成的其它机型）
```

---

## 3. 安装与构建

```bash
# 依赖（本机 Node 25 + npm 已验证可用；仓库 engines 写的是 Node 22）
npm install --legacy-peer-deps

# 构建，产出 dist/（ESM + .d.ts）
npm run build
```

本仓库 `package.json` 固定了 `packageManager: yarn@4.18.1`：

- 用 Yarn 4：先 `npm i -g @yarnpkg/cli-dist@4.18.1`，再 `yarn install`
- 用 npm：所有脚本都能跑，**传参要加 `--`**，如 `npm run probe -- listen 20`

上层工程引用本包：

```jsonc
// 你的 package.json
{ "dependencies": { "companion-surface-mirabox-stream-dock": "file:../companion-surface-mirabox-stream-dock" } }
```

```ts
import { StreamDockDevice } from 'companion-surface-mirabox-stream-dock/driver'
```

---

## 4. 五分钟上手

```ts
import { StreamDockDevice } from 'companion-surface-mirabox-stream-dock/driver'

const dock = await StreamDockDevice.open() // 不传参 = 第一台
console.log(dock.model.name) // Mirabox Stream Dock M18
console.log(dock.keys.length, dock.buttons.length) // 15 3

// 画一个纯色键：RawImage = 紧凑 RGB(A) 像素
const { width, height } = dock.imageFormat // M18: 64x64
const rgb = new Uint8Array(width * height * 3).fill(0x40)
await dock.drawKey(0, { data: rgb, width, height, channels: 3 })

// 事件：LCD 键和底部按钮都会走 keyDown/keyUp，用 kind 区分
dock.on('keyDown', async (event) => {
	console.log(event.kind, event.index, event.id, event.name) // key 0 0/0 Key 1
	await dock.setLed(event.index % dock.model.ledCount, [0, 200, 120])
})
dock.on('keyUp', (event) => console.log('up', event.name))
dock.on('error', (error) => console.error(error.message))

// 亮度与灯
await dock.setBrightness(85)
await dock.setLedBrightness(70)
await dock.setAllLeds([0, 40, 90])

// 退出
await dock.close() // 清屏 + 睡眠；想保留最后一帧用 close({ shutdown: false })
```

跑起来看效果（参考实现）：

```bash
npm run demo          # 键位跑马灯 + 24 灯追光 + 按键闪绿，Ctrl+C 退出
```

---

## 5. 核心概念

### 5.1 Model / Key / Button / Slot

- `model`：机型描述，含 `packetSize`、`image`、`keys`、`buttons`、`ledCount`、`capabilities`
- `keys[i]`：`{ index, inputId, slot, name, row, column, id }`
  - `index`：0-based 序号，画图用它
  - `slot`：**图片槽位号**，与按键码不同（M18 上 `0x0b…0x05`），由驱动内部使用
  - `id`：`"row/column"`，与 Companion 的控件 ID 一致，方便迁移布局
- `buttons[i]`：屏幕下方无显示的物理键（M18 的 `3/1`、`3/2`、`3/3`），
  **不在 `keys[]` 里**，也没有槽位、不能画图
- `image`：面板分辨率与 JPEG 上限，M18 为 `64×64`、`10240` 字节

### 5.2 imageFormat 可运行时覆盖

```ts
dock.imageFormat = { ...dock.imageFormat, rotation: 180, fit: 'cover' }
```

用于个别应用想换构图方式，或适配未验证的 OEM 机型。改分辨率要谨慎（M18 就是 64）。

### 5.3 事件模型

| 事件      | 参数           | 说明                                  |
| --------- | -------------- | ------------------------------------- |
| `keyDown` | `DockKeyEvent` | 按下（含底部按钮）                    |
| `keyUp`   | `DockKeyEvent` | 松开（M18 支持双态）                  |
| `report`  | `Buffer`       | 原始输入报文（逆向/排错用，512 字节） |
| `error`   | `Error`        | 心跳/写入失败等，非致命               |
| `close`   | —              | 句柄已关闭                            |

`DockKeyEvent = { kind: 'key' | 'button', index, id, name, row, column, code }`

驱动会过滤**重复的相同状态**（有些固件会连续上报同一状态），所以 `keyDown` 不会连发。

### 5.4 生命周期与设备独占

1. `open()` 会自动发 `DIS`（唤醒）并启动 **8 秒心跳**（`CONNECT`），否则固件会重启
2. 所有 HID 写操作经由内部队列串行化，心跳不会插进图片分包中间
3. `close({ shutdown: true })`（默认）发 `CLE .. DC` + `HAN` 再关句柄；`shutdown: false` 保留画面
4. 心跳停止后设备可能自行复位/黑屏，这取决于固件——需要"常亮"就保持进程运行

---

## 6. API 参考

### 6.1 枚举与打开

```ts
static StreamDockDevice.list(): Promise<DiscoveredDock[]>      // [{ model, info }]
static StreamDockDevice.open(options?: OpenOptions): Promise<StreamDockDevice>

listDocks({ includeAllInterfaces? }): Promise<DiscoveredDock[]>
findDock({ modelId?, serialNumber? }): Promise<DiscoveredDock | undefined>
watchDocks({ intervalMs?, includeAllInterfaces? }): DockWatcher
```

`OpenOptions`：

| 字段                   | 默认    | 说明                                       |
| ---------------------- | ------- | ------------------------------------------ |
| `path`                 | —       | 按 HID 路径打开（多台同型号时最稳）        |
| `serialNumber`         | —       | 按序列号打开                               |
| `index`                | `0`     | 第几台                                     |
| `heartbeatMs`          | `8000`  | 心跳间隔，`0` 关闭                         |
| `autoRefresh`          | `true`  | 绘制后是否自动发 `STP`                     |
| `includeAllInterfaces` | `false` | 仅诊断用，会看到键盘模拟口（只读，不能写） |

```ts
// 多设备
for (const d of await StreamDockDevice.list()) console.log(d.model.id, d.info.serialNumber, d.info.path)
const left = await StreamDockDevice.open({ serialNumber: '08A903783D06' })
```

### 6.2 属性

| 属性                                      | 类型                    | 说明                                                      |
| ----------------------------------------- | ----------------------- | --------------------------------------------------------- |
| `model`                                   | `StreamDockModel`       | 机型描述                                                  |
| `info`                                    | `HidDeviceInfo`         | `path`/`serialNumber`/`vendorId`/`productId`/`interface`… |
| `imageFormat`                             | `StreamDockImageFormat` | **可写**，绘制时使用                                      |
| `keys` / `buttons`                        | readonly 数组           | 布局                                                      |
| `isOpen` / `serialNumber` / `productName` | —                       | 便捷读取                                                  |
| `ledColors`                               | `ReadonlyArray<Rgb>`    | 最近一次下发的灯色                                        |

### 6.3 绘制

```ts
drawKey(index, image: RawImage, options?: DrawImageOptions): Promise<void>
drawKeyJpeg(index, jpeg: Uint8Array, options?: { refresh?: boolean }): Promise<void>
drawKeys(entries: KeyImageEntry[], options?: { refresh?: boolean }): Promise<void>
clearKey(index, options?: { refresh?: boolean }): Promise<void>
clearAll(options?: { refresh?: boolean }): Promise<void>
flush(): Promise<void>                          // 单独发 STP
```

```ts
interface RawImage {
	data: Uint8Array
	width: number
	height: number
	channels: 3 | 4
}

interface DrawImageOptions {
	rotation?: 0 | 90 | 180 | 270 // 顺时针角度，默认取 model.image.rotation
	mirrorX?: boolean
	mirrorY?: boolean
	fit?: 'contain' | 'cover' | 'stretch' // 默认 contain（等比+留黑边）
	background?: [number, number, number] // 留边颜色 / 透明通道合成底色
	quality?: number // JPEG 起始质量，默认 90，过大时自动降
	noRefresh?: boolean // 先不刷新，之后自己 flush()
}
```

- **批量永远优于循环单键**：`drawKeys` 会一次性编码、一次 `STP`
- `drawKeyJpeg` 用于你已经编码好的 JPEG（尺寸必须等于 `imageFormat`，且 ≤ `maxBytes`）

### 6.4 亮度与灯

```ts
setBrightness(percent: 0..100)
setLedBrightness(percent: 0..100)
setAllLeds(rgb)
setLed(index, rgb)                      // 只改一颗，其余保持（驱动缓存状态）
updateLeds({ [index]: rgb })            // 改多颗
setLedColors(colors: Rgb[])             // 必须正好 model.ledCount 个
ledColors                               // 读取当前缓存
```

亮度是**线性** 0..100 直接下发；Companion 模块额外做了 `pow(x, 0.75)` 伽马，本驱动没有。
需要相同观感就自己映射。

### 6.5 其它

```ts
wake()                    // DIS
sleep()                   // HAN
keepAlive()               // CONNECT（一般不用手调）
readFirmwareVersion()     // Windows 上返回 undefined（HID feature report 限制）
sendCommand([0x53, 0x54, 0x50])  // 逃生口：任意 CRT 命令
setMode(mode)             // MOD，机型相关
close({ shutdown?: boolean })
```

### 6.6 工具函数

```ts
parseInputReport(model, data): ParsedInput | null   // 纯函数，可单测
prepareImage(raw, format, options?): RgbImage      // 镜像/旋转/缩放/留白
encodeJpeg(image, format, quality?): Promise<Buffer>
resizeImage / rotateImage / mirrorImage / toRgbImage / blitImage
findModel(vid, pid) / getModel(id) / DRIVER_MODELS
```

---

## 7. 图像管线

```
RawImage(RGB/RGBA)
   → 透明通道按 background 合成
   → mirrorX/mirrorY
   → rotation(0/90/180/270)
   → 按 fit 缩放（contain 留边 / cover 裁切 / stretch 拉伸）
   → JPEG 编码（质量从 90 逐级下降，必要时 4:2:0，直到 ≤ maxBytes）
   → BAT 命令 + 分包数据（每包 1024 字节）+ 一次 STP
```

注意：

- 驱动**不解码** PNG/JPEG/SVG。应用层用 `sharp` / `jimp` / `canvas` 解成像素再传
- 缩放用纯 TS 实现（缩小走面积平均、放大走双线性），无原生依赖
- 64×64 的键图一般 2–4 KB，远低于 10 KB 上限；10 KB 是设备侧限制
- 想"先画 N 个键再一起显示"：`drawKeys(entries, { refresh: false })` + 稍后 `flush()`

---

## 8. 性能实测（M18 EN，本机 Windows + Node 25）

| 操作                         | 耗时        |
| ---------------------------- | ----------- |
| `drawKey`（单键，含 STP）    | **1.6 ms**  |
| `drawKey(noRefresh) + flush` | 1.2 ms      |
| `drawKeys` 15 键（一次 STP） | **13.8 ms** |
| `setAllLeds` 24 颗           | **0.2 ms**  |
| `setBrightness`              | 0.2 ms      |

结论与建议：

- 全屏 15 键重画 ≈ 14 ms，做 30–60 fps 动画没问题，但**只重画变化的键**更省
- LED 几乎免费（0.2 ms），可以每帧刷
- 单键 `drawKey` 每次都带 `STP`，循环 15 次 ≈ 24 ms 且闪烁，务必用 `drawKeys`
- 动画循环建议 `setInterval(…, 100)` 起，按需降到 33 ms

---

## 9. 上层应用常用配方

### 9.1 只重画变化的键

```ts
const shown = new Map<number, string>()

async function render(next: Map<number, RawImage>, key: (i: number) => string) {
	const changed = [...next].filter(([index]) => shown.get(index) !== key(index))
	if (changed.length === 0) return
	await dock.drawKeys(changed.map(([index, image]) => ({ index, image })))
	for (const [index] of changed) shown.set(index, key(index))
}
```

### 9.2 长按 / 双击

```ts
const pressedAt = new Map<number, number>()
const lastTap = new Map<number, number>()

dock.on('keyDown', (e) => pressedAt.set(e.index, Date.now()))
dock.on('keyUp', (e) => {
	const start = pressedAt.get(e.index) ?? Date.now()
	const held = Date.now() - start
	const previous = lastTap.get(e.index) ?? 0
	lastTap.set(e.index, Date.now())

	if (held > 600) onLongPress(e)
	else if (Date.now() - previous < 300) onDoubleTap(e)
	else onTap(e)
})
```

### 9.3 呼吸灯 / 跑马灯

```ts
let phase = 0
const timer = setInterval(() => {
	phase += 0.08
	const level = Math.round((Math.sin(phase) * 0.5 + 0.5) * 255)
	dock.setAllLeds([0, level, level]).catch(() => {})
}, 50)
```

### 9.4 热插拔（轮询，node-hid 无系统级热插拔）

```ts
import { watchDocks, StreamDockDevice } from 'companion-surface-mirabox-stream-dock/driver'

let dock: StreamDockDevice | undefined
const watcher = watchDocks({ intervalMs: 2000 })
watcher.on('attach', async (d) => {
	if (dock?.isOpen) return
	dock = await StreamDockDevice.open({ path: d.info.path })
	await paintInitial()
})
watcher.on('detach', (d) => {
	if (dock?.info.path === d.info.path) void dock?.close({ shutdown: false })
})
// 退出时 watcher.stop()
```

### 9.5 异常与重连

`error` 事件是非致命的（心跳失败、单次写失败）。真正拔线时 `write` 会持续报错，
建议：监听 `error`，连续 N 次失败就 `close({ shutdown: false })` 并交给 `watchDocks` 重新打开。

### 9.6 优雅退出

```ts
process.on('SIGINT', async () => {
	clearInterval(timer)
	await dock.close() // 清屏 + 睡眠；不想清屏用 { shutdown: false }
	process.exit(0)
})
```

> Windows 上只有**真实 Ctrl+C**（控制台信号）会触发 `SIGINT`；用 `kill` 一般无效。

---

## 10. M18 硬件手册（实机确认）

### 10.1 控件

| 控件     | index | id    | 输入码 | 图片槽位 |
| -------- | ----- | ----- | ------ | -------- |
| Key 1    | 0     | `0/0` | `0x01` | `0x0b`   |
| Key 2    | 1     | `0/1` | `0x02` | `0x0c`   |
| Key 3    | 2     | `0/2` | `0x03` | `0x0d`   |
| Key 4    | 3     | `0/3` | `0x04` | `0x0e`   |
| Key 5    | 4     | `0/4` | `0x05` | `0x0f`   |
| Key 6    | 5     | `1/0` | `0x06` | `0x06`   |
| Key 7    | 6     | `1/1` | `0x07` | `0x07`   |
| Key 8    | 7     | `1/2` | `0x08` | `0x08`   |
| Key 9    | 8     | `1/3` | `0x09` | `0x09`   |
| Key 10   | 9     | `1/4` | `0x0a` | `0x0a`   |
| Key 11   | 10    | `2/0` | `0x0b` | `0x01`   |
| Key 12   | 11    | `2/1` | `0x0c` | `0x02`   |
| Key 13   | 12    | `2/2` | `0x0d` | `0x03`   |
| Key 14   | 13    | `2/3` | `0x0e` | `0x04`   |
| Key 15   | 14    | `2/4` | `0x0f` | `0x05`   |
| Button 1 | 0     | `3/1` | `0x25` | —        |
| Button 2 | 1     | `3/2` | `0x30` | —        |
| Button 3 | 2     | `3/3` | `0x31` | —        |

底部按钮用 `kind: 'button'` 区分，`index` 是 0–2 的独立序号（不是 15–17）。

### 10.2 规格

| 项             | 值                                           |
| -------------- | -------------------------------------------- |
| USB ID         | `0x6603:0x1012`（EN）、`0x6603:0x1009`（CN） |
| 控制接口       | `interface 0`，`usagePage 0xFFA0`，`usage 1` |
| 写报文         | 1024 字节载荷 + 1 字节 report id             |
| 输入报文       | 512 字节                                     |
| 面板           | 每键 64×64，rotation 0，无镜像               |
| 单键 JPEG 上限 | 10240 字节                                   |
| LED            | 24 颗，`SETLB` + 24×RGB，`LBLIG` 单独调亮度  |
| 按键双态       | 支持（按下 + 松开）                          |
| 固件版本读取   | Windows 不支持                               |

---

## 11. 协议速查

命令 = `CRT`（`43 52 54`）+ 3 字节操作码，末尾补零到 1024 字节，报文首字节为 report id `0x00`。

| 操作码    | 字节                             | 作用                           |
| --------- | -------------------------------- | ------------------------------ |
| `DIS`     | `44 49 53`                       | 唤醒 / 开显示                  |
| `HAN`     | `48 41 4e`                       | 睡眠                           |
| `STP`     | `53 54 50`                       | 刷新（提交已发送的图）         |
| `CONNECT` | `43 4f 4e 4e 45 43 54`           | 心跳，约 8s 一次，否则设备重启 |
| `BAT`     | `42 41 54` + `u32 长度` + `槽位` | 后面的分包是一张 JPEG          |
| `CLE`     | `43 4c 45 00 00 00 槽位`         | 清一个键（`0xff` 清全部）      |
| `LIG`     | `4c 49 47 00 00 百分比`          | 屏幕亮度                       |
| `LBLIG`   | `4c 42 4c 49 47 百分比`          | LED 亮度                       |
| `SETLB`   | `53 45 54 4c 42` + RGB…          | 每颗灯一个 RGB                 |
| `MOD`     | `4d 4f 44 00 00 0x30+模式`       | 切换设备模式                   |
| `QUCMD`   | `51 55 43 4d 44` + bytes         | 厂商配置（如 N4 Pro 振动）     |
| `LOG`     | `4c 4f 47 00 11 94 00 01`        | 开机图（随后是裸 BGR 数据）    |

图片数据分包**不带** `CRT` 前缀，每包 1024 字节，最后一条 `STP` 提交。

输入报文（512 字节）：

```
0..2  'ACK'   3..4 00 00   5..6 'OK'   7..8 00 00
9     键码（0x01..0x0f / 0x25 / 0x30 / 0x31）
10    状态（0x01 按下 / 0x00 松开）
```

> 别按前缀过滤 `ACK`：按键事件也用它（早期驱动踩过这个坑，`probe selftest` 已覆盖）。

---

## 12. 调试工具

```powershell
npm run probe -- list                    # 枚举（型号/序列号/接口/键位/灯数）
npm run probe -- list --all              # 连键盘模拟口一起列出（只读，别当控制口用）
npm run probe -- info --no-shutdown      # 型号、能力、固件、完整键表
npm run probe -- listen 20               # 按键事件；--raw 打印原始报文
npm run probe -- selftest                # 离线解析回归测试（不需要设备）
npm run probe -- pattern --hold 30       # 每键画"数字 + 四角标记"，验证分辨率/方向/槽位
npm run probe -- solid '#3050ff'         # 全部键刷色
npm run probe -- led '#00ff00'           # 全部灯刷色
npm run probe -- led-scan                # 逐颗点亮，验证数量与索引
npm run probe -- led-rainbow             # 彩虹
npm run probe -- brightness 70
npm run probe -- led-brightness 50
npm run probe -- clear / wake / sleep
```

环境变量 `STREAMDOCK_DEBUG=1` 会打开驱动的调试日志（未识别的报码等）。

PowerShell 里设置：

```powershell
$env:STREAMDOCK_DEBUG=1; npm run probe -- listen 10
```

---

## 13. 排错

| 现象                                                 | 原因 / 处理                                                                     |
| ---------------------------------------------------- | ------------------------------------------------------------------------------- |
| `Could not open the Stream Dock … (0x00000005)`      | 设备被占用：关掉厂商软件、Companion、其它实例                                   |
| `No Stream Dock found`                               | 线/口问题，或 `interface` 不是 0；用 `probe list` 核对                          |
| 设备每 ~10 秒重启                                    | 心跳被关（`heartbeatMs: 0`）或进程被阻塞                                        |
| 图出现在错误的键上                                   | 槽位映射；`probe pattern` 看数字是否对位                                        |
| 图方向不对                                           | `probe pattern --rotate 90/180/270`、`--mirror-x`，然后把值写进 `models/m18.ts` |
| 图模糊 / 错位                                        | 分辨率不是 64（M18 上 60 会错位）                                               |
| `Could not compress the key image below 10240 bytes` | 图太复杂，用 `fit: 'cover'` 或减少细节                                          |
| `HID write timed out`                                | USB 忙/线材问题；驱动已把超时设为 5s                                            |
| `listen` 里 `0 raw report(s)`                        | 20 秒内没按键，或设备被占用                                                     |
| 有 RAW 没有 DOWN/UP                                  | 输入格式变了，加 `--raw` 把 hex 发出来；`selftest` 是基线                       |
| 只有按下没有松开                                     | 个别固件只上报按下；在应用里自己合成松开事件                                    |
| `yarn probe` 报 Corepack / Yarn 版本                 | 见第 3 节，或直接用 `npm run probe -- …`                                        |
| Linux 权限不足                                       | 加 udev 规则：`SUBSYSTEM=="hidraw", ATTRS{idVendor}=="6603", MODE="0666"`       |

---

## 14. 扩展

### 14.1 新增机型

两种方式：

1. **写一个模型对象**（推荐，能力最全）：参考 `src/driver/models/m18.ts`，
   填 `usbIds` / `keys` / `buttons` / `ledCount` / `image` / `capabilities`
2. **复用 Companion 定义**：往 `src/models/*.ts` 加定义后，`models/index.ts` 会自动生成
   基础模型（键位、槽位、输入码由定义推导；分辨率取 LCD 的 `resolutionx/y`）

### 14.2 目前没做的

- **旋钮 / 滑动手势 / N4 Pro 触控条**：协议码在 `src/models/*.ts` 里有
  （`rotateLeft`/`rotateRight`/`swipeLeft`/`swipeRight`），驱动的 `DockEvents` 还没有对应事件
- `LOG`（开机图）、`MOD`（模式）已实现但未逐机型验证
- 其他机型（N4/XL/N3/293V3/AKP05E…）的槽位映射是推导值，没有实机验证
- VSDinside M18（`5548:1000`）复用同一套布局，未实机验证

### 14.3 与 Companion 模块并存

两者都用同一份 HID 协议，但**不能同时运行**。如果你的应用要和 Companion 共存，
需要做仲裁（例如检测 Companion 是否在跑，或让用户二选一）。

---

## 15. 出处与许可

- 驱动/本仓库：MIT（Bitfocus AS）
- 协议来源：
  - 本仓库 Companion 模块 `src/streamdock.ts`（基础命令、M18 槽位）
  - `mirajazz` / OpenDeck 设备插件（`HAN`/`MOD`/`QUCMD`/`LOG`、批量 flush、24 LED）
  - 实测抓包（输入报文格式、512 字节输入、`SETLB` 逐颗灯）
- 官方 `StreamDock-Plugin-SDK`：**无 HID 层**，仅用于插件层概念（`Knob` 命名、
  无 `setFeedback`、`Keypad` 控制器）与设备能力口径
