# Quote0 Link

把电脑上的状态和消息，通过 USB 送上标准版 Quote/0 墨水屏。非 MindReset 官方项目。

## 两个独立部分

| 层 | 职责 |
|---|---|
| `firmware/` | 标准版 ESP32-C3 / UC8251D。只负责接收、校验、刷新黑白图帧，不知道 Codex、Claude 或模板。 |
| `app/` | Go 托盘程序，本机 API、管理页、集成目录、模板渲染、USB 串口。管理页关掉后，后台仍能接收事件。 |

```text
Codex / Claude Code Hook ──→ /v1/events ──┐
其他 App / 管理页 ─────────→ /v1/display ─┼─→ 模板渲染 → 1bpp 图帧 → USB → Quote/0
                                        ┘
```

应用接入只需要 HTTP，不需要操作串口；USB 层只接收渲染后的图帧。新增模板只改 App；新增使用场景可以直接调用 API，不需要改 App 或固件。集成目录目前是内置适配器，不是能安装任意第三方代码的远程市场。

## 本地构建

需要 Go 1.24。macOS 构建还需要 Xcode Command Line Tools；在 Mac 上运行下列命令：

```sh
make test
make mac       # dist/Quote0 Link.app
make windows   # dist/Quote0 Link.exe（Windows x64，可跨编译）
```

上述目标只编译桌面 App。要生成带固件和独立刷写工具的完整包，先按 [固件及回滚说明](firmware/README.md) 编译，再运行：

```sh
make package-mac package-windows
```

完整包生成到 `dist/`，打包时从 Espressif 下载固定版本的独立版 `esptool` 并核对 SHA-256。用户不必安装 Python、ESP-IDF 或额外下载刷写工具。macOS 解压后可把 `.app` 拖到「应用程序」；Windows 应完整解压 ZIP，并保持 `maintenance/` 与 `.exe` 同目录。当前是开发构建：macOS 使用本地临时签名（未公证），Windows 为未签名便携包。每次启动会自动打开管理页，也可在菜单栏或系统托盘重新打开。App 路径变动后重新启用 Hook。

## 设备维护

从系统托盘打开管理页，找到「设备维护」，按手机 Dot. App 的设备详情选择版本：

| Dot. App 显示 | 备份与恢复同一台设备的原厂 Flash | 安装 Link |
|---|---|---|
| 无「首发版」标签，标准版 UC8251D | 支持（须通过 ROM 与容量检查） | 支持 |
| 有「首发版」标签，UC8151 | 支持（同上） | 暂不支持，应用会阻止安装 |

如果无法确认版本，请勿安装 Link。USB 标识可以绑定到设备，但**不能识别屏幕控制器**。左侧「设备维护」分为三个标签：

- **备份原厂**：确认设备仍运行原厂固件后读取 Flash。两次读取的 SHA-256 一致才会生成 `.q0backup`；请点击「下载备份」并自行保存。App 不维护备份库，下载文件仅在内存中临时保留 10 分钟。
- **安装 Link**：选择标准版即可安装内置固件。建议先下载原厂备份以便日后恢复，但安装时不要求提供备份文件。
- **恢复原厂**：直接选择本地 `.q0backup`，无需先导入或重新判断屏幕版本。备份含设备配置，不要共享或上传。旧版 App 已保存的 `.q0backup` 仍然兼容，可从 `~/Library/Application Support/Quote0 Link/backups/`（macOS）或 `%APPDATA%\Quote0 Link\backups\`（Windows）直接选取；升级不会扫描、迁移或删除这些旧文件。

刷写工具运行时需要使用操作系统的私有临时目录；任务结束后 App 会清理临时文件。

安装和恢复都会核对设备标识，要求安全启动及 Flash 加密均关闭、Flash 容量为 4 MiB；恢复还要求备份与芯片 MAC 匹配。检查失败时不会强刷。写入期间暂停显示任务，完成后校验并重启。恢复后请用 Dot. App 确认设备正常。如果无法自动进入 ROM 下载模式，请按设备说明手动进入后再勾选对应选项。不要拔线或休眠。维护授权不使用 `api-token`；重新加载网页后，维护入口需从托盘重新打开。

原厂固件不能直接接收 Link 图帧。接入多个 Espressif 设备时，可通过 `--port /dev/cu.usbmodem1101` 或 `--port COM4` 指定目标；调试时可用 `--headless`，以 `--font` 指定额外的中文字体（TTF、OTF 或 TTC）。

管理页左下角显示设备连接状态、已安装的固件版本、USB 供电状态和电池电压。早期固件仍可投图；没有版本或电压读数时会明确提示。若当前安装包内置更新的固件，侧栏会提供升级入口，点击后进入「安装 Link」，不会自动刷写。电压不是充电状态或容量百分比。

## 本机 API

服务只监听 `127.0.0.1:45470`。启动时生成令牌，保存在用户配置目录下 `Quote0 Link/api-token`；不要分享令牌。macOS 示例：

```sh
token="$(cat "$HOME/Library/Application Support/Quote0 Link/api-token")"
curl http://127.0.0.1:45470/v1/display \
  -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' \
  --data '{"template":"card","title":"构建","subtitle":"CI","body":"测试通过"}'
```

Windows 令牌通常位于 `%APPDATA%\Quote0 Link\api-token`。无网络请求发往云端。`POST /v1/display` 支持：

| `template` | 参数 |
|---|---|
| `text` | `body` |
| `image` | `image_base64`，不带 data URL 前缀的 PNG/JPEG |
| `card` | `title`、`subtitle`、`body` |
| `test` | 无，棋盘格 |

返回 `202` 代表已排队，而不是已上屏。未开始的旧任务会被较新的任务替换；`GET /v1/status` 返回最近任务的 `job`、`state` 及可能的 `error`。`GET /v1/device` 返回 `state`、可用的 `port`，以及固件支持时的 `firmware_version`、`usb_power` 和 `battery_mv`。完整包还会报告 `bundled_firmware_version`；检测到可升级时返回 `update_available: true`。只有握手成功才返回 `connected`。设备拔插后，下次提交会重新探测串口；离线期间提交失败的内容不会补发。

`POST /v1/events` 接收内置适配器的 `source`、`session`、`state`。集成目录可按应用启用或移除 Hook，同时保留其他 Hook 并备份已有配置。Codex 仍需要审核并信任 Hook；仅支持本机 Hook 可执行的会话。Hook 只转发事件状态和会话 ID，不发提示词，不替用户审批，也不等待墨水屏刷新。
