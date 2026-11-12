// LanLink Windows 客户端：托盘常驻 + 原生窗口。
// 不内嵌浏览器内核、不必打开浏览器，直接在窗口里收发文本与文件。
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/energye/systray"
	"golang.org/x/sys/windows/registry"
)

//go:embed assets/icon.ico
var iconData []byte

const (
	appVersion       = "1.2.0"
	discoveryPort    = 8788
	discoveryMagic   = "lanlink.discover"
	runKeyPath       = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName     = "LanLink"
	alreadyExists    = syscall.Errno(183) // ERROR_ALREADY_EXISTS
	statusCheckEvery = 60 * time.Second
)

type Config struct {
	Server    string `json:"server"`
	Token     string `json:"token,omitempty"` // 加入后拿到的设备令牌，重开不用再输入加入码
	Name      string `json:"name,omitempty"`
	Autostart bool   `json:"autostart"`
}

var (
	cfgMu   sync.RWMutex
	cfg     Config
	cfgPath string
)

// cfg 会被托盘线程和窗口线程同时读写，统一走这几个入口。
func getConfig() Config {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return cfg
}

func mutateConfig(fn func(*Config)) {
	cfgMu.Lock()
	fn(&cfg)
	snap := cfg
	cfgMu.Unlock()
	saveConfig(snap)
}

func main() {
	if !acquireSingleInstance() {
		return // 已经在运行，静默退出
	}
	cfgPath = configPath()
	cfg = loadConfig()
	if cfg.Server == "" {
		if found, ok := discover(); ok {
			cfg.Server = found
			saveConfig(cfg)
		}
	}
	startWindow()
	systray.Run(onReady, quitWindow)
}

func onReady() {
	systray.SetIcon(iconData)
	systray.SetTitle("LanLink 邻传")
	systray.SetTooltip("LanLink 邻传")

	mOpen := systray.AddMenuItem("打开 LanLink 窗口", "不打开浏览器，直接收发文本与文件")
	mSend := systray.AddMenuItem("快速发送文件…", "直接选文件（可多选）并上传")
	mStatus := systray.AddMenuItem("状态：检测中…", "与 NAS 的连接状态")
	mStatus.Disable()
	mServer := systray.AddMenuItem(serverLabel(), "当前服务端地址")
	mServer.Disable()
	mCopy := systray.AddMenuItem("复制访问地址", "复制到剪贴板")
	systray.AddSeparator()
	mWeb := systray.AddMenuItem("在浏览器中打开", "打开 NAS 上的网页界面")
	mDiscover := systray.AddMenuItem("重新自动发现 NAS", "广播查找局域网里的服务端")
	mEdit := systray.AddMenuItem("编辑配置文件", "用记事本修改服务端地址")
	mReload := systray.AddMenuItem("重新读取配置", "修改配置后点这里生效")
	systray.AddSeparator()
	mAutostart := systray.AddMenuItemCheckbox("开机自动启动", "登录 Windows 后自动常驻状态栏", autostartEnabled())
	systray.AddSeparator()
	mAbout := systray.AddMenuItem("关于", "")
	mQuit := systray.AddMenuItem("退出", "退出托盘程序")

	mOpen.Click(showWindow)
	mSend.Click(pickFilesFromTray)
	mCopy.Click(copyAddress)
	mWeb.Click(openWeb)
	mDiscover.Click(func() {
		if found, ok := discover(); ok {
			mutateConfig(func(c *Config) {
				c.Server = found
				c.Token = "" // 换了服务端，旧令牌不再有效
			})
			mServer.SetTitle(serverLabel())
			go checkStatus(mStatus)
			reloadWindow()
			messageBox("已找到 NAS：" + found)
		} else {
			messageBox("没有找到 NAS，请在配置文件里手动填写地址。")
		}
	})
	mEdit.Click(func() { openFile("notepad.exe", cfgPath) })
	mReload.Click(func() {
		c := loadConfig()
		cfgMu.Lock()
		cfg = c
		cfgMu.Unlock()
		mServer.SetTitle(serverLabel())
		go checkStatus(mStatus)
		reloadWindow()
	})
	mAutostart.Click(func() {
		want := !mAutostart.Checked()
		if err := setAutostart(want); err != nil {
			messageBox("设置开机自启失败：" + err.Error())
			return
		}
		if want {
			mAutostart.Check()
		} else {
			mAutostart.Uncheck()
		}
		mutateConfig(func(c *Config) { c.Autostart = want })
	})
	mAbout.Click(func() {
		messageBox(fmt.Sprintf("LanLink 邻传 %s\n\n自带原生窗口，不打开浏览器也能收发文本与文件；\n文件可以直接拖进窗口，也能从托盘快速发送。\n托盘常驻、支持开机自启；双击托盘图标打开窗口。\n不内嵌浏览器内核，内存占用约 10MB。", appVersion))
	})
	mQuit.Click(systray.Quit)

	systray.SetOnClick(func(m systray.IMenu) {
		if m != nil {
			_ = m.ShowMenu()
		}
	})
	systray.SetOnDClick(func(m systray.IMenu) { showWindow() })
	systray.SetOnRClick(func(m systray.IMenu) {
		if m != nil {
			_ = m.ShowMenu()
		}
	})

	go func() {
		checkStatus(mStatus)
		ticker := time.NewTicker(statusCheckEvery)
		defer ticker.Stop()
		for range ticker.C {
			checkStatus(mStatus)
		}
	}()
}

