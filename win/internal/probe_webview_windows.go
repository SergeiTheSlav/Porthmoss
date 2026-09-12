//go:build windows

package probe

import "github.com/jchv/go-webview2"

func New() webview2.WebView { return webview2.NewWithOptions(webview2.WebViewOptions{}) }
