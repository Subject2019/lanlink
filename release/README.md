# LanLink v1.2.0 预编译包

本目录是可直接使用的成品，源码见仓库根目录。校验方式见文末。

| 文件 | 大小 | 用途 |
| --- | --- | --- |
| `lanlink.fpk` | 2.4MB | 飞牛 fnOS 应用包，应用中心「手动安装」 |
| `lanlink-server` | 5.5MB | Linux x86_64 服务端，静态编译无依赖 |
| `lanlink-server-arm64` | 5.2MB | Linux ARM64 服务端 |
| `lanlink-tray.exe` | 5.8MB | Windows 客户端（托盘 + 原生收发窗口，可选） |
| `lanlink-android.apk` | 80KB | 安卓纯原生客户端（可选，不加载网页） |
| `sha256sums.txt` | — | 校验和，`sha256sum -c sha256sums.txt` |

## 用飞牛 fnOS

应用中心 → 手动安装 → 选 `lanlink.fpk`，安装时选一个没被占用的端口（默认 8787），
**数据目录建议直接填大容量存储池上的路径**（大文件都存这里）。
装好后桌面出现「LanLink 邻传」，手机/电脑在同一局域网打开 `http://<NAS地址>:<端口>` 即可。

飞牛不允许应用直接读写用户目录，所以自定义数据目录的完整流程是：

1. 安装向导填好目录 → 完成安装（此时先照常用默认目录，不会失败）；
2. 「应用设置 → 授权目录」把该目录授权给 LanLink；
3. 重启一次应用，会自动切过去。

**单个文件大小上限默认 64GB**（v1.2.0 起，旧版是 4GB）。要改就在
`/var/apps/lanlink/etc/settings.conf` 加一行 `max_mb=131072`（单位 MB）后重启应用。
上传前服务端还会检查剩余空间，不够会明确提示，不会留下半个文件。

## 用其他 Linux / NAS

```bash
chmod +x lanlink-server
./lanlink-server -addr :8787 -data ./data
```

或用 systemd 常驻（仓库里 `deploy/install.sh` + `deploy/lanlink.service`）。
默认会打印加入码与访问地址；加 `-open` 可免加入码。
单文件上限用 `-max-mb`（MB）或环境变量 `LANLINK_MAX_MB` 调整，默认 64GB。

## Windows 客户端

双击安装/运行 `lanlink-tray.exe`（免安装、纯 Go、不需要 .NET 或 WebView2）。
双击托盘图标打开原生窗口即可收发文本与文件，文件也能直接拖进窗口；
上传/下载都有原生进度条，超过服务端单文件上限会当场弹窗说明。
托盘菜单可勾选开机自启、复制访问地址、重新自动发现 NAS。

## 安卓客户端

`lanlink-android.apk` 是**纯原生界面**，不内嵌 WebView、不加载服务端网页，
直连服务端 API（SSE 即时收消息）。文件卡片旁有环形上传/下载进度，
支持多选文件、双向系统分享、下载目录授权。

## 校验

```bash
sha256sum -c sha256sums.txt
```

## 说明

- 服务端零运行时依赖，实测常驻内存约 12MB。
- 客户端都不是必需的：浏览器直接打开 `http://<NAS地址>:<端口>` 就能收发。
- 端口、数据目录都可在应用设置里改，改完重启应用生效。
