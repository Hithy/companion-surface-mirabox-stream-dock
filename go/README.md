# m18 — 纯 Go 驱动（M18 专用）

只支持 Mirabox Stream Dock M18（`6603:1012` / `6603:1009`），**零第三方依赖**、`CGO_ENABLED=0` 可编译。
运行机制、协议字节、硬件参数与 Node 版一致（见仓库根目录 `docs/DRIVER.zh-CN.md`），
这里只保留 M18 需要的东西：15 个 LCD 键、3 个按钮、24 颗灯、64×64 图像、心跳、输入解析。

## 平台支持

| 平台    | 状态              | HID 实现                                                                                     |
| ------- | ----------------- | -------------------------------------------------------------------------------------------- |
| Windows | ✅ 实机验证       | `cfgmgr32` 枚举 + `CreateFile`/overlapped `ReadFile`/`WriteFile`（`hid.dll` 取属性和 usage） |
| Linux   | ⚠️ 已实现、未上机 | `/sys/class/hidraw` + `select` + 非阻塞 fd；需要 udev 权限                                   |
| macOS   | ❌ 未实现         | 需要 IOHIDManager（必须 cgo），`hid_unsupported.go` 返回明确错误                             |

Windows 下不需要 hidapi、不需要 DLL，`go build` 出的 exe 可直接拷走；
Linux 交叉编译同样是纯 Go 产物：`GOOS=linux go build ./cmd/probe`。

## 目录

```
go/
  go.mod
  m18/
    model.go       常量、键位/按钮表、Event
    protocol.go    CRT 帧构造、BAT/分包、SETLB、亮度、心跳、关机
    image.go       缩放/旋转/镜像/fit → JPEG（≤10KB，质量自动下降）
    parse.go       ParseInput / DecodeInput（输入报文）
    hid.go         Info、Transport、List、Find（含重试）
    hid_windows.go Windows 后端（纯 Go + syscall）
    hid_linux.go   Linux 后端（hidraw）
    hid_unsupported.go
    device.go      Device：写队列 goroutine、读循环、心跳、事件、命令
    *_test.go      帧字节/输入解析/图像/设备行为（mock transport）
  cmd/probe/       命令行工具（list/listen/pattern/image/led/selftest…）
  cmd/demo/        参考上层应用（跑马灯 + 按键反馈 + 灯追光）
  cmd/dashboard/   15 键状态面板（时钟/CPU/内存/运行时长/CPU 历史），可 -dump 导出 PNG
```

## 构建

```bash
cd go
go vet ./...
go test ./...
go build ./cmd/probe
go build ./cmd/demo
```

要求 Go ≥ 1.24（只用到标准库）。

## Linux

权限（systemd 系统）：

```bash
sudo cp 60-mirabox-m18.rules /etc/udev/rules.d/
sudo udevadm control --reload-rules
sudo udevadm trigger
# 拔插设备后
go run ./cmd/probe list
```

非 systemd 发行版把规则里的 `TAG+="uaccess"` 换成 `MODE="0666"`（或 `GROUP="plugdev"` + `MODE="0660"`）。

验证顺序（建议）：

```bash
go run ./cmd/probe list                  # 应看到 control(0xffa0/1) 与 secondary(0x1/6)
go run ./cmd/probe info
go run ./cmd/probe pattern -hold 30s     # 数字 + 四角标记
go run ./cmd/probe led-scan              # 24 颗灯逐颗点亮
go run ./cmd/probe listen -duration 20s  # 按键（结束时打印 raw/events 计数）
```

Linux 与 Windows 的差异只有"怎么拿到设备"和"怎么阻塞读"：协议、图像、解析都是同一份代码；
`hidraw` **不会**在报文前加 report id，解析器两种形式都接受。

## 用法

```go
package main

import (
	"context"
	"image"
	"image/color"
	"log"

	"github.com/Hithy/companion-surface-mirabox-stream-dock/go/m18"
)

func main() {
	ctx := context.Background()

	dock, err := m18.Open(m18.Options{}) // 或 {Serial: "08A903783D06"}
	if err != nil {
		log.Fatal(err)
	}
	defer dock.Close() // 清屏 + 睡眠；想保留画面用 CloseKeepImage()

	dock.SetBrightness(ctx, 85)
	dock.SetAllLeds(ctx, color.RGBA{R: 0, G: 40, B: 90, A: 255})

	img := image.NewRGBA(image.Rect(0, 0, m18.ImageWidth, m18.ImageHeight))
	for y := 0; y < m18.ImageHeight; y++ {
		for x := 0; x < m18.ImageWidth; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 0x40, A: 255})
		}
	}
	if err := dock.DrawKey(ctx, 0, img, m18.ImageOptions{}); err != nil {
		log.Fatal(err)
	}

	for event := range dock.Events() { // 关闭时 channel 自动关闭
		log.Printf("%v %s (%s)", event.Pressed, event.Name, event.ID)
	}
}
```

