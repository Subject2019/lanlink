package main

// 纯 Win32 原生窗口：不内嵌浏览器内核、不引入 GUI 框架，
// 用系统自带控件实现「不开浏览器也能收发文本与文件」。
// 所有 DLL 调用都走 syscall.LazyProc，因此整个程序仍是 CGO_ENABLED=0 的静态 exe。

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	wsBorder           = 0x00800000
	wsVScroll          = 0x00200000
	wsTabStop          = 0x00010000

	esMultiLine   = 0x0004
	esAutoVScroll = 0x0040
	esAutoHScroll = 0x0080
	esWantReturn  = 0x1000

	lbsNotify           = 0x0001
	lbsHasStrings       = 0x0040
	lbsNoIntegralHeight = 0x0100

	lbAddString    = 0x0180
	lbResetContent = 0x0184
	lbSetCurSel    = 0x0186
	lbGetCurSel    = 0x0188
	lbSetTopIndex  = 0x0197

	bnClicked   = 0
	lbnDblClk   = 2
	wmSetFont   = 0x0030
	wmSize      = 0x0005
	wmClose     = 0x0010
	wmDestroy   = 0x0002
	wmCommand   = 0x0111
	wmDropFiles = 0x0233

	swHide = 0
	swShow = 5

	wmApp        = 0x8000
	wmAppShow    = wmApp + 1
	wmAppQuit    = wmApp + 2
	wmAppReload  = wmApp + 3
	wmAppPick    = wmApp + 4
	wmAppStatus  = wmApp + 5
	wmAppState   = wmApp + 6
	wmAppRefresh = wmApp + 7

	ofnAllowMultiSelect = 0x00000200
	ofnPathMustExist    = 0x00000800
	ofnFileMustExist    = 0x00001000
	ofnExplorer         = 0x00080000
	ofnHideReadOnly     = 0x00000004
	ofnOverwritePrompt  = 0x00000002
	ofnNoChangeDir      = 0x00000008

	defaultFont = 17 // DEFAULT_GUI_FONT
	idiArrow    = 32512
	idiAppIcon  = 32512
	btnFace     = 16 // COLOR_BTNFACE + 1
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	procRegisterClassEx  = user32.NewProc("RegisterClassExW")
	procCreateWindowEx   = user32.NewProc("CreateWindowExW")
	procShowWindow       = user32.NewProc("ShowWindow")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procGetMessage       = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessage  = user32.NewProc("DispatchMessageW")
	procPostMessage      = user32.NewProc("PostMessageW")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procDefWindowProc    = user32.NewProc("DefWindowProcW")
	procSendMessage      = user32.NewProc("SendMessageW")
	procSetWindowText    = user32.NewProc("SetWindowTextW")
	procGetWindowText    = user32.NewProc("GetWindowTextW")
	procGetTextLength    = user32.NewProc("GetWindowTextLengthW")
	procMoveWindow       = user32.NewProc("MoveWindow")
	procLoadCursor       = user32.NewProc("LoadCursorW")
	procLoadIcon         = user32.NewProc("LoadIconW")
	procSetDPIAware      = user32.NewProc("SetProcessDPIAware")
	procGetClientRect    = user32.NewProc("GetClientRect")

	procGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
	procGetStockObject  = gdi32.NewProc("GetStockObject")

	procGetOpenFileName = comdlg32.NewProc("GetOpenFileNameW")
	procGetSaveFileName = comdlg32.NewProc("GetSaveFileNameW")

	procDragAcceptFiles = shell32.NewProc("DragAcceptFiles")
	procDragQueryFile   = shell32.NewProc("DragQueryFileW")
	procDragFinish      = shell32.NewProc("DragFinish")

	clsStatic = utf16ptr("STATIC")
	clsEdit   = utf16ptr("EDIT")
	clsButton = utf16ptr("BUTTON")
	clsList   = utf16ptr("LISTBOX")
	clsWindow = utf16ptr("LanLinkMainWindow")
	winTitle  = utf16ptr("LanLink 邻传")
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type winPoint struct{ x, y int32 }

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      winPoint
}

// 与 Win32 OPENFILENAMEW 逐字段对应，Go 的对齐规则与 C 一致。
type openFileName struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

