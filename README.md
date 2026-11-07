# LanLink

局域网内 Windows / Android 互传文本与文件的极简方案。

**一个服务端跑在 NAS 上，客户端不带浏览器内核。**

## 为什么这样设计

| | 旧方案（Electron） | 现在 |
| --- | --- | --- |
| Windows 客户端 | Electron + React，安装包 ~90MB，解压 377MB | 3.1MB 单文件 exe，复用系统 WebView2 |
| 服务端 | 每个客户端各跑一份 | NAS 上只有一份 5.5MB 静态二进制 |
| Android | WebView 壳 + 打包前端资源 | 23KB APK，纯壳，界面全由 NAS 下发 |
| 空闲内存 | 每台设备 150MB+ | 服务端约 12MB，客户端就是一个标签页 |
| 依赖 | Node / pnpm / Electron 运行时 | 无运行时依赖，`CGO_ENABLED=0` 纯静态 |

界面只有一份（NAS 上的 `server/web/`，原生 HTML/CSS/JS，零框架，全部约 30KB），
所以任何改版对 Windows 和 Android 同时生效，客户端永远不需要重新打包。

## 目录

```
server/            Go 服务端（单二进制，前端用 embed 打进包里）
  web/             前端：index.html / style.css / app.js
client/windows/    Windows 客户端（WebView2 壳 + UDP 自动发现）
client/android/    Android 客户端（WebView 壳 + UDP 自动发现）
deploy/            systemd 单元与 NAS 安装脚本
build.sh           一键构建
```

## 快速开始（开发）

```bash
./build.sh server
./dist/lanlink-server -addr :8787 -data ./data
```

启动后终端会打印加入码和访问地址，例如：

```
 加入码  : 370950
 访问地址:
   http://192.168.1.10:8787/?c=370950
```

浏览器打开任意一台设备的地址即可用；首次进入输入加入码绑定设备（之后靠 Cookie 记住）。

## 部署到 NAS（开机自启）

```bash
./build.sh server
sudo ./deploy/install.sh
systemctl status lanlink
journalctl -u lanlink -n 30 --no-pager     # 查看加入码
```

默认端口 8787（`LANLINK_PORT=xxxx sudo ./deploy/install.sh` 可改），数据目录 `/var/lib/lanlink`。
systemd 单元把常驻内存限制在 256MB（实际用不到 15MB）。

## 客户端

- **Windows**：`dist/lanlink.exe` 双击运行。首次会用 UDP 广播找 NAS，找到就直接打开；
  找不到会弹出填写地址的页面。窗口用的是系统自带 WebView2，所以体积只有 3MB。
  也可以用 `lanlink.exe -server http://192.168.1.10:8787` 直接指定。
- **Android**：`dist/lanlink-android.apk`，同样自动发现 NAS，找不到会让你手填地址。
  支持相册/文件选择上传、下载到系统下载目录、返回键回退。

不想装客户端也可以直接用浏览器访问地址，功能完全一致。

## 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/join` | 用加入码换取设备 Cookie |
| GET | `/api/state` | 设备列表 + 最近 500 条消息 |
| GET | `/api/events` | SSE 长连接，推送新消息/设备变化 |
| POST | `/api/messages` | 发送文本 |
| PUT | `/api/files?name=x` | 流式上传文件（请求体就是文件） |
| GET | `/api/files/{id}?n=x` | 下载文件 |
| DELETE | `/api/messages/{id}` | 删除自己的记录 |
| POST | `/api/device` | 修改自己的设备名 |

自动发现：UDP 8788，发送 `lanlink.discover`，服务端回 `{"v":1,"port":8787}`，
客户端用应答包的源 IP 拼地址，因此不需要任何配置。

## 资源占用实测

| 项 | 数值 |
| --- | --- |
| 服务端二进制 | 5.5MB（静态链接，无依赖） |
| 服务端常驻内存 | ~11.6MB |
| Windows 客户端 | 3.1MB |
| Android APK | 23KB |
| 前端资源 | 约 30KB |

事件推送用 SSE：没有新消息时服务端不产生任何轮询和 CPU 开销，空闲连接只占一个 goroutine。