### API 一览

| 方法                                                          | 说明                                                      |
| ------------------------------------------------------------- | --------------------------------------------------------- |
| `Open(Options) (*Device, error)`                              | 打开（默认第一台，8s 心跳；`Heartbeat<0` 关闭心跳）       |
| `List() ([]Info, error)` / `Find(Options)`                    | 枚举 / 查找（含短暂重试，设备重枚举时不会误报）           |
| `Events() <-chan Event`                                       | 按键事件（自动过滤重复状态）                              |
| `Raw() <-chan []byte`                                         | 原始输入报文（调试）                                      |
| `Errors() <-chan error`                                       | 非致命错误（心跳/写入失败）                               |
| `Err() error`                                                 | 读循环的致命错误                                          |
| `DrawKey / DrawKeys`                                          | 画一个 / 批量画（一次编码、单次 `STP`，批量永远优于循环） |
| `ClearKey / ClearAll`                                         | 清屏                                                      |
| `SetBrightness / SetLedBrightness`                            | 0..100                                                    |
| `SetLedColors / SetLed / UpdateLeds / SetAllLeds / LedColors` | 24 颗灯，驱动缓存状态                                     |
| `Wake / Sleep / KeepAlive`                                    | DIS / HAN / CONNECT                                       |
| `Close / CloseKeepImage`                                      | 关机清屏 / 保留画面                                       |

`KeyImage{Index, Image, Options}`，`ImageOptions{Fit, Background, Rotate, MirrorX, MirrorY, Quality}`。
图像管线：合成透明 → 镜像 → 旋转 → `FitContain`(默认)/`FitCover`/`FitStretch` → JPEG（超过 10240 字节自动降质量）。

### 与 Node 版（`src/driver`）的差异

| Node 版                           | Go 版                                |
| --------------------------------- | ------------------------------------ |
| EventEmitter（`keyDown`/`keyUp`） | `Events()` channel + `Event.Pressed` |
| `drawKeys(entries)`               | `DrawKeys([]KeyImage)`               |
| `setLed/updateLeds`               | `SetLed/UpdateLeds`                  |
| 自动注册 17 个机型                | 只有 M18（砍掉）                     |
| 热插拔 `watchDocks()`             | 未提供（需要可加轮询）               |
| 固件版本读取                      | 未实现（Windows 也读不到）           |
| macOS                             | 未实现                               |

## 验证

```bash
cd go
go run ./cmd/probe list                  # 枚举（control / secondary 两个接口）
go run ./cmd/probe info                  # 键位/槽位/输入码全表
go run ./cmd/probe listen -duration 20s  # 按键事件（-raw 打印原始报文）
go run ./cmd/probe selftest              # 离线解析回归（无需设备）
go run ./cmd/probe pattern -hold 30s     # 每键画数字 + 四角标记
go run ./cmd/probe image -file logo.png  # 显示图片（见下）
go run ./cmd/probe led-scan              # 逐颗点亮 24 颗灯
go run ./cmd/probe led-rainbow
go run ./cmd/probe brightness 80
go run ./cmd/demo                        # 参考应用，Ctrl+C 退出
go run ./cmd/dashboard -duration 30s     # 15 键状态面板
```

### 显示图片（`probe image`）

Go 标准库自带 PNG/JPEG/GIF 解码，直接喂文件即可，缩放/裁切/旋转由驱动完成：

```bash
go run ./cmd/probe image -file logo.png                    # 画到 key 1（contain）
go run ./cmd/probe image -file logo.png -key 3 -fit cover  # 指定键 + 裁切填满
go run ./cmd/probe image -file logo.png -all               # 15 个键都画
go run ./cmd/probe image -file logo.png -rotate 90 -mirror-x -bg '#101018'
```

`-fit contain|cover|stretch`、`-rotate 0|90|180|270`、`-mirror-x/-mirror-y`、
`-bg` 控制留边/透明像素的底色。

### 状态面板（`cmd/dashboard`）

```bash
go run ./cmd/dashboard                         # 一直跑，Ctrl+C 退出
go run ./cmd/dashboard -duration 30s           # 跑 30 秒后退出
go run ./cmd/dashboard -dump ./preview         # 不需要设备，导出 15 键 PNG + 拼图
go run ./cmd/dashboard -dump ./preview -live   # 用真实指标导出（同样不需要设备）
```

| 键  | 内容                          | 键    | 内容                                                            |
| --- | ----------------------------- | ----- | --------------------------------------------------------------- |
| 1   | 时钟 HH:MM + 秒               | 6–9   | **CPU 分簇**（P / E），不足 4 个时用 LED / EVT / FPS / GOR 补齐 |
| 2   | 日期 + 星期                   | 10–14 | CPU 最近 5 次采样（新 → 旧，柱状）                              |
| 3   | CPU 总占用 + 条               |       |                                                                 |
| 4   | 内存占用 + 条                 |       |                                                                 |
| 5   | **GPU 利用率 + 条**           |       |                                                                 |
| 6   | **显存占用**（已用 + 占比条） |       |                                                                 |