type appWindow struct {
	hwnd   uintptr
	hInst  uintptr
	hFont  uintptr
	labels [2]uintptr

	hwndTop    uintptr
	hwndStatus uintptr
	hwndName   uintptr
	hwndCode   uintptr
	hwndJoin   uintptr
	hwndRefr   uintptr
	hwndList   uintptr
	hwndInput  uintptr
	hwndSend   uintptr
	hwndPick   uintptr
	hwndSave   uintptr

	hwndProgress     uintptr
	hwndProgressText uintptr

	messages []apiMessage
	lastSig  string
}

var (
	mainHWND     atomic.Uintptr
	theWindow    *appWindow // 只在窗口所属线程访问
	statusMu     sync.Mutex
	statusText   string
	stateMu      sync.Mutex
	pendingState *apiState
	refreshMu    sync.Mutex
)

// ---------- 对外的小接口：托盘菜单都通过 PostMessage 通知窗口线程 ----------

func showWindow() { postToWindow(wmAppShow) }
func quitWindow() { postToWindow(wmAppQuit) }
func reloadWindow() {
	restartEvents()
	postToWindow(wmAppReload)
}
func pickFilesFromTray() {
	// 托盘里直接发文件时先把窗口带出来，否则文件对话框没有可见的 owner
	postToWindow(wmAppShow)
	postToWindow(wmAppPick)
}

func postToWindow(msg uintptr) {
	if h := mainHWND.Load(); h != 0 {
		_, _, _ = procPostMessage.Call(h, msg, 0, 0)
	}
}

func setStatus(s string) {
	statusMu.Lock()
	statusText = s
	statusMu.Unlock()
	postToWindow(wmAppStatus)
}

func postState(st *apiState) {
	stateMu.Lock()
	pendingState = st
	stateMu.Unlock()
	postToWindow(wmAppState)
}

func startWindow() {
	go runWindow()
	go eventsLoop()
	go func() {
		refreshState()
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for range t.C {
			refreshState()
		}
	}()
}

// ---------- 窗口生命周期 ----------

func runWindow() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	_, _, _ = procSetDPIAware.Call()
	initProgressControls()
	hinst, _, _ := procGetModuleHandle.Call(0)
	hCursor, _, _ := procLoadCursor.Call(0, idiArrow)
	hIcon, _, _ := procLoadIcon.Call(0, idiAppIcon)
	hFont, _, _ := procGetStockObject.Call(defaultFont)

	w := &appWindow{hInst: hinst, hFont: hFont}
	theWindow = w // CreateWindowEx 期间就会派发 WM_SIZE，必须先设好

	wcex := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     hinst,
		hIcon:         hIcon,
		hCursor:       hCursor,
		hbrBackground: btnFace,
		lpszClassName: clsWindow,
	}
	if ret, _, _ := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wcex))); ret == 0 {
		setStatus("窗口类注册失败")
		return
	}

	hwnd, _, _ := procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(clsWindow)),
		uintptr(unsafe.Pointer(winTitle)),
		// 去掉 WS_THICKFRAME：固定尺寸但仍可最小化/最大化，控件布局不会被拖乱
		uintptr(wsOverlappedWindow&^0x00040000),
		100, 100, 780, 620,
		0, 0, hinst, 0,
	)
	if hwnd == 0 {
		setStatus("创建窗口失败")
		return
	}
	w.hwnd = hwnd
	mainHWND.Store(hwnd)

	w.createControls()
	_, _, _ = procDragAcceptFiles.Call(hwnd, 1)
	w.layoutFromClient()
	postToWindow(wmAppStatus) // 把窗口创建前攒下的状态文本显示出来

	var msg winMsg
	for {
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
	mainHWND.Store(0)
}

func (w *appWindow) control(class *uint16, text string, style uint32, x, y, cx, cy int) uintptr {
	h, _, _ := procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(class)),
		uintptr(unsafe.Pointer(utf16ptr(text))),
		uintptr(style),
		uintptr(x), uintptr(y), uintptr(cx), uintptr(cy),
		w.hwnd, 0, w.hInst, 0,
	)
	if h != 0 && w.hFont != 0 {
		_, _, _ = procSendMessage.Call(h, wmSetFont, w.hFont, 1)
	}
	return h
}

