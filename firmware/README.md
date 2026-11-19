# Quote0 Link 固件

仅针对标准版 Quote/0（ESP32-C3、UC8251D）。不支持标为「首发版」的 UC8151 设备；App 会禁止为首发版安装 Link 固件。请优先用完整 App 包中的「设备维护」完成备份和恢复。下列是开发构建和命令行回滚的备用路径。硬件和显示驱动来源：MindReset 的 Quote/0 公开资料；构建引用其 Apache-2.0 驱动，不复制代码进本仓库。

```sh
git clone https://github.com/MindReset/dot_open_platform.git ../dot_open_platform
git -C ../dot_open_platform checkout c86603987ba1632b5e2b89fc4704fbf1b007def4
export QUOTE0_PLATFORM="$(cd ../dot_open_platform && pwd)"
cd firmware
idf.py build
```

已用 ESP-IDF 5.4.4 编译通过。在执行 `idf.py -p <port> flash` 之前，必须核对同一台设备的芯片、Flash 容量和安全配置，并在下载模式下用 `esptool read_flash 0 ALL <备份文件>` 完整读取两次、比较 SHA-256。请把原厂备份存到仓库外的私有目录，不要上传。若无法读取、两次结果不同，或发现启用了加密/安全启动，不要继续。不要操作 eFuse。

回滚路径是停止桌面 App，进入同一台设备的 ROM 下载模式，用 `esptool write_flash 0x0 <本机原厂整片备份>` 恢复并确认 Dot. App 能重新连接。上述下划线命令适用于 ESP-IDF 5.4.4 自带的 esptool v4；esptool v5 改用 `read-flash`、`write-flash`。官网工具从自制固件恢复的能力未验证。刷第三方固件可能影响保修。

## USB 协议 v1

- `Q0PING\n` → `Q0LINK 1 152 296\n`。App 用握手区分 Link 固件与具有相同 VID/PID 的其他设备。
- `Q0IMG1 152 296 1BPP 5624 <8 位十六进制 IEEE CRC32>\n` 后面必须紧接 5624 字节。逐行、MSB 先，`0` 黑、`1` 白。
- 刷完回复 `OK\n`；失败回复 `ERR <reason>\n`。应用侧等 ACK 后再发下一帧；断线重连先握手。

图帧格式参考 `stvlynn/quote-buddy`，`Q0PING` 是本项目的握手。