func serverLabel() string {
	c := getConfig()
	if c.Server == "" {
		return "服务器：未配置（请编辑配置文件）"
	}
	return "服务器：" + c.Server
}

func checkStatus(item *systray.MenuItem) {
	c := getConfig()
	if c.Server == "" {
		item.SetTitle("状态：未配置服务器")
		systray.SetTooltip("LanLink 邻传 · 未配置")
		return
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(c.Server + "/api/info")
	if err != nil {
		item.SetTitle("状态：离线（连不上 NAS）")
		systray.SetTooltip("LanLink 邻传 · 离线")
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		item.SetTitle("状态：异常（HTTP " + resp.Status + "）")
		systray.SetTooltip("LanLink 邻传 · 异常")
		return
	}
	item.SetTitle("状态：在线  ·  " + c.Server)
	systray.SetTooltip("LanLink 邻传 · 在线 · " + c.Server)
}

func openWeb() {
	c := getConfig()
	if c.Server == "" {
		openFile("notepad.exe", cfgPath)
		return
	}
	openFile("rundll32", "url.dll,FileProtocolHandler", c.Server+"/")
}

func openFile(bin string, args ...string) {
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Start()
}

func copyAddress() {
	c := getConfig()
	if c.Server == "" {
		return
	}
	cmd := exec.Command("cmd", "/c", "echo "+c.Server+"/| clip")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run()
}

func messageBox(text string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	msg := user32.NewProc("MessageBoxW")
	body, _ := syscall.UTF16PtrFromString(text)
	title, _ := syscall.UTF16PtrFromString("LanLink 邻传")
	_, _, _ = msg.Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), 0)
}

// ---------- 单实例 ----------

func acquireSingleInstance() bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	createMutex := kernel32.NewProc("CreateMutexW")
	name, _ := syscall.UTF16PtrFromString("LanLinkTraySingleInstance")
	ret, _, errno := createMutex.Call(0, 1, uintptr(unsafe.Pointer(name)))
	if ret == 0 {
		return true
	}
	return errno != alreadyExists
}

// ---------- 配置 ----------

func configPath() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	dir := filepath.Join(base, "LanLink")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "config.json")
}

func loadConfig() Config {
	var c Config
	if b, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	c.Server = normalize(c.Server)
	return c
}

func saveConfig(c Config) {
	b, _ := json.MarshalIndent(c, "", "  ")
	_ = os.WriteFile(cfgPath, b, 0o644)
}

func normalize(s string) string {
	s = strings.TrimSpace(s)
	if s != "" && !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		s = "http://" + s
	}
	return strings.TrimSuffix(s, "/")
}

// ---------- 开机自启 ----------

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(runValueName)
	return err == nil && v != ""
}

func setAutostart(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if !autostartEnabled() {
			return nil
		}
		return k.DeleteValue(runValueName)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(runValueName, `"`+exe+`"`)
}

// ---------- 自动发现 ----------

func discover() (string, bool) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return "", false
	}
	defer conn.Close()

	targets := []string{"255.255.255.255:8788"}
	for _, ip := range broadcastAddrs() {
		targets = append(targets, fmt.Sprintf("%s:%d", ip, discoveryPort))
	}
	for attempt := 0; attempt < 3; attempt++ {
		for _, t := range targets {
			if addr, e := net.ResolveUDPAddr("udp4", t); e == nil {
				_, _ = conn.WriteTo([]byte(discoveryMagic), addr)
			}
		}
		_ = conn.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
		buf := make([]byte, 512)
		for {
			n, src, e := conn.ReadFrom(buf)
			if e != nil {
				break
			}
			var reply struct {
				V    int `json:"v"`
				Port int `json:"port"`
			}
			if json.Unmarshal(buf[:n], &reply) == nil && reply.V == 1 && reply.Port > 0 {
				ip := src.(*net.UDPAddr).IP.String()
				return fmt.Sprintf("http://%s:%d", ip, reply.Port), true
			}
		}
	}
	return "", false
}

func broadcastAddrs() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
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
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			ip := ipnet.IP.To4()
			mask := ipnet.Mask
			if len(mask) == 16 {
				mask = mask[12:]
			}
			if len(mask) != 4 {
				continue
			}
			b := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				b[i] = ip[i] | ^mask[i]
			}
			out = append(out, b.String())
		}
	}
	return out
}