底部按钮：1 = 灯开关，2 = LED 模式（LOAD/RAIN/OFF），3 = 亮度切换（90/100）。

**CPU 详情页**：在主页面按第 3 个键（CPU）进入，15 个键显示每个**逻辑核心**的利用率和当前频率
（超过 15 核自动分页，页尾显示 `PAGE 1/2` 和 `BTN2 BACK`）。详情页里底部按钮变成：
左 = 上一页、**中 = 返回主页面**、右 = 下一页。
频率来源：Windows 用 PDH `Processor Information(*)% Processor Performance` × 标称频率
（标称频率来自 `CallNtPowerInformation`），所以睿频会体现出来；Linux 读
`/sys/devices/system/cpu/cpuN/cpufreq/scaling_cur_freq`，没有 cpufreq 时显示 `?`。

**CPU 分簇（只聚合，不逐核显示）**
Windows：`GetSystemCpuSetInformation` 取每个逻辑核的 EfficiencyClass，配合
`NtQuerySystemInformation(SystemProcessorPerformanceInformation)` 的每核时间做增量聚合；
带 SMT 的那一簇标 `P`，其余标 `E`（判断不确定时退化为 `C0/C1`；只有一簇时直接用总量，不重复显示）。
Linux：按 `/sys/devices/system/cpu/cpuN/cpu_capacity` 分组，没有该文件时单簇。

**GPU / 显存**
Windows：PDH 性能计数器（纯 Go，无 cgo）。
`GPU Engine(*)\Utilization Percentage` 先按引擎类型求和再取最大值（接近任务管理器口径）；
`GPU Adapter Memory(*)\Dedicated Usage` 取各适配器最大值作为显存占用；
总显存取注册表 `HardwareInformation.qwMemorySize` 的最大值。
Linux：优先 `nvidia-smi`（2 秒缓存，避免每秒起进程），否则读 AMD 的
`gpu_busy_percent` / `mem_info_vram_used` / `mem_info_vram_total`。取不到时显示 `N/A`。

实现上演示了三件上层应用最需要的事：**250ms 定时 + 签名比对只重画变化的键**、
**每秒采样一次指标**、**按键按下时只给那个键加高亮边框**（220ms 后自动恢复）。

已在 Windows 的 M18 EN（`6603:1012`，序列号 `08A903783D06`）上验证：枚举、唤醒、15 键批量绘制、
`SETLB`/`LBLIG`、亮度、`Close` 关机时序，以及**全部 18 个控件**（15 键 + 3 按钮）
按下/松开共 36 条事件（`probe listen`）。

### 性能（实机，Go 1.24 / Windows）

| 操作                              | 耗时                    |
| --------------------------------- | ----------------------- |
| `DrawKeys` 15 键（单次 STP）      | **9.6 ms**（0.6 ms/键） |
| `DrawKeys` 2 键                   | 1.0 ms                  |
| `DrawKey` 1 键                    | 0.5 ms                  |
| `SetLedColors` 24 颗              | 0.1 ms                  |
| `SetBrightness`                   | 0.1 ms                  |
| `EncodeKeyImage`（64×64，纯 CPU） | 0.17 ms                 |

60 fps 全屏重画（16.7 ms/帧）还有余量；动画建议只重画变化的键，`go test -bench .` 可复现纯 CPU 部分。

## 注意

- **JPEG 编码器差异**：Go 标准库固定 4:2:0，Node 版优先 4:2:2。当前实测 M18 接受 4:2:0；
  若换固件后出现色块，把 `image.go` 的编码换成 libjpeg-turbo 绑定（例如 `pixiv/go-libjpeg`）即可，
  其余代码不用改。
- **输入报文偏移**：Windows 的 `ReadFile` 会在报文前加 1 字节 report id（`0x00`），
  Linux 不会；解析器按 `ACK` 的实际位置处理两种形式（有对应单测）。
- **读超时**：读是阻塞式的（和 hidapi 一致），`Close()` 会取消挂起的读并唤醒等待，不会卡住；
  `probe listen` 结束时会打印 `raw=N events=M err=...` 便于判断"没数据"还是"解析失败"。
- **设备独占**：厂商软件（StreamDock / VSD Craft）和 Companion 必须退出。
- **Windows 首帧超时**：上一个进程刚退出时设备可能正在重枚举，`Open` 会自动重试一次；
  仍失败就等 1 秒再试。
- **Linux 权限**：`SUBSYSTEM=="hidraw", ATTRS{idVendor}=="6603", MODE="0666"`。
- 许可证：MIT（与仓库一致）。
