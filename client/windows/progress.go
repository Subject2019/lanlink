package main

// 系统原生进度条（msctls_progress32）。上传/下载在后台 goroutine 里跑，
// 只用 PostMessage 把进度丢回窗口线程，绝不在工作线程里碰控件。

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

const (
	wmAppProgress = wmApp + 8

	pbsSmooth        = 0x0001
	pbmSetPos        = 0x0402 // WM_USER + 2
	pbmSetRange32    = 0x0406 // WM_USER + 6
	iccProgressClass = 0x0020

	progressMax = 1000 // 0.1% 粒度，大文件也能看出在动
)

var (
	comctl32               = syscall.NewLazyDLL("comctl32.dll")
	procInitCommonControls = comctl32.NewProc("InitCommonControlsEx")
	clsProgress            = utf16ptr("msctls_progress32")

	transferMu     sync.Mutex
	transferActive bool
	transferLabel  string
	transferDone   int64
	transferTotal  int64
)

type initCommonControlsEx struct {
	dwSize uint32
	dwICC  uint32
}

// initProgressControls 在创建进度条之前注册公共控件类。
func initProgressControls() {
	icex := initCommonControlsEx{
		dwSize: uint32(unsafe.Sizeof(initCommonControlsEx{})),
		dwICC:  iccProgressClass,
	}
	_, _, _ = procInitCommonControls.Call(uintptr(unsafe.Pointer(&icex)))
}

func beginTransfer(label string) {
	transferMu.Lock()
	transferActive = true
	transferLabel = label
	transferDone = 0
	transferTotal = 0
	transferMu.Unlock()
	postToWindow(wmAppProgress)
}

func reportTransfer(done, total int64) {
	transferMu.Lock()
	if !transferActive {
		transferMu.Unlock()
		return
	}
	transferDone = done
	transferTotal = total
	transferMu.Unlock()
	postToWindow(wmAppProgress)
}

func endTransfer() {
	transferMu.Lock()
	transferActive = false
	transferMu.Unlock()
	postToWindow(wmAppProgress)
}

type transferSnapshot struct {
	active bool
	label  string
	done   int64
	total  int64
}

func snapshotTransfer() transferSnapshot {
	transferMu.Lock()
	defer transferMu.Unlock()
	return transferSnapshot{transferActive, transferLabel, transferDone, transferTotal}
}

func (w *appWindow) applyTransfer() {
	s := snapshotTransfer()
	if !s.active {
		showControl(w.hwndProgress, false)
		showControl(w.hwndProgressText, false)
		return
	}
	showControl(w.hwndProgress, true)
	showControl(w.hwndProgressText, true)

	pct := 0
	if s.total > 0 {
		pct = int(s.done * progressMax / s.total)
		if pct > progressMax {
			pct = progressMax
		}
	}
	sendMsg(w.hwndProgress, pbmSetPos, uintptr(pct), 0)
	if s.total > 0 {
		setText(w.hwndProgressText,
			fmt.Sprintf("%s %d%% · %s/%s", s.label, pct/10,
				fmtSize(s.done), fmtSize(s.total)))
	} else {
		setText(w.hwndProgressText, fmt.Sprintf("%s … · %s", s.label, fmtSize(s.done)))
	}
}

func showControl(h uintptr, show bool) {
	if h == 0 {
		return
	}
	mode := uintptr(swHide)
	if show {
		mode = swShow
	}
	_, _, _ = procShowWindow.Call(h, mode)
}
