# Quote0 Link 固件

仅针对标准版 Quote/0（ESP32-C3、UC8251D）。不支持标为「首发版」的 UC8151 设备；App 会禁止为首发版安装 Link 固件。请优先用完整 App 包中的「设备维护」完成备份和恢复。下列是开发构建和命令行回滚的备用路径。硬件和显示驱动来源：MindReset 的 Quote/0 公开资料；构建引用其 Apache-2.0 驱动，不复制代码进本仓库。

```sh
git clone https://github.com/MindReset/dot_open_platform.git ../dot_open_platform
git -C ../dot_open_platform checkout c86603987ba1632b5e2b89fc4704fbf1b007def4
export QUOTE0_PLATFORM="$(cd ../dot_open_platform && pwd)"
cd firmware
idf.py build
```

固件版本由 `version.txt` 管理，格式为 `X.Y.Z`。修改固件并对外打包时更新该版本号，并重新运行 `idf.py reconfigure build`。打包会检查已编译固件的版本与此文件一致。

已用 ESP-IDF 5.4.4 编译通过。执行 `idf.py -p <port> flash` 前，必须核对芯片、Flash 容量和安全配置；发现启用了加密或安全启动时不要继续。为了保留回滚路径，建议在下载模式下用 `esptool read_flash 0 ALL <备份文件>` 完整读取两次、比较 SHA-256。备份请留在仓库外的私有目录，不要上传。不要操作 eFuse。

回滚路径是停止桌面 App，进入同一台设备的 ROM 下载模式，用 `esptool write_flash 0x0 <本机原厂整片备份>` 恢复并确认 Dot. App 能重新连接。上述下划线命令适用于 ESP-IDF 5.4.4 自带的 esptool v4；esptool v5 改用 `read-flash`、`write-flash`。官网工具从自制固件恢复的能力未验证。刷第三方固件可能影响保修。

## 电源与休眠

- 外部供电存在时，保持 USB 串口可用；面板只在刷新时供电，刷新完成后休眠并释放 `GPIO20`，不清除画面。
- `GPIO0` 未检测到外部供电、USB 也无活动且持续至少 2 秒后，尝试进入深睡。重新插线以 `GPIO0` 高电平唤醒，并保留 120 秒定时回退；深睡唤醒及再次刷屏需要在真机上验证。
- `GPIO1` 提供经 ADC 校准和分压换算的电池电压。固件不改变充电芯片和指示灯的行为，不推算精确电量百分比，也不限制充电上限。

## USB 协议 v1

- `Q0PING\n` → `Q0LINK 1 152 296\n`。App 用握手区分 Link 固件与具有相同 VID/PID 的其他设备。
- `Q0VERSION\n` → `Q0FW <X.Y.Z>\n`。版本来自已刷入设备的固件，不是桌面 App 的版本。旧版 Link 固件不支持此查询。
- `Q0STATUS\n` → `Q0POWER 1 <usb> <mv>\n`。`usb` 为 `1`（有外部供电）或 `0`（无）；`mv` 为电池电压（毫伏）。任一数值不可用时使用 `-1`，旧版固件不支持此查询。
- `Q0IMG1 152 296 1BPP 5624 <8 位十六进制 IEEE CRC32>\n` 后面必须紧接 5624 字节。逐行、MSB 先，`0` 黑、`1` 白。
- 刷完回复 `OK\n`；失败回复 `ERR <reason>\n`。应用侧等 ACK 后再发下一帧；断线重连先握手。

图帧格式参考 `stvlynn/quote-buddy`，`Q0PING` 是本项目的握手。
