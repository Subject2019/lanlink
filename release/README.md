# LanLink v1.0.0 预编译包

本目录是可直接使用的成品，源码见仓库根目录。

| 文件 | 大小 | 用途 |
| --- | --- | --- |
| `lanlink.fpk` | 2.4MB | 飞牛 fnOS 应用包，应用中心「手动安装」 |
| `lanlink-android.apk` | 31KB | 安卓壳（可选，纯浏览器也能用） |
| `lanlink-server` | 5.5MB | Linux x86_64 服务端，静态编译无依赖 |
| `lanlink-server-arm64` | 5.2MB | Linux ARM64 服务端 |
| `sha256sums.txt` | — | 校验和，`sha256sum -c sha256sums.txt` |

## 用飞牛 fnOS

应用中心 → 手动安装 → 选 `lanlink.fpk`，安装时选一个没被占用的端口（默认 8787）。
装好后桌面出现「LanLink 邻传」，手机/电脑在同一局域网打开 `http://<NAS地址>:<端口>` 即可。

数据默认存在应用共享目录（文件管理器里能看到「LanLink → data」）。
想换到自己的文件夹：**先**在「应用设置 → 授权目录」授权该目录，**再**在「数据目录」里填同一个路径保存，
顺序反了会提示无法写入——这是飞牛的目录授权机制，不是故障。

## 用其他 Linux / NAS

```bash
chmod +x lanlink-server
./lanlink-server -addr :8787 -data ./data
```

或用 systemd 常驻（仓库里 `deploy/install.sh` + `deploy/lanlink.service`）。
默认会打印加入码与访问地址；加 `-open` 可免加入码。

## 校验

```bash
sha256sum -c sha256sums.txt
```

## 说明

- 服务端零运行时依赖，实测常驻内存约 12MB。
- 客户端不需要安装任何东西，浏览器直接打开即可；Android APK 只是省去手输地址。
- 端口、数据目录都可在应用设置里改，改完重启应用生效。
