package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

var errUnauthorized = errors.New("尚未加入")

const cookieName = "lanlink_token"

// apiClient 是一层很薄的 HTTP 封装：托盘窗口不经过浏览器，
// 自己带着 lanlink_token 直接跟 NAS 上的服务端通信。
type apiClient struct {
	short *http.Client
	long  *http.Client
}

var api = &apiClient{
	short: &http.Client{Timeout: 6 * time.Second},
	// 十几 GB 的文件在百兆网络下可能要传 20 分钟以上，别用整体超时把它掐断
	long: &http.Client{Timeout: 2 * time.Hour},
}

type apiInfo struct {
	Open     bool   `json:"open"`
	Version  string `json:"version"`
	MaxBytes int64  `json:"maxBytes"`
}

type apiFileMeta struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Mime string `json:"mime"`
}

type apiDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Online   bool   `json:"online"`
}

type apiMessage struct {
	ID       string       `json:"id"`
	Kind     string       `json:"kind"`
	Text     string       `json:"text"`
	File     *apiFileMeta `json:"file"`
	FromID   string       `json:"fromId"`
	FromName string       `json:"fromName"`
	TS       int64        `json:"ts"`
}

type apiState struct {
	Me       *apiDevice     `json:"me"`
	Devices  []apiDevice    `json:"devices"`
	Messages []apiMessage   `json:"messages"`
	Server   map[string]any `json:"server"`
}

func (a *apiClient) request(c Config, method, path string, body io.Reader,
	headers map[string]string, long bool) (*http.Response, error) {
	return a.requestWithLength(c, method, path, body, headers, long, 0)
}

// requestWithLength 额外声明请求体长度，让服务端能在写盘前做剩余空间检查。
// bodyLen <= 0 时保持 net/http 的默认行为。
func (a *apiClient) requestWithLength(c Config, method, path string, body io.Reader,
	headers map[string]string, long bool, bodyLen int64) (*http.Response, error) {

	if c.Server == "" {
		return nil, errors.New("未配置服务器地址")
	}
	req, err := http.NewRequest(method, c.Server+path, body)
	if err != nil {
		return nil, err
	}
	if bodyLen > 0 {
		req.ContentLength = bodyLen
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.Token != "" {
		req.Header.Set("Cookie", cookieName+"="+c.Token)
	}
	client := a.short
	if long {
		client = a.long
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		return nil, errUnauthorized
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := resp.Status
		var e struct {
			Error string `json:"error"`
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return nil, errors.New(msg)
	}
	return resp, nil
}

func (a *apiClient) get(c Config, path string, out any) error {
	resp, err := a.request(c, "GET", path, nil, nil, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func (a *apiClient) postJSON(c Config, path string, payload any, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := a.request(c, "POST", path, bytes.NewReader(b),
		map[string]string{"Content-Type": "application/json"}, false)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// join 返回设备令牌。服务端把 device.id 作为 cookie 值，所以两者都能用。
func (a *apiClient) join(c Config, name, code string) (string, error) {
	resp, err := a.request(c, "POST", "/api/join",
		bytes.NewReader(mustJSON(map[string]string{
			"name": name, "code": code, "platform": "Windows",
		})), map[string]string{"Content-Type": "application/json"}, false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		Device struct {
			ID string `json:"id"`
		} `json:"device"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err == nil && out.Device.ID != "" {
		return out.Device.ID, nil
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == cookieName && ck.Value != "" {
			return ck.Value, nil
		}
	}
	return "", errors.New("加入成功但没拿到令牌")
}

func (a *apiClient) sendText(c Config, text string) error {
	return a.postJSON(c, "/api/messages", map[string]string{"text": text}, nil)
}

// progressFunc 报告已传输字节数与总字节数；total <= 0 表示长度未知。
type progressFunc func(done, total int64)

// countedReader 边传边统计，按约 1%（或 256KB）节流，避免刷爆窗口消息队列。
type countedReader struct {
	r        io.Reader
	done     int64
	total    int64
	last     int64
	onChange progressFunc
}

func (c *countedReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n <= 0 {
		return n, err
	}
	c.done += int64(n)
	step := c.total / 100
	if step < 256<<10 {
		step = 256 << 10
	}
	if c.done-c.last >= step || (c.total > 0 && c.done >= c.total) || err != nil {
		c.last = c.done
		if c.onChange != nil {
			c.onChange(c.done, c.total)
		}
	}
	return n, err
}

// upload 用文件本身的 *os.File 作为请求体，不会把整个文件读进内存。
func (a *apiClient) upload(c Config, file string, onProgress progressFunc) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	total := fi.Size()

	name := filepath.Base(file)
	headers := map[string]string{"Content-Type": "application/octet-stream"}
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		headers["X-File-Type"] = t
	}
	body := &countedReader{r: f, total: total, onChange: onProgress}
	resp, err := a.requestWithLength(c, "PUT",
		"/api/files?name="+url.QueryEscape(name), body, headers, true, total)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// download 先写 .part 再改名，中途失败不会留下半个文件。
func (a *apiClient) download(c Config, m apiMessage, dest string, onProgress progressFunc) error {
	if m.File == nil {
		return errors.New("选中的不是文件")
	}
	path := "/api/files/" + url.PathEscape(m.ID) + "?n=" + url.QueryEscape(m.File.Name)
	resp, err := a.request(c, "GET", path, nil, nil, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	body := &countedReader{r: resp.Body, total: resp.ContentLength, onChange: onProgress}
	_, copyErr := io.Copy(f, body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmp)
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	_ = os.Remove(dest)
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func fmtSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	f := float64(n)
	units := []string{"KB", "MB", "GB", "TB"}
	for _, u := range units {
		f /= unit
		if f < unit {
			return fmt.Sprintf("%.1f %s", f, u)
		}
	}
	return fmt.Sprintf("%.1f PB", f/unit)
}
