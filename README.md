# LanLink 邻传

局域网内互传文本与文件的极简方案：**NAS 上跑一个服务端，手机/平板/电脑打开网页就能用**。

## 为什么这样设计

| | 常见做法（Electron/客户端） | 现在 |
| --- | --- | --- |
| 服务端 | 每台设备各跑一份 | NAS 上一份 5.5MB 静态二进制 |
| 客户端 | 各平台各一个安装包（~100MB 起步） | **浏览器**，零安装 |
| 安卓可选壳 | — | 23KB APK（只为自动发现省一次输地址） |
| 空闲内存 | 每端 150MB+ | 服务端约 12MB |
| 依赖 | Node / Electron / 运行时 | 无运行时依赖，`CGO_ENABLED=0` 纯静态 |

界面只有一份（`server/web/`，原生 HTML/CSS/JS，约 20KB，无框架），
改版对所有设备同时生效，永远不需要重新打包客户端。

## 安装到飞牛 fnOS

仓库提供已打包好的原生应用（不是 Docker 应用，直接跑二进制）：

```bash
./build.sh fnos          # 产出 dist/lanlink.fpk
```

然后在 fnOS「应用中心 → 手动安装」选择 `lanlink.fpk`。安装后：

- 桌面出现「LanLink 邻传」图标，点开即用；
- 服务端口 **8787**，手机在同一局域网打开 `http://<NAS地址>:8787` 即可；
- 数据（消息记录 + 文件）存放在数据共享目录 `lanlink/data`，即
  `/var/apps/lanlink/share/data`；
- 日志在 `/var/apps/lanlink/var/app.log`。

**关于加入码**：fpk 默认以 `-open` 启动，局域网内设备只要填个设备名就能加入（家里用最省事）。
想改成需要加入码：编辑 `/var/apps/lanlink/target/app/cmd/main`，把启动命令里的 `-open` 删掉，
然后 `应用中心 → 停止 → 启动`；此时加入码会打印在 `app.log` 里，访问链接形如
`http://<NAS>:8787/?c=加入码`。

> fpk 包结构遵循飞牛官方 native 应用规范：`manifest` / `config/{privilege,resource}` /
> `app/ui/config` / `cmd/main`（start|stop|status）/ 图标 64+256。

## 手动部署（非飞牛）

```bash
./build.sh server
./dist/lanlink-server -addr :8787 -data ./data        # 前台试跑
sudo ./deploy/install.sh                              # systemd 常驻（Debian/Ubuntu）
```

启动后终端会打印加入码和访问地址。systemd 单元把常驻内存上限设在 256MB（实际约 12MB）。

## 安卓壳（可选）

`dist/lanlink-android.apk` 是 23KB 的 WebView 壳，启动时用 UDP 广播自动找 NAS，
找不到再让你填地址；支持选文件上传、下载到系统下载目录。不装也能用浏览器访问。

## 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/info` | 公开信息（是否免加入码、版本） |
| POST | `/api/join` | 加入并换取设备 Cookie |
| GET | `/api/state` | 设备列表 + 最近 500 条消息 |
| GET | `/api/events` | SSE 长连接，推送新消息/设备变化 |
| POST | `/api/messages` | 发送文本 |
| PUT | `/api/files?name=x` | 流式上传文件（请求体就是文件） |
| GET | `/api/files/{id}?n=x` | 下载文件 |
| DELETE | `/api/messages/{id}` | 删除自己的记录 |
| POST | `/api/device` | 修改自己的设备名 |

自动发现：UDP 8788，发送 `lanlink.discover`，服务端回 `{"v":1,"port":8787}`，
客户端用应答包的源 IP 拼地址，因此不需要任何配置。

## 目录

```
server/                 Go 服务端（前端 embed 进二进制）
  web/                  index.html / style.css / app.js
client/android/         Android WebView 壳
deploy/fnos/            飞牛应用包（manifest/config/cmd/app）
deploy/lanlink.service  systemd 单元 + install.sh
tools/icons/            图标生成器（纯标准库绘制）
build.sh                一键构建
```

## 资源占用实测

| 项 | 数值 |
| --- | --- |
| 服务端二进制 | 5.5MB（静态链接，无依赖） |
| 服务端常驻内存 | ~11.6MB |
| 飞牛 fpk 安装包 | 2.4MB |
| Android APK | 23KB |
| 前端资源 | 约 20KB |

事件推送用 SSE：没有新消息时服务端不产生任何轮询和 CPU 开销，空闲连接只占一个 goroutine。

## 许可

MIT
