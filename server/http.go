package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

const cookieName = "lanlink_token"

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"open": a.cfg.Open, "version": version})
	})
	mux.HandleFunc("POST /api/join", a.handleJoin)
	mux.HandleFunc("GET /api/state", a.auth(a.handleState))
	mux.HandleFunc("GET /api/events", a.auth(a.handleEvents))
	mux.HandleFunc("POST /api/messages", a.auth(a.handleSendText))
	mux.HandleFunc("DELETE /api/messages/{id}", a.auth(a.handleDelete))
	mux.HandleFunc("PUT /api/files", a.auth(a.handleUpload))
	mux.HandleFunc("GET /api/files/{id}", a.auth(a.handleDownload))
	mux.HandleFunc("POST /api/device", a.auth(a.handleRename))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	fileServer := http.FileServer(http.FS(a.web))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && strings.HasPrefix(r.URL.Path, "/api") {
			http.NotFound(w, r)
			return
		}
		// index.html 不缓存，静态资源带 hash 时可长期缓存
		if p := path.Clean(r.URL.Path); p == "/" || p == "/index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})

	return logRequests(mux)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/events") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

// ---------- 认证 ----------

func (a *App) currentDevice(r *http.Request) *Device {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	return a.store.Device(c.Value)
}

func (a *App) auth(next func(http.ResponseWriter, *http.Request, *Device)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d := a.currentDevice(r)
		if d == nil {
			fail(w, http.StatusUnauthorized, "未绑定设备，请重新加入")
			return
		}
		next(w, r, d)
	}
}

