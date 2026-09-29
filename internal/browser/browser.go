package browser

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Open hands url to the default system browser without blocking on the
// browser process. Repeated calls normally open new tabs in the current window.
func Open(url string) error {
	name, args := opener(url)
	if name == "" {
		return fmt.Errorf("no browser opener found for %s", runtime.GOOS)
	}
	return exec.Command(name, args...).Start()
}

func opener(url string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		for _, name := range []string{"xdg-open", "sensible-browser", "x-www-browser"} {
			if path, err := exec.LookPath(name); err == nil {
				return path, []string{url}
			}
		}
		return "", nil
	}
}
