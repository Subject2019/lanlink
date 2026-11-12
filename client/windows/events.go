package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	eventsMu     sync.Mutex
	eventsCancel context.CancelFunc
)

// restartEvents 在服务器地址或登录态变化后，让长连接立刻重连。
func restartEvents() {
	eventsMu.Lock()
	cancel := eventsCancel
	eventsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// eventsLoop 维持一条 SSE 长连接：服务端据此把本设备标记为在线，
// 新消息到达时也可以立刻通知窗口刷新，不必等轮询。
// 轮询仍然保留作兜底，SSE 断线不影响收发。
func eventsLoop() {
	client := &http.Client{} // 长连接不设总超时，服务端每 25 秒发 ping
	for {
		c := getConfig()
		if c.Server == "" || c.Token == "" {
			time.Sleep(2 * time.Second)
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		eventsMu.Lock()
		eventsCancel = cancel
		eventsMu.Unlock()

		req, err := http.NewRequestWithContext(ctx, "GET", c.Server+"/api/events", nil)
		if err == nil {
			req.Header.Set("Cookie", cookieName+"="+c.Token)
			resp, e := client.Do(req)
			if e == nil {
				scanner := bufio.NewScanner(resp.Body)
				scanner.Buffer(make([]byte, 64*1024), 1024*1024)
				for scanner.Scan() {
					line := scanner.Text()
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var ev struct {
						Type string `json:"type"`
					}
					if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) == nil &&
						(ev.Type == "message" || ev.Type == "delete" || ev.Type == "devices") {
						postToWindow(wmAppRefresh)
					}
				}
				_ = resp.Body.Close()
			}
		}
		// cancel 幂等；eventsCancel 下一轮会被覆盖，旧函数随 GC 回收
		cancel()
		time.Sleep(2 * time.Second)
	}
}
