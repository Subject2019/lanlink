// LanLink Windows 客户端：复用系统自带的 WebView2 渲染 NAS 上的页面，
// 自身不带浏览器内核，因此只有几 MB。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	webview2 "github.com/jchv/go-webview2"
)

const discoveryPort = 8788
const discoveryMagic = "lanlink.discover"

type config struct {
	Server string `json:"server"`
}

func main() {
	serverFlag := flag.String("server", "", "直接指定服务端地址，例如 http://192.168.1.10:8787")
	flag.Parse()

	cfgPath := configPath()
	cfg := loadConfig(cfgPath)

	addr := strings.TrimSpace(*serverFlag)
	if addr == "" {
		addr = cfg.Server
	}
	if addr == "" {
		if found, ok := discover(); ok {
			addr = found
			cfg.Server = found
			saveConfig(cfgPath, cfg)
		}
	}

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  filepath.Join(filepath.Dir(cfgPath), "webview"),
		WindowOptions: webview2.WindowOptions{
			Title:  "LanLink",
			Width:  960,
			Height: 720,
			Center: true,
		},
	})

	if addr == "" {
		// 没有配置也发现不到：先把填写地址的引导页显示出来
		host := "LanLink"
		if w == nil {
			// 连 WebView2 都没有，只能让用户手动打开浏览器
			openBrowser("http://127.0.0.1:8787/")
			return
		}
		w.SetTitle("LanLink - 设置服务端地址")
		_ = w.Bind("saveServer", func(url string) string {
			url = strings.TrimSpace(strings.TrimSuffix(url, "/"))
			if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
				url = "http://" + url
			}
			cfg.Server = url
			saveConfig(cfgPath, cfg)
			w.Dispatch(func() { w.Navigate(url) })
			return "ok"
		})
		w.SetHtml(setupPage(host))
		w.Run()
		return
	}

	if w == nil {
		openBrowser(addr)
		return
	}
	w.Navigate(addr + "/")
	w.Run()
}

func configPath() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	dir := filepath.Join(base, "LanLink")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "config.json")
}

func loadConfig(p string) config {
	var c config
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

func saveConfig(p string, c config) {
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		log.Printf("保存配置失败: %v", err)
	}
}

// discover 向局域网广播探测包，收到服务端应答后返回访问地址。
func discover() (string, bool) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return "", false
	}
	defer conn.Close()

	targets := []string{"255.255.255.255:8788"}
	for _, ip := range broadcastAddrs() {
		targets = append(targets, net.JoinHostPort(ip, fmt.Sprint(discoveryPort)))
	}

	for attempt := 0; attempt < 3; attempt++ {
		for _, t := range targets {
			if addr, err := net.ResolveUDPAddr("udp4", t); err == nil {
				_, _ = conn.WriteTo([]byte(discoveryMagic), addr)
			}
		}
		_ = conn.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
		buf := make([]byte, 512)
		for {
			n, src, err := conn.ReadFrom(buf)
			if err != nil {
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

func openBrowser(url string) {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func setupPage(title string) string {
	return `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>` + title + `</title>
<style>
body{font:15px system-ui,"Microsoft YaHei",sans-serif;background:#0d1117;color:#e6edf3;
display:grid;place-items:center;height:100vh;margin:0}
.box{width:360px}
h2{margin:0 0 6px}p{color:#8b949e;font-size:13px;line-height:1.6}
input{width:100%;padding:10px 12px;border-radius:10px;border:1px solid #30363d;background:#161b22;
color:#e6edf3;outline:none;margin:10px 0}
button{width:100%;padding:10px;border:0;border-radius:10px;background:#2f81f7;color:#fff;font-size:15px}
</style>
<div class=box><h2>连接 LanLink</h2>
<p>没有自动发现 NAS。请填写 NAS 上 LanLink 的地址（服务端启动时会打印，例如 http://192.168.1.10:8787）。</p>
<input id=u placeholder="http://192.168.1.10:8787" autofocus>
<button onclick="go()">连接</button>
<p id=msg></p></div>
<script>
function go(){var v=document.getElementById('u').value.trim();if(!v){return}
 var p=window.saveServer?window.saveServer(v):Promise.reject('不支持');
 Promise.resolve(p).then(function(){document.getElementById('msg').textContent='已保存'})
 .catch(function(e){document.getElementById('msg').textContent=String(e)})}
document.getElementById('u').addEventListener('keydown',function(e){if(e.key==='Enter')go()})
</script></html>`
}