func (w *appWindow) createControls() {
	base := uint32(wsChild | wsVisible | wsTabStop)
	plain := uint32(wsChild | wsVisible)

	w.hwndTop = w.control(clsStatic, "正在连接…", plain, 0, 0, 10, 10)
	w.labels[0] = w.control(clsStatic, "设备名", plain, 0, 0, 10, 10)
	w.labels[1] = w.control(clsStatic, "加入码", plain, 0, 0, 10, 10)
	w.hwndName = w.control(clsEdit, "Windows 电脑", base|wsBorder|esAutoHScroll, 0, 0, 10, 10)
	w.hwndCode = w.control(clsEdit, "", base|wsBorder|esAutoHScroll, 0, 0, 10, 10)
	w.hwndJoin = w.control(clsButton, "加入", base, 0, 0, 10, 10)
	w.hwndRefr = w.control(clsButton, "刷新", base, 0, 0, 10, 10)

	w.hwndList = w.control(clsList, "",
		wsChild|wsVisible|wsBorder|wsVScroll|lbsNotify|lbsHasStrings|lbsNoIntegralHeight,
		0, 0, 10, 10)
	w.hwndInput = w.control(clsEdit, "",
		base|wsBorder|esMultiLine|esAutoVScroll|esWantReturn, 0, 0, 10, 10)
	w.hwndSend = w.control(clsButton, "发送", base, 0, 0, 10, 10)
	w.hwndPick = w.control(clsButton, "发送文件…", base, 0, 0, 10, 10)
	w.hwndSave = w.control(clsButton, "下载选中", base, 0, 0, 10, 10)
	w.hwndStatus = w.control(clsStatic, "", plain, 0, 0, 10, 10)
	// 不带 wsVisible：空闲时整条进度区隐藏，不占视觉空间
	w.hwndProgress = w.control(clsProgress, "", wsChild|pbsSmooth, 0, 0, 10, 10)
	w.hwndProgressText = w.control(clsStatic, "", plain, 0, 0, 10, 10)
	sendMsg(w.hwndProgress, pbmSetRange32, 0, progressMax)

	c := getConfig()
	if c.Name != "" {
		setText(w.hwndName, c.Name)
	}
}

func (w *appWindow) layoutFromClient() {
	var rc struct {
		left, top, right, bottom int32
	}
	_, _, _ = procGetClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&rc)))
	w.layout(int(rc.right-rc.left), int(rc.bottom-rc.top))
}

func (w *appWindow) layout(width, height int) {
	if w == nil || width < 100 || height < 100 {
		return
	}
	const pad = 10
	move(w.hwndTop, pad, 8, width-2*pad, 18)

	y := 32
	move(w.labels[0], pad, y+4, 44, 18)
	move(w.hwndName, pad+46, y, 124, 24)
	move(w.labels[1], pad+180, y+4, 44, 18)
	move(w.hwndCode, pad+226, y, 96, 24)
	move(w.hwndRefr, width-pad-60, y, 60, 24)
	move(w.hwndJoin, width-pad-126, y, 60, 24)

	listY := 64
	listH := height - listY - 108
	if listH < 60 {
		listH = 60
	}
	move(w.hwndList, pad, listY, width-2*pad, listH)
	move(w.hwndInput, pad, height-100, width-2*pad, 44)

	// 进度条独占一条，空闲时隐藏；百分比/字节数放右侧，不与状态栏抢位置
	const labelW = 168
	progressY := height - 50
	move(w.hwndProgress, pad, progressY, width-2*pad-labelW-8, 12)
	move(w.hwndProgressText, width-pad-labelW, progressY-3, labelW, 18)

	btnY := height - 34
	move(w.hwndSave, width-pad-88, btnY, 88, 26)
	move(w.hwndPick, width-pad-182, btnY, 88, 26)
	move(w.hwndSend, width-pad-256, btnY, 68, 26)
	statusW := width - 2*pad - 88 - 88 - 68 - 20
	move(w.hwndStatus, pad, btnY+5, statusW, 18)
}

// ---------- 窗口过程 ----------

func wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	w := theWindow
	switch uint32(msg) {
	case wmAppShow:
		_, _, _ = procShowWindow.Call(hwnd, swShow)
		_, _, _ = procSetForeground.Call(hwnd)
		go refreshState()
		return 0
	case wmAppQuit:
		_, _, _ = procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	case wmClose:
		_, _, _ = procShowWindow.Call(hwnd, swHide) // 关闭只是隐藏，托盘继续常驻
		return 0
	case wmAppReload:
		if w != nil {
			w.lastSig = ""
		}
		go refreshState()
		return 0
	case wmAppRefresh:
		go refreshState()
		return 0
	case wmAppStatus:
		statusMu.Lock()
		s := statusText
		statusMu.Unlock()
		if w != nil {
			setText(w.hwndStatus, s)
		}
		return 0
	case wmAppState:
		stateMu.Lock()
		st := pendingState
		pendingState = nil
		stateMu.Unlock()
		if w != nil && st != nil {
			w.renderState(st)
		}
		return 0
	case wmAppProgress:
		if w != nil {
			w.applyTransfer()
		}
		return 0
	case wmSize:
		if w != nil {
			w.layout(int(lparam&0xffff), int((lparam>>16)&0xffff))
		}
		return 0
	case wmCommand:
		if w != nil {
			w.onCommand(wparam, lparam)
		}
		return 0
	case wmDropFiles:
		if w != nil {
			paths := dropFiles(wparam)
			if len(paths) > 0 {
				w.uploadFiles(paths)
			}
		}
		return 0
	}
	ret, _, _ := procDefWindowProc.Call(hwnd, msg, wparam, lparam)
	return ret
}

func (w *appWindow) onCommand(wparam, lparam uintptr) {
	code := uint32(wparam >> 16)
	ctl := lparam
	if ctl == w.hwndList && code == lbnDblClk {
		w.downloadSelected()
		return
	}
	if code != bnClicked {
		return
	}
	switch ctl {
	case w.hwndSend:
		w.sendText()
	case w.hwndJoin:
		w.join()
	case w.hwndRefr:
		setStatus("正在刷新…")
		go refreshState()
	case w.hwndPick:
		w.pickAndUpload()
	case w.hwndSave:
		w.downloadSelected()
	}
}

// ---------- 状态同步 ----------

func refreshState() {
	if !refreshMu.TryLock() {
		return // 上一次请求还没回来，避免堆积
	}
	defer refreshMu.Unlock()

	c := getConfig()
	if c.Server == "" {
		setStatus("未配置服务器：请在托盘菜单里重新自动发现，或编辑配置文件")
		return
	}
	var st apiState
	err := api.get(c, "/api/state", &st)
	if err == errUnauthorized {
		var info apiInfo
		_ = api.get(c, "/api/info", &info)
		if info.Open {
			name := c.Name
			if name == "" {
				name = "Windows 电脑"
			}
			token, e := api.join(c, name, "")
			if e != nil {
				setStatus("自动加入失败：" + e.Error())
				return
			}
			mutateConfig(func(cfg *Config) {
				cfg.Token = token
				cfg.Name = name
			})
			setStatus("已自动加入")
			var st2 apiState
			if e2 := api.get(getConfig(), "/api/state", &st2); e2 == nil {
				postState(&st2)
			}
			return
		}
		setStatus("服务端需要加入码：请填好设备名与加入码，然后点「加入」")
		return
	}
	if err != nil {
		setStatus("连不上 NAS：" + err.Error())
		return
	}
	postState(&st)
}

func (w *appWindow) renderState(st *apiState) {
	msgs := st.Messages
	if len(msgs) > 200 {
		msgs = msgs[len(msgs)-200:]
	}
	name := "未加入"
	if st.Me != nil {
		name = st.Me.Name
	}
	c := getConfig()
	setText(w.hwndTop, "服务器："+c.Server+"    ·    在线    ·    设备："+name)

	sig := fmt.Sprintf("%d", len(msgs))
	if len(msgs) > 0 {
		sig += "/" + msgs[len(msgs)-1].ID
	}
	if sig == w.lastSig {
		return
	}

	selectedID := ""
	if cur := int(int32(sendMsg(w.hwndList, lbGetCurSel, 0, 0))); cur >= 0 && cur < len(w.messages) {
		selectedID = w.messages[cur].ID
	}

	_, _, _ = procSendMessage.Call(w.hwndList, lbResetContent, 0, 0)
	w.messages = make([]apiMessage, 0, len(msgs))
	for _, m := range msgs {
		line := w.formatMessage(m)
		_, _, _ = procSendMessage.Call(w.hwndList, lbAddString, 0,
			uintptr(unsafe.Pointer(utf16ptr(line))))
		w.messages = append(w.messages, m)
		if m.ID == selectedID {
			_, _, _ = procSendMessage.Call(w.hwndList, lbSetCurSel,
				uintptr(len(w.messages)-1), 0)
		}
	}
	if n := len(w.messages); n > 0 {
		_, _, _ = procSendMessage.Call(w.hwndList, lbSetTopIndex, uintptr(n-1), 0)
	}
	w.lastSig = sig
	setStatus(fmt.Sprintf("共 %d 条 · 每 3 秒刷新 · 文件可直接拖进窗口", len(w.messages)))
}

