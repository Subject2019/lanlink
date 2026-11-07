package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// FileMeta 描述一个已保存到磁盘的文件（文件名不做磁盘映射，磁盘名统一用消息 ID）。
type FileMeta struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Mime string `json:"mime"`
}

// Message 是聊天流中的一条记录：纯文本或一个文件。
type Message struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"` // text | file
	Text     string    `json:"text,omitempty"`
	File     *FileMeta `json:"file,omitempty"`
	FromID   string    `json:"fromId"`
	FromName string    `json:"fromName"`
	TS       int64     `json:"ts"`
}

type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Created  int64  `json:"created"`
	LastSeen int64  `json:"lastSeen"`
}

type configFile struct {
	Code string `json:"code"`
}

type stateFile struct {
	Devices  map[string]*Device `json:"devices"`
	Messages []*Message         `json:"messages"`
}

// Store 用单个 JSON 文件保存状态，文件本身放在磁盘上，避免引入数据库。
type Store struct {
	mu      sync.RWMutex
	dir     string
	state   stateFile
	maxMsgs int
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, maxMsgs: 500}
	s.state.Devices = map[string]*Device{}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err == nil {
		_ = json.Unmarshal(b, &s.state)
		if s.state.Devices == nil {
			s.state.Devices = map[string]*Device{}
		}
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	b, err := json.Marshal(&s.state)
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, "state.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, "state.json"))
}

func (s *Store) Device(id string) *Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.state.Devices[id]
	if !ok {
		return nil
	}
	cp := *d
	return &cp
}

func (s *Store) AddDevice(d *Device) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Devices[d.ID] = d
	_ = s.saveLocked()
}

// TouchDevice 只更新内存里的最后活跃时间，不落盘（避免每次刷新都写文件）。
func (s *Store) TouchDevice(id string, ts int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.state.Devices[id]; ok {
		d.LastSeen = ts
	}
}

func (s *Store) RenameDevice(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.state.Devices[id]; ok {
		d.Name = name
		_ = s.saveLocked()
	}
}

func (s *Store) Devices() []*Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Device, 0, len(s.state.Devices))
	for _, d := range s.state.Devices {
		cp := *d
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created < out[j].Created })
	return out
}

func (s *Store) Messages() []*Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Message, len(s.state.Messages))
	copy(out, s.state.Messages)
	return out
}

func (s *Store) AddMessage(m *Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Messages = append(s.state.Messages, m)
	if len(s.state.Messages) > s.maxMsgs {
		drop := len(s.state.Messages) - s.maxMsgs
		for _, old := range s.state.Messages[:drop] {
			if old.Kind == "file" {
				_ = os.Remove(s.FilePath(old.ID))
			}
		}
		s.state.Messages = append([]*Message{}, s.state.Messages[drop:]...)
	}
	_ = s.saveLocked()
}

func (s *Store) DeleteMessage(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, m := range s.state.Messages {
		if m.ID == id {
			s.state.Messages = append(s.state.Messages[:i], s.state.Messages[i+1:]...)
			if m.Kind == "file" {
				_ = os.Remove(s.FilePath(m.ID))
			}
			_ = s.saveLocked()
			return true
		}
	}
	return false
}

func (s *Store) Message(id string) *Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.state.Messages {
		if m.ID == id {
			cp := *m
			return &cp
		}
	}
	return nil
}

// FilePath 返回文件在磁盘上的真实路径（文件名即消息 ID，规避中文/特殊字符问题）。
func (s *Store) FilePath(id string) string {
	return filepath.Join(s.dir, "files", id)
}

// LoadCode 读取或生成加入码。加入码只用于首次绑定设备，之后用 token 认证。
func LoadCode(dir string) (string, error) {
	p := filepath.Join(dir, "config.json")
	b, err := os.ReadFile(p)
	if err == nil {
		var c configFile
		if json.Unmarshal(b, &c) == nil && len(c.Code) >= 4 {
			return c.Code, nil
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	c := configFile{Code: randomCode(6)}
	b, _ = json.Marshal(c)
	return c.Code, os.WriteFile(p, b, 0o600)
}

func randomCode(n int) string {
	const digits = "0123456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "123456"
	}
	for i := range buf {
		buf[i] = digits[int(buf[i])%len(digits)]
	}
	return string(buf)
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))[:32]
	}
	return hex.EncodeToString(b)
}
