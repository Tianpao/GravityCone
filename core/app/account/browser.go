package account

import (
	"os/exec"
	"runtime"
)

// OpenBrowser opens a URL in the system default browser.
// 供 microsoft/naids 两个 OAuth 服务共用。
func OpenBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
