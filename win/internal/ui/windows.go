//go:build windows

package ui

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/energye/systray"
	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

//go:embed assets/index.html
var indexHTML string

//go:embed assets/tray.ico
var trayIcon []byte

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	dwmapi = windows.NewLazySystemDLL("dwmapi.dll")

	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procDwmSetWindowAttrib  = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	swHide = 0
	swShow = 5

	// Windows 11 window trimmings. Both are no-ops on Windows 10, which is
	// why they are fire-and-forget rather than checked.
	dwmUseImmersiveDarkMode = 20
	dwmWindowCornerPref     = 33
	cornerPreferenceRound   = 2
)

// WindowsUI is a notification-area icon plus a WebView2 window.
//
// Threading is the delicate part. systray locks the main OS thread in its own
// init and owns the main message loop, so the WebView gets a dedicated locked
// thread with a message loop of its own. That is ordinary Win32 — windows
// belong to the thread that created them — but it does mean every call into
// the view has to go through Dispatch.
type WindowsUI struct {
	opts Options

	mu      sync.Mutex
	view    webview2.WebView
	hwnd    uintptr
	last    State
	loaded  bool
	stopped bool

	ready chan struct{}
}

// New returns the tray-and-window front end.
func New(opts Options) UI { return NewWindows(opts) }

func NewWindows(opts Options) *WindowsUI {
	return &WindowsUI{opts: opts, ready: make(chan struct{})}
}

func (w *WindowsUI) Run() error {
	go w.runWebView()

	systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTitle("Porthmoss")
		systray.SetTooltip("Porthmoss — waiting for your Mac")

		open := systray.AddMenuItem("Open Porthmoss", "Show the Porthmoss window")
		open.Click(w.Show)
		systray.AddSeparator()
		forget := systray.AddMenuItem("Forget paired Mac", "Require pairing again next time")
		forget.Click(func() {
			if w.opts.OnUnpair != nil {
				w.opts.OnUnpair()
			}
		})
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit Porthmoss", "Stop accepting control from your Mac")
		quit.Click(func() { systray.Quit() })

		// Clicking the icon itself is the obvious way to get the window back.
		systray.SetOnClick(func(systray.IMenu) { w.Show() })
	}, func() {
		w.teardown()
		if w.opts.OnQuit != nil {
			w.opts.OnQuit()
		}
	})
	return nil
}

func (w *WindowsUI) runWebView() {
	// The window and its message loop live and die on this one thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	view := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "Porthmoss",
			Width:  460,
			Height: 620,
			Center: true,
		},
	})
	if view == nil {
		// No WebView2 runtime. The agent still works; it just has no window,
		// and main() has already told the user what to install.
		close(w.ready)
		return
	}
	defer view.Destroy()

	hwnd := uintptr(view.Window())
	applyWindowChrome(hwnd)

	// The page calls these; they are the entire UI-to-agent surface.
	_ = view.Bind("unpair", func() {
		if w.opts.OnUnpair != nil {
			w.opts.OnUnpair()
		}
	})
	_ = view.Bind("hideWindow", func() { w.Hide() })
	_ = view.Bind("ready", func() {
		// The page is live: replay whatever state arrived before it loaded.
		w.mu.Lock()
		w.loaded = true
		state := w.last
		w.mu.Unlock()
		w.push(state)
	})

	view.SetHtml(indexHTML)

	w.mu.Lock()
	w.view = view
	w.hwnd = hwnd
	w.mu.Unlock()
	close(w.ready)

	view.Run()
}

// applyWindowChrome asks for the Windows 11 look. Both attributes are silently
// ignored on Windows 10, so failures are not worth reporting.
func applyWindowChrome(hwnd uintptr) {
	dark := int32(1)
	procDwmSetWindowAttrib.Call(hwnd, dwmUseImmersiveDarkMode,
		uintptr(unsafe.Pointer(&dark)), unsafe.Sizeof(dark))

	corner := int32(cornerPreferenceRound)
	procDwmSetWindowAttrib.Call(hwnd, dwmWindowCornerPref,
		uintptr(unsafe.Pointer(&corner)), unsafe.Sizeof(corner))
}

func (w *WindowsUI) Show() {
	<-w.ready
	w.mu.Lock()
	hwnd, view := w.hwnd, w.view
	w.mu.Unlock()
	if view == nil || hwnd == 0 {
		return
	}
	view.Dispatch(func() {
		procShowWindow.Call(hwnd, swShow)
		procSetForegroundWindow.Call(hwnd)
	})
}

func (w *WindowsUI) Hide() {
	w.mu.Lock()
	hwnd, view := w.hwnd, w.view
	w.mu.Unlock()
	if view == nil || hwnd == 0 {
		return
	}
	view.Dispatch(func() { procShowWindow.Call(hwnd, swHide) })
}

func (w *WindowsUI) Update(state State) {
	w.mu.Lock()
	w.last = state
	loaded := w.loaded
	w.mu.Unlock()

	systray.SetTooltip(tooltip(state))
	if loaded {
		w.push(state)
	}
	// An unpaired Mac is waiting on a code nobody can see if the window is
	// hidden, so this is the one state that opens it unprompted.
	if state.Code != "" {
		go w.Show()
	}
}

func (w *WindowsUI) push(state State) {
	w.mu.Lock()
	view := w.view
	w.mu.Unlock()
	if view == nil {
		return
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return
	}
	view.Dispatch(func() { view.Eval("window.applyState(" + string(encoded) + ")") })
}

func tooltip(state State) string {
	if state.Detail == "" {
		return "Porthmoss — " + state.Title
	}
	return fmt.Sprintf("Porthmoss — %s", state.Title)
}

func (w *WindowsUI) Stop() { systray.Quit() }

func (w *WindowsUI) teardown() {
	w.mu.Lock()
	view := w.view
	w.stopped = true
	w.mu.Unlock()
	if view != nil {
		view.Terminate()
	}
}
