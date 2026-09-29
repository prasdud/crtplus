package browser

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type family int

const (
	familyUnknown family = iota
	familyChromium
	familyFirefox
)

// OpenWindow opens urls as tabs in a single new browser window. It returns true
// when a new window was requested, or false when it had to fall back to the
// default opener (which opens tabs in the existing window).
func OpenWindow(urls []string) (bool, error) {
	if len(urls) == 0 {
		return false, nil
	}
	if name, args := windowCommand(urls); name != "" {
		if err := exec.Command(name, args...).Start(); err != nil {
			return false, err
		}
		return true, nil
	}

	var firstErr error
	for _, u := range urls {
		if err := Open(u); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false, firstErr
}

// Open hands url to the default system browser without blocking on the
// browser process.
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

func windowCommand(urls []string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return darwinWindow(urls)
	case "windows":
		return windowsWindow(urls)
	default:
		return linuxWindow(urls)
	}
}

func linuxWindow(urls []string) (string, []string) {
	execPath, fam := defaultBrowser()
	if execPath != "" && fam != familyUnknown {
		return execPath, newWindowArgs(fam, urls)
	}

	for _, b := range []struct {
		name string
		fam  family
	}{
		{"brave-browser", familyChromium},
		{"google-chrome", familyChromium},
		{"chromium", familyChromium},
		{"microsoft-edge", familyChromium},
		{"vivaldi", familyChromium},
		{"firefox", familyFirefox},
	} {
		if path, err := exec.LookPath(b.name); err == nil {
			return path, newWindowArgs(b.fam, urls)
		}
	}
	return "", nil
}

func darwinWindow(urls []string) (string, []string) {
	for _, app := range []string{"Google Chrome", "Brave Browser", "Firefox", "Microsoft Edge", "Chromium"} {
		if _, err := os.Stat("/Applications/" + app + ".app"); err == nil {
			args := append([]string{"-n", "-a", app, "--args", "--new-window"}, urls...)
			return "open", args
		}
	}
	return "", nil
}

func windowsWindow(urls []string) (string, []string) {
	for _, b := range []struct {
		name string
		fam  family
	}{
		{"chrome.exe", familyChromium},
		{"brave.exe", familyChromium},
		{"msedge.exe", familyChromium},
		{"firefox.exe", familyFirefox},
	} {
		if path, err := exec.LookPath(b.name); err == nil {
			return path, newWindowArgs(b.fam, urls)
		}
	}
	return "", nil
}

func newWindowArgs(fam family, urls []string) []string {
	return append([]string{"--new-window"}, urls...)
}

func defaultBrowser() (string, family) {
	if env := os.Getenv("BROWSER"); env != "" {
		if path, err := exec.LookPath(env); err == nil {
			return path, familyOf(env)
		}
	}
	for _, id := range desktopIDs() {
		if path := execFromDesktop(id); path != "" {
			return path, familyOf(path)
		}
	}
	return "", familyUnknown
}

func desktopIDs() []string {
	var ids []string
	for _, cmd := range [][]string{
		{"xdg-settings", "get", "default-web-browser"},
		{"xdg-mime", "query", "default", "x-scheme-handler/https"},
		{"xdg-mime", "query", "default", "x-scheme-handler/http"},
	} {
		out, err := exec.Command(cmd[0], cmd[1:]...).Output()
		if err != nil {
			continue
		}
		if id := strings.TrimSpace(string(out)); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func execFromDesktop(id string) string {
	if !strings.HasSuffix(id, ".desktop") {
		id += ".desktop"
	}
	dirs := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local/share/applications"))
	}
	dirs = append(dirs, "/usr/local/share/applications", "/usr/share/applications")

	for _, dir := range dirs {
		if path := execFromDesktopFile(filepath.Join(dir, id)); path != "" {
			return path
		}
	}
	return ""
}

func execFromDesktopFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "Exec=") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "Exec="))
		if len(fields) == 0 {
			continue
		}
		bin := fields[0]
		if strings.HasPrefix(bin, "/") {
			if _, err := os.Stat(bin); err == nil {
				return bin
			}
			continue
		}
		if resolved, err := exec.LookPath(bin); err == nil {
			return resolved
		}
	}
	return ""
}

func familyOf(name string) family {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "firefox"):
		return familyFirefox
	case strings.Contains(n, "chrome"),
		strings.Contains(n, "chromium"),
		strings.Contains(n, "brave"),
		strings.Contains(n, "edge"),
		strings.Contains(n, "vivaldi"),
		strings.Contains(n, "opera"):
		return familyChromium
	}
	return familyUnknown
}