func (w *appWindow) formatMessage(m apiMessage) string {
	ts := time.UnixMilli(m.TS).Format("15:04")
	if m.Kind == "file" && m.File != nil {
		return fmt.Sprintf("[%s] %s: [文件] %s (%s)", ts, m.FromName,
			m.File.Name, fmtSize(m.File.Size))
	}
	text := strings.ReplaceAll(m.Text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " / ")
	return fmt.Sprintf("[%s] %s: %s", ts, m.FromName, text)
}

// ---------- 业务动作 ----------

func (w *appWindow) join() {
	c := getConfig()
	name := strings.TrimSpace(getText(w.hwndName))
	if name == "" {
		name = "Windows 电脑"
		setText(w.hwndName, name)
	}
	code := strings.TrimSpace(getText(w.hwndCode))
	setStatus("正在加入…")
	go func() {
		token, err := api.join(c, name, code)
		if err != nil {
			setStatus("加入失败：" + err.Error())
			return
		}
		mutateConfig(func(cfg *Config) {
			cfg.Token = token
			cfg.Name = name
		})
		setStatus("已加入，正在同步…")
		refreshState()
	}()
}

func (w *appWindow) sendText() {
	text := strings.TrimSpace(getText(w.hwndInput))
	if text == "" {
		setStatus("请输入要发送的内容")
		return
	}
	setText(w.hwndInput, "")
	c := getConfig()
	setStatus("正在发送…")
	go func() {
		if err := api.sendText(c, text); err != nil {
			setStatus("发送失败：" + err.Error())
			return
		}
		setStatus("已发送")
		refreshState()
	}()
}

func (w *appWindow) pickAndUpload() {
	w.uploadFiles(openFiles(w.hwnd))
}

func (w *appWindow) uploadFiles(paths []string) {
	if len(paths) == 0 {
		return
	}
	// 先给一个同步反馈：拖进去/选完文件立刻能看到，不用等网络请求回来
	setStatus(fmt.Sprintf("已收到 %d 个文件，正在检查…", len(paths)))
	c := getConfig()
	if c.Token == "" {
		setStatus("请先点「加入」再发送文件")
		messageBox("还没加入服务端：请先在窗口里点「加入」，再拖文件进来。")
		return
	}
	go func() {
		defer endTransfer()

		// 上传前问一次服务端上限，超了就别白传十几个 GB；旧版服务端没有这个字段时按不限制处理
		var limit int64
		var info apiInfo
		if err := api.get(c, "/api/info", &info); err == nil {
			limit = info.MaxBytes
		}

		queued := make([]string, 0, len(paths))
		notes := make([]string, 0)
		for _, p := range paths {
			fi, err := os.Stat(p)
			if err != nil {
				notes = append(notes, filepath.Base(p)+"：读不到文件（"+err.Error()+"）")
				continue
			}
			if limit > 0 && fi.Size() > limit {
				notes = append(notes, fmt.Sprintf("%s：%s，超过服务端单文件上限 %s",
					filepath.Base(p), fmtSize(fi.Size()), fmtSize(limit)))
				continue
			}
			queued = append(queued, p)
		}

		ok := 0
		for i, p := range queued {
			setStatus(fmt.Sprintf("正在上传 %d/%d：%s", i+1, len(queued), filepath.Base(p)))
			beginTransfer(fmt.Sprintf("上传 %d/%d", i+1, len(queued)))
			if err := api.upload(c, p, func(done, total int64) {
				reportTransfer(done, total)
			}); err != nil {
				notes = append(notes, filepath.Base(p)+"："+err.Error())
			} else {
				ok++
			}
		}

		setStatus(fmt.Sprintf("发送完成：成功 %d，未发送 %d", ok, len(notes)))
		if len(notes) > 0 {
			messageBox("以下文件没有发送成功：\n\n" + strings.Join(notes, "\n") +
				"\n\n单文件上限由服务端的 -max-mb / LANLINK_MAX_MB 决定；" +
				"飞牛应用可在 /var/apps/lanlink/etc/settings.conf 里加 max_mb=xxxxx（单位 MB）后重启。")
		}
		refreshState()
	}()
}

