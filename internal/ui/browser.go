package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/shiv/internal/logger"
)

const prefKeyDefaultBrowser = "default_browser"

// DetectedBrowser holds a browser's display name and executable path.
type DetectedBrowser struct {
	Name string
	Path string
}

// isFirefox returns true if the executable looks like Firefox.
func isFirefox(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.Contains(base, "firefox")
}

// DetectBrowsers scans the system for supported browsers and returns all found.
func DetectBrowsers() []DetectedBrowser {
	switch runtime.GOOS {
	case "linux":
		return detectBrowsersLinux()
	case "darwin":
		return detectBrowsersDarwin()
	case "windows":
		return detectBrowsersWindows()
	}
	return nil
}

func detectBrowsersLinux() []DetectedBrowser {
	candidates := []struct{ name, bin string }{
		{"Google Chrome", "google-chrome"},
		{"Google Chrome (stable)", "google-chrome-stable"},
		{"Chromium", "chromium"},
		{"Chromium (browser)", "chromium-browser"},
		{"Brave (Browser)", "brave-browser"},
		{"Brave", "brave"},
		{"Microsoft Edge", "microsoft-edge"},
		{"Vivaldi", "vivaldi"},
		{"Vivaldi (stable)", "vivaldi-Stable"},
		{"Opera", "opera"},
		{"Opera (stable)", "opera-stable"},
		{"Opera (browser)", "opera-browser"},
		{"Firefox", "firefox"},
		{"Firefox ESR", "firefox-esr"},
	}
	var found []DetectedBrowser
	seen := map[string]bool{}
	for _, c := range candidates {
		path, err := exec.LookPath(c.bin)
		if err != nil {
			continue
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		found = append(found, DetectedBrowser{Name: c.name, Path: path})
	}
	return found
}

func detectBrowsersDarwin() []DetectedBrowser {
	candidates := []struct{ name, app string }{
		{"Google Chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
		{"Chromium", "/Applications/Chromium.app/Contents/MacOS/Chromium"},
		{"Brave", "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"},
		{"Microsoft Edge", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
		{"Vivaldi", "/Applications/Vivaldi.app/Contents/MacOS/Vivaldi"},
		{"Opera", "/Applications/Opera.app/Contents/MacOS/Opera"},
		{"Firefox", "/Applications/Firefox.app/Contents/MacOS/firefox"},
	}
	var found []DetectedBrowser
	for _, c := range candidates {
		if _, err := os.Stat(c.app); err == nil {
			found = append(found, DetectedBrowser{Name: c.name, Path: c.app})
		}
	}
	return found
}

func detectBrowsersWindows() []DetectedBrowser {
	candidates := []struct{ name, path string }{
		{"Google Chrome", `C:\Program Files\Google\Chrome\Application\chrome.exe`},
		{"Google Chrome (x86)", `C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`},
		{"Chromium", `C:\Program Files\Chromium\Application\chrome.exe`},
		{"Brave", `C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`},
		{"Microsoft Edge", `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`},
		{"Vivaldi", `C:\Program Files\Vivaldi\Application\vivaldi.exe`},
		{"Opera", `C:\Program Files\Opera\opera.exe`},
		{"Firefox", `C:\Program Files\Mozilla Firefox\firefox.exe`},
		{"Firefox (x86)", `C:\Program Files (x86)\Mozilla Firefox\firefox.exe`},
	}
	var found []DetectedBrowser
	seen := map[string]bool{}
	for _, c := range candidates {
		if _, err := os.Stat(c.path); err == nil {
			if !seen[c.path] {
				seen[c.path] = true
				found = append(found, DetectedBrowser{Name: c.name, Path: c.path})
			}
		}
	}
	return found
}

// profileDir returns the persistent browser profile directory for the given browser executable.
func profileDir(fyneApp fyne.App, browserPath string) string {
	root := fyneApp.Storage().RootURI().Path()
	if isFirefox(browserPath) {
		return filepath.Join(root, "browser-profile-firefox")
	}
	return filepath.Join(root, "browser-profile-chrome")
}

// setupFirefoxProfile creates a Firefox profile dir with proxy settings written into user.js.
func setupFirefoxProfile(profilePath, proxyHost string, proxyPort int) error {
	if err := os.MkdirAll(profilePath, 0700); err != nil {
		return fmt.Errorf("create firefox profile dir: %w", err)
	}
	userJS := fmt.Sprintf(`// Shiv proxy profile — auto-generated, do not edit manually.
user_pref("network.proxy.type", 1);
user_pref("network.proxy.http", "%s");
user_pref("network.proxy.http_port", %d);
user_pref("network.proxy.ssl", "%s");
user_pref("network.proxy.ssl_port", %d);
user_pref("network.proxy.no_proxies_on", "");
user_pref("browser.startup.homepage_override.mstone", "ignore");
user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("extensions.enabledScopes", 0);
user_pref("extensions.autoDisableScopes", 15);
user_pref("network.stricttransportsecurity.preloadlist", false);
user_pref("security.enterprise_roots.enabled", true);
`, proxyHost, proxyPort, proxyHost, proxyPort)
	return os.WriteFile(filepath.Join(profilePath, "user.js"), []byte(userJS), 0600)
}

// LaunchBrowser launches the given browser pointed at the proxy with an isolated profile.
func LaunchBrowser(fyneApp fyne.App, browser DetectedBrowser, proxyHost string, proxyPort int) error {
	profile := profileDir(fyneApp, browser.Path)
	proxyAddr := fmt.Sprintf("%s:%d", proxyHost, proxyPort)

	var args []string

	if isFirefox(browser.Path) {
		if err := setupFirefoxProfile(profile, proxyHost, proxyPort); err != nil {
			return err
		}
		args = []string{
			"-profile", profile,
			"-no-remote",
		}
	} else {
		// Chromium-based — profile dir is created automatically by the browser.
		args = []string{
			"--proxy-server=http://" + proxyAddr,
			"--user-data-dir=" + profile,
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-extensions",
			"--disable-sync",
			"--disable-background-networking",
			"--disable-default-apps",
			"--disable-component-update",
			"--disable-breakpad",
			"--metrics-recording-only",
			"--disable-features=OptimizationHints,MediaRouter",
		}
	}

	cmd := exec.Command(browser.Path, args...)
	cmd.Stderr = nil
	cmd.Stdout = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch browser: %w", err)
	}

	logger.Info("browser: launched %s (pid %d) via %s", browser.Name, cmd.Process.Pid, proxyAddr)

	// Detach — we don't want to wait for the browser to exit.
	go func() {
		err := cmd.Wait()
		if err != nil {
			logger.Error("browser exited early: %v", err)
		}
	}()
	return nil
}

// ClearBrowserProfile deletes the stored profile for the given browser executable.
func ClearBrowserProfile(fyneApp fyne.App, browserPath string) error {
	profile := profileDir(fyneApp, browserPath)
	if err := os.RemoveAll(profile); err != nil {
		return fmt.Errorf("clear browser profile: %w", err)
	}
	return nil
}

// launchDefaultBrowser reads the saved default browser from prefs and launches it.
// If no default is set it shows a picker dialog first.
func launchDefaultBrowser(fyneApp fyne.App, win fyne.Window) {
	prefs := fyneApp.Preferences()
	proxyHost := prefs.StringWithFallback(prefKeyProxyHost, defaultProxyHost)
	proxyPort := prefs.IntWithFallback(prefKeyProxyPort, defaultProxyPort)

	savedPath := prefs.String(prefKeyDefaultBrowser)
	if savedPath != "" {
		// Verify it still exists.
		if _, err := os.Stat(savedPath); err == nil {
			browser := DetectedBrowser{Name: filepath.Base(savedPath), Path: savedPath}
			if err := LaunchBrowser(fyneApp, browser, proxyHost, proxyPort); err != nil {
				dialog.ShowError(err, win)
			}
			return
		}
		// Saved browser no longer exists — fall through to picker.
		prefs.SetString(prefKeyDefaultBrowser, "")
	}

	browsers := DetectBrowsers()
	if len(browsers) == 0 {
		dialog.ShowInformation("No Browser Found",
			"No supported browser was detected on your system.\nInstall Chrome, Brave, Firefox, or another Chromium-based browser.",
			win)
		return
	}

	// If only one browser found, use it directly and save as default.
	if len(browsers) == 1 {
		prefs.SetString(prefKeyDefaultBrowser, browsers[0].Path)
		if err := LaunchBrowser(fyneApp, browsers[0], proxyHost, proxyPort); err != nil {
			dialog.ShowError(err, win)
		}
		return
	}

	// Multiple browsers — show picker.
	showBrowserPickerDialog(fyneApp, win, browsers, proxyHost, proxyPort)
}

// showBrowserPickerDialog shows a one-time dialog to pick a default browser.
func showBrowserPickerDialog(fyneApp fyne.App, win fyne.Window, browsers []DetectedBrowser, proxyHost string, proxyPort int) {
	names := make([]string, len(browsers))
	for i, b := range browsers {
		names[i] = b.Name
	}

	selected := browsers[0]
	picker := newBrowserPickerContent(names, func(name string) {
		for _, b := range browsers {
			if b.Name == name {
				selected = b
				break
			}
		}
	})

	d := dialog.NewCustomConfirm("Select Default Browser", "Launch", "Cancel", picker, func(confirmed bool) {
		if !confirmed {
			return
		}
		fyneApp.Preferences().SetString(prefKeyDefaultBrowser, selected.Path)
		if err := LaunchBrowser(fyneApp, selected, proxyHost, proxyPort); err != nil {
			dialog.ShowError(err, win)
		}
	}, win)
	d.Show()
}
