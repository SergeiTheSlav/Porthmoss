//go:build windows

package ui

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"syscall"
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
	procSetWindowLongPtr    = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProc      = user32.NewProc("CallWindowProcW")
	procDefWindowProc       = user32.NewProc("DefWindowProcW")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procDwmSetWindowAttrib  = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	swHide = 0
	swShow = 5

	wmClose = 0x0010
	// GWLP_WNDPROC, which is -4. Only the 64-bit form is needed: this agent
	// ships for amd64 and arm64 and nothing else.
	gwlpWndProc = ^uintptr(3)

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

	mu          sync.Mutex
	view        webview2.WebView
	hwnd        uintptr
	last        State
	loaded      bool
	stopped     bool
	lastTooltip string

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
			Height: 640,
			Center: true,
			// The RT_GROUP_ICON that rsrc embeds from the same drawing the Mac
			// app uses. Without it the window shows the generic Windows icon.
			IconId: 1,
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
	hideOnClose(hwnd)

	// The page calls these; they are the entire UI-to-agent surface.
	_ = view.Bind("unpair", func() {
		if w.opts.OnUnpair != nil {
			w.opts.OnUnpair()
		}
	})
	_ = view.Bind("hideWindow", func() { w.Hide() })
	_ = view.Bind("openDropFolder", func() {
		if w.opts.OnOpenDropFolder != nil {
			w.opts.OnOpenDropFolder()
		}
	})
	_ = view.Bind("ready", func() {
		// The page is live: replay whatever state arrived before it loaded.
		w.mu.Lock()
		w.loaded = true
		state := w.last
		w.mu.Unlock()
		w.push(state)
	})

	view.SetHtml(indexHTML)

	if w.opts.StartHidden {
		procShowWindow.Call(hwnd, swHide)
	}

	w.mu.Lock()
	w.view = view
	w.hwnd = hwnd
	w.mu.Unlock()
	close(w.ready)

	view.Run()

	// Run returns once the window is destroyed, and the deferred Destroy above
	// then releases the WebView2 objects. Anything still holding this pointer
	// would be calling into freed COM the next time the state changed — a
	// session connecting, a file arriving — so it must not outlive the loop.
	w.mu.Lock()
	w.view = nil
	w.hwnd = 0
	w.mu.Unlock()
}

// hideOnClose makes the window's close button hide it rather than destroy it.
//
// This is a tray application: closing the window should put it away, not tear
// down the interface for the rest of the session. Destroying it also ended the
// message loop, after which every state update dispatched into released COM
// objects and took the process with it — which is why the agent kept dying
// shortly after the window was "minimised".
func hideOnClose(hwnd uintptr) {
	var previous uintptr
	proc := syscall.NewCallback(func(hwnd, msg, wParam, lParam uintptr) uintptr {
		if msg == wmClose {
			procShowWindow.Call(hwnd, swHide)
			return 0
		}
		if previous == 0 {
			// A message arriving before SetWindowLongPtrW has returned. Rare,
			// but the alternative is calling through a null pointer.
			ret, _, _ := procDefWindowProc.Call(hwnd, msg, wParam, lParam)
			return ret
		}
		ret, _, _ := procCallWindowProc.Call(previous, hwnd, msg, wParam, lParam)
		return ret
	})
	previous, _, _ = procSetWindowLongPtr.Call(hwnd, gwlpWndProc, proc)
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
	hwnd, view, stopped := w.hwnd, w.view, w.stopped
	w.mu.Unlock()
	if view == nil || hwnd == 0 || stopped {
		return
	}
	view.Dispatch(func() {
		procShowWindow.Call(hwnd, swShow)
		procSetForegroundWindow.Call(hwnd)
	})
}

func (w *WindowsUI) Hide() {
	w.mu.Lock()
	hwnd, view, stopped := w.hwnd, w.view, w.stopped
	w.mu.Unlock()
	if view == nil || hwnd == 0 || stopped {
		return
	}
	view.Dispatch(func() { procShowWindow.Call(hwnd, swHide) })
}

func (w *WindowsUI) Update(state State) {
	w.mu.Lock()
	previous := w.last
	w.last = state
	loaded := w.loaded
	tip := tooltip(state)
	changedTip := tip != w.lastTooltip
	if changedTip {
		w.lastTooltip = tip
	}
	w.mu.Unlock()

	// Only when the text actually changes. Update runs on whichever goroutine
	// happened to change the state — the server's, a file arriving — and every
	// call here is a cross-thread Shell_NotifyIcon. Doing it on every state
	// change meant doing it for reasons that never altered a single character.
	if changedTip {
		systray.SetTooltip(tip)
	}
	if loaded {
		w.push(state)
	}
	switch {
	case state.Code != "" && state.Code != previous.Code:
		// An unpaired Mac is waiting on a code nobody can see if the window is
		// hidden, so this is the one state that opens it unprompted. Only when
		// the code is new: repeating it would pile up goroutines all fighting
		// to front the same window.
		go w.Show()
	case state.Controlled && !previous.Controlled:
		// SendInput delivers keystrokes to whatever window is in front. If
		// that is this one, every key the Mac sends lands in a status page
		// with nowhere to put it — the mouse still works, because it is
		// positioned absolutely, and the keyboard silently does nothing.
		go w.Hide()
	}
}

func (w *WindowsUI) push(state State) {
	w.mu.Lock()
	view, stopped := w.view, w.stopped
	w.mu.Unlock()
	if view == nil || stopped {
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
