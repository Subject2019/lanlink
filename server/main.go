package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

type App struct {
	cfg   *Config
	store *Store
	hub   *Hub
	web   fs.FS
}

func main() {
	addr := flag.String("addr", envOr("LANLINK_ADDR", ":8787"), "监听地址，例如 :8787")
	dataDir := flag.String("data", envOr("LANLINK_DATA", "./data"), "数据目录")
	// 默认 64GB：局域网里传十几 GB 的蓝光原盘/录像很常见，4GB 的旧默认值会直接挡掉。
	// 真正的兜底是磁盘剩余空间检查（见 handleUpload），上限只用来防止误传超大文件。
	defaultMaxMB := int64(65536)
	if v := os.Getenv("LANLINK_MAX_MB"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			defaultMaxMB = n
		}
	}
	maxMB := flag.Int64("max-mb", defaultMaxMB,
		"单个文件大小上限（MB），默认 65536；也可用 LANLINK_MAX_MB 覆盖")
	discover := flag.Bool("discover", true, "开启 UDP 自动发现，客户端可零配置连接")
	open := flag.Bool("open", envOr("LANLINK_OPEN", "") == "1", "免加入码：局域网设备填个名字即可加入")
	flag.Parse()

	log.SetFlags(log.Ltime)

	abs, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatalf("数据目录无效: %v", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		log.Fatalf("无法创建数据目录: %v", err)
	}
	// 目录存在不代表能写（换到别的用户目录时很常见），启动时就探一下，别等上传时才报错
	probe := filepath.Join(abs, ".write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		log.Fatalf("数据目录不可写，请更换数据目录或检查权限: %v", err)
	}
	_ = os.Remove(probe)

	store, err := OpenStore(abs)
	if err != nil {
		log.Fatalf("打开数据存储失败: %v", err)
	}
	code, err := LoadCode(abs)
	if err != nil {
		log.Fatalf("读取加入码失败: %v", err)
	}

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("前端资源缺失: %v", err)
	}

	app := &App{
		cfg:   &Config{Addr: *addr, DataDir: abs, Code: code, MaxBytes: *maxMB << 20, Open: *open},
		store: store,
		hub:   NewHub(),
		web:   sub,
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// 大文件上传可能很慢，不设置读写超时；空闲长连接由 TCP keepalive 维护。
		IdleTimeout: 120 * time.Second,
	}

	printBanner(*addr, abs, code, *open)

	if *discover {
		port := 8787
		if _, p, err := net.SplitHostPort(*addr); err == nil {
			if v, convErr := strconv.Atoi(p); convErr == nil {
				port = v
			}
		}
		conn, err := startDiscovery(8788, port)
		if err != nil {
			log.Printf("自动发现未启动（UDP 8788 可能被占用）: %v", err)
		} else {
			defer conn.Close()
			log.Println("自动发现已开启（UDP 8788）")
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("启动失败: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("正在退出…")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}

type Config struct {
	Addr     string
	DataDir  string
	Code     string
	MaxBytes int64
	Open     bool
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// printBanner 打印局域网可访问地址，方便在手机/平板上直接输入。
func printBanner(addr, dataDir, code string, open bool) {
	port := addr
	if h, p, err := net.SplitHostPort(addr); err == nil {
		_ = h
		port = p
	}
	fmt.Println("──────────────────────────────────────────")
	fmt.Println(" LanLink 已启动")
	fmt.Println(" 数据目录:", dataDir)
	if open {
		fmt.Println(" 加入码  : 已关闭（-open，局域网内填名字即可加入）")
	} else {
		fmt.Printf(" 加入码  : %s\n", code)
	}
	fmt.Println(" 访问地址:")
	suffix := "/?c=" + code
	if open {
		suffix = "/"
	}
	for _, u := range lanURLs(port) {
		fmt.Printf("   %s%s\n", u, suffix)
	}
	fmt.Printf("   http://127.0.0.1:%s%s\n", port, suffix)
	fmt.Println("──────────────────────────────────────────")
}

func lanURLs(port string) []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil || ipnet.IP.IsLinkLocalUnicast() {
				continue
			}
			// 只展示常见家庭/办公网段，过滤容器虚拟网卡
			ip := ipnet.IP.To4()
			if ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31 {
				continue
			}
			out = append(out, "http://"+ip.String()+":"+port)
		}
	}
	return out
}