// handleJoin 校验加入码并发放设备 token（写入 HttpOnly Cookie）。
func (a *App) handleJoin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code     string `json:"code"`
		Name     string `json:"name"`
		Platform string `json:"platform"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if !a.cfg.Open {
		code := strings.TrimSpace(body.Code)
		if subtle.ConstantTimeCompare([]byte(code), []byte(a.cfg.Code)) != 1 {
			fail(w, http.StatusForbidden, "加入码不正确")
			return
		}
	}
	name := cleanName(body.Name)
	if name == "" {
		name = "未命名设备"
	}
	now := time.Now().Unix()
	d := &Device{ID: newID(), Name: name, Platform: clip(body.Platform, 20), Created: now, LastSeen: now}
	a.store.AddDevice(d)

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    d.ID,
		Path:     "/",
		MaxAge:   3600 * 24 * 365,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	a.broadcastDevices()
	writeJSON(w, http.StatusOK, map[string]any{"device": d})
}

// ---------- 状态与事件 ----------

func (a *App) handleState(w http.ResponseWriter, r *http.Request, d *Device) {
	a.store.TouchDevice(d.ID, time.Now().Unix())
	writeJSON(w, http.StatusOK, map[string]any{
		"me":       d,
		"devices":  a.deviceList(),
		"messages": a.store.Messages(),
		"server":   map[string]any{"version": version},
	})
}

func (a *App) deviceList() []map[string]any {
	online := a.hub.Online()
	devs := a.store.Devices()
	out := make([]map[string]any, 0, len(devs))
	for _, d := range devs {
		out = append(out, map[string]any{
			"id":       d.ID,
			"name":     d.Name,
			"platform": d.Platform,
			"online":   online[d.ID],
			"lastSeen": d.LastSeen,
		})
	}
	return out
}

func (a *App) broadcastDevices() {
	a.hub.Broadcast(Event{Type: "devices", Data: a.deviceList()})
}

// handleEvents 是 SSE 长连接：只在有事件时唤醒，空闲零开销。
func (a *App) handleEvents(w http.ResponseWriter, r *http.Request, d *Device) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "不支持流式响应")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sub := a.hub.Subscribe(d.ID)
	defer func() {
		a.hub.Unsubscribe(sub)
		a.broadcastDevices()
	}()
	frame, _ := json.Marshal(Event{Type: "ready", Data: map[string]any{"online": true}})
	fmt.Fprintf(w, "data: %s\n\n", frame)
	flusher.Flush()
	a.broadcastDevices()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-sub.ch:
			if !ok {
				return
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// ---------- 消息 ----------

func (a *App) handleSendText(w http.ResponseWriter, r *http.Request, d *Device) {
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		fail(w, http.StatusBadRequest, "内容为空")
		return
	}
	if len(text) > 32<<10 {
		fail(w, http.StatusRequestEntityTooLarge, "文本过长")
		return
	}
	m := &Message{ID: newID(), Kind: "text", Text: text, FromID: d.ID, FromName: d.Name, TS: time.Now().UnixMilli()}
	a.store.AddMessage(m)
	a.hub.Broadcast(Event{Type: "message", Data: m})
	writeJSON(w, http.StatusOK, m)
}

func (a *App) handleDelete(w http.ResponseWriter, r *http.Request, d *Device) {
	id := r.PathValue("id")
	m := a.store.Message(id)
	if m == nil {
		fail(w, http.StatusNotFound, "记录不存在")
		return
	}
	if m.FromID != d.ID {
		fail(w, http.StatusForbidden, "只能删除自己发送的记录")
		return
	}
	a.store.DeleteMessage(id)
	a.hub.Broadcast(Event{Type: "delete", Data: map[string]string{"id": id}})
	w.WriteHeader(http.StatusNoContent)
}

// handleUpload 把请求体直接流式写到磁盘，不在内存里缓存整个文件。
func (a *App) handleUpload(w http.ResponseWriter, r *http.Request, d *Device) {
	name := cleanName(r.URL.Query().Get("name"))
	if name == "" {
		fail(w, http.StatusBadRequest, "缺少文件名")
		return
	}
	if r.ContentLength > a.cfg.MaxBytes {
		fail(w, http.StatusRequestEntityTooLarge, "文件超出大小限制")
		return
	}
	id := newID()
	dst := a.store.FilePath(id)
	f, err := os.Create(dst)
	if err != nil {
		fail(w, http.StatusInternalServerError, "无法写入文件")
		return
	}
	limited := http.MaxBytesReader(w, r.Body, a.cfg.MaxBytes)
	n, copyErr := io.Copy(f, limited)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(dst)
		fail(w, http.StatusBadRequest, "上传中断")
		return
	}

	mimeType := r.Header.Get("X-File-Type")
	if mimeType == "" {
		mimeType = mime.TypeByExtension(path.Ext(name))
	}
	m := &Message{
		ID:       id,
		Kind:     "file",
		File:     &FileMeta{Name: name, Size: n, Mime: mimeType},
		FromID:   d.ID,
		FromName: d.Name,
		TS:       time.Now().UnixMilli(),
	}
	a.store.AddMessage(m)
	a.hub.Broadcast(Event{Type: "message", Data: m})
	writeJSON(w, http.StatusOK, m)
}

func (a *App) handleDownload(w http.ResponseWriter, r *http.Request, d *Device) {
	m := a.store.Message(r.PathValue("id"))
	if m == nil || m.Kind != "file" || m.File == nil {
		fail(w, http.StatusNotFound, "文件不存在")
		return
	}
	f, err := os.Open(a.store.FilePath(m.ID))
	if err != nil {
		fail(w, http.StatusNotFound, "文件已被删除")
		return
	}
	defer f.Close()

	disposition := fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s",
		asciiFallback(m.File.Name), url.PathEscape(m.File.Name))
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", time.UnixMilli(m.TS), f)
}

func (a *App) handleRename(w http.ResponseWriter, r *http.Request, d *Device) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	name := cleanName(body.Name)
	if name == "" {
		fail(w, http.StatusBadRequest, "名称不能为空")
		return
	}
	a.store.RenameDevice(d.ID, name)
	a.broadcastDevices()
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// ---------- 辅助 ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// cleanName 清理用户提供的名字/文件名：去掉路径分隔符与控制字符，限制长度。
func cleanName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	if len(s) > 200 {
		s = strings.ToValidUTF8(s[:200], "")
	}
	return s
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func asciiFallback(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 128 && r != '"' && r != '\\' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "file"
	}
	return b.String()
}