func (w *appWindow) downloadSelected() {
	sel := int(int32(sendMsg(w.hwndList, lbGetCurSel, 0, 0)))
	if sel < 0 || sel >= len(w.messages) {
		setStatus("请先在列表里选中一条文件消息（双击也可下载）")
		return
	}
	m := w.messages[sel]
	if m.Kind != "file" || m.File == nil {
		setStatus("选中的是文本消息，没有文件可下载")
		return
	}
	dest := saveFile(w.hwnd, m.File.Name)
	if dest == "" {
		return
	}
	c := getConfig()
	setStatus("正在下载：" + m.File.Name)
	go func() {
		beginTransfer("下载")
		defer endTransfer()
		err := api.download(c, m, dest, func(done, total int64) {
			reportTransfer(done, total)
		})
		if err != nil {
			setStatus("下载失败：" + err.Error())
			return
		}
		setStatus("已保存到 " + dest)
	}()
}

// ---------- 系统文件对话框 ----------

func openFiles(owner uintptr) []string {
	buf := make([]uint16, 32768)
	ofn := openFileName{
		lStructSize: uint32(unsafe.Sizeof(openFileName{})),
		hwndOwner:   owner,
		lpstrFilter: utf16ptr("所有文件\x00*.*\x00\x00"),
		lpstrFile:   &buf[0],
		nMaxFile:    uint32(len(buf)),
		lpstrTitle:  utf16ptr("选择要发送的文件（可按 Ctrl / Shift 多选）"),
		flags: ofnAllowMultiSelect | ofnExplorer | ofnFileMustExist |
			ofnPathMustExist | ofnHideReadOnly | ofnNoChangeDir,
	}
	ret, _, _ := procGetOpenFileName.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret == 0 {
		return nil
	}

	var parts []string
	start := 0
	for i, v := range buf {
		if v != 0 {
			continue
		}
		if i == start {
			break
		}
		parts = append(parts, syscall.UTF16ToString(buf[start:i]))
		start = i + 1
		if len(parts) > 4096 {
			break
		}
	}
	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 {
		return []string{parts[0]}
	}
	dir := parts[0]
	out := make([]string, 0, len(parts)-1)
	for _, name := range parts[1:] {
		out = append(out, filepath.Join(dir, name))
	}
	return out
}

func saveFile(owner uintptr, suggested string) string {
	buf := make([]uint16, 1024)
	name, _ := syscall.UTF16FromString(suggested)
	if len(name) > len(buf) {
		name = name[:len(buf)-1]
	}
	copy(buf, name)
	ofn := openFileName{
		lStructSize: uint32(unsafe.Sizeof(openFileName{})),
		hwndOwner:   owner,
		lpstrFilter: utf16ptr("所有文件\x00*.*\x00\x00"),
		lpstrFile:   &buf[0],
		nMaxFile:    uint32(len(buf)),
		lpstrTitle:  utf16ptr("保存文件到…"),
		flags:       ofnExplorer | ofnOverwritePrompt | ofnPathMustExist | ofnHideReadOnly | ofnNoChangeDir,
	}
	ret, _, _ := procGetSaveFileName.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func dropFiles(hdrop uintptr) []string {
	n, _, _ := procDragQueryFile.Call(hdrop, 0xffffffff, 0, 0)
	count := int(uint32(n))
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		buf := make([]uint16, 1024)
		got, _, _ := procDragQueryFile.Call(hdrop, uintptr(i),
			uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if got > 0 {
			out = append(out, syscall.UTF16ToString(buf))
		}
	}
	_, _, _ = procDragFinish.Call(hdrop)
	return out
}

// ---------- Win32 小工具 ----------

func utf16ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func sendMsg(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	ret, _, _ := procSendMessage.Call(hwnd, uintptr(msg), wp, lp)
	return ret
}

func setText(hwnd uintptr, s string) {
	if hwnd == 0 {
		return
	}
	_, _, _ = procSetWindowText.Call(hwnd, uintptr(unsafe.Pointer(utf16ptr(s))))
}

func getText(hwnd uintptr) string {
	if hwnd == 0 {
		return ""
	}
	n, _, _ := procGetTextLength.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, int(n)+1)
	_, _, _ = procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func move(hwnd uintptr, x, y, cx, cy int) {
	if hwnd == 0 || cx <= 0 || cy <= 0 || x < 0 || y < 0 {
		return
	}
	_, _, _ = procMoveWindow.Call(hwnd, uintptr(x), uintptr(y), uintptr(cx), uintptr(cy), 1)
}
