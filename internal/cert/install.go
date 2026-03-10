package cert

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// InstallCA attempts to install the Shiv CA certificate into the OS trust store.
// Returns a human-readable status string describing what was done or what the
// user needs to do manually.
func (ca *CA) InstallCA() (string, error) {
	certPath, err := CertPEMPath()
	if err != nil {
		return "", err
	}

	switch runtime.GOOS {
	case "linux":
		return installLinux(certPath)
	case "darwin":
		return installDarwin(certPath)
	case "windows":
		return installWindows(certPath)
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// installLinux tries system-wide installation first, falls back to Firefox NSS.
func installLinux(certPath string) (string, error) {
	// Step 1: standard system trust store (Debian/Ubuntu/Arch etc.)
	if err := installLinuxSystem(certPath); err == nil {
		return "CA certificate installed to system trust store.", nil
	}

	// Step 2: Firefox NSS via certutil
	if err := installLinuxNSS(certPath); err == nil {
		return "CA certificate installed to Firefox trust store via certutil.", nil
	}

	// Step 3: nothing worked, tell the user what to do
	return "", fmt.Errorf(
		"automatic installation failed.\n\n"+
			"Manual options:\n"+
			"  System-wide (Debian/Ubuntu):  copy %s to /usr/local/share/ca-certificates/shiv-ca.crt and run: sudo update-ca-certificates\n"+
			"  System-wide (Arch):           copy %s to /etc/ca-certificates/trust-source/anchors/shiv-ca.crt and run: sudo trust extract-compat\n"+
			"  Firefox only:                 import via Firefox Preferences → Privacy & Security → Certificates → Import\n"+
			"  NixOS:                        add to configuration.nix: security.pki.certificateFiles = [ \"%s\" ]",
		certPath, certPath, certPath,
	)
}

func installLinuxSystem(certPath string) error {
	// Try Debian/Ubuntu path first.
	destDir := "/usr/local/share/ca-certificates"
	updateCmd := "update-ca-certificates"

	if _, err := exec.LookPath(updateCmd); err != nil {
		// Try Arch/Fedora path.
		destDir = "/etc/ca-certificates/trust-source/anchors"
		updateCmd = "trust"
		if _, err := exec.LookPath(updateCmd); err != nil {
			return fmt.Errorf("neither update-ca-certificates nor trust found")
		}
	}

	dest := filepath.Join(destDir, "shiv-ca.crt")
	if err := copyFile(certPath, dest); err != nil {
		return fmt.Errorf("copy to %s: %w", dest, err)
	}

	var cmd *exec.Cmd
	if updateCmd == "trust" {
		cmd = exec.Command("sudo", "trust", "extract-compat")
	} else {
		cmd = exec.Command("sudo", updateCmd)
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("update command failed: %w\n%s", err, out)
	}

	return nil
}

func installLinuxNSS(certPath string) error {
	if _, err := exec.LookPath("certutil"); err != nil {
		return fmt.Errorf("certutil not found")
	}

	// Find Firefox profile directories.
	profiles, err := firefoxProfiles()
	if err != nil || len(profiles) == 0 {
		return fmt.Errorf("no Firefox profiles found")
	}

	var lastErr error
	for _, profile := range profiles {
		cmd := exec.Command("certutil", "-A", "-n", "Shiv CA", "-t", "CT,,", "-i", certPath, "-d", profile)
		if out, err := cmd.CombinedOutput(); err != nil {
			lastErr = fmt.Errorf("certutil failed for %s: %w\n%s", profile, err, out)
			continue
		}
	}

	return lastErr
}

func firefoxProfiles() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	// Standard Firefox profile location on Linux.
	profilesDir := filepath.Join(home, ".mozilla", "firefox")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		return nil, err
	}

	var profiles []string
	for _, e := range entries {
		if e.IsDir() {
			profiles = append(profiles, filepath.Join(profilesDir, e.Name()))
		}
	}
	return profiles, nil
}

func installDarwin(certPath string) (string, error) {
	cmd := exec.Command("sudo", "security", "add-trusted-cert",
		"-d", "-r", "trustRoot",
		"-k", "/Library/Keychains/System.keychain",
		certPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("security add-trusted-cert failed: %w\n%s", err, out)
	}
	return "CA certificate installed to macOS system keychain.", nil
}

func installWindows(certPath string) (string, error) {
	cmd := exec.Command("certutil", "-addstore", "-f", "ROOT", certPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("certutil failed: %w\n%s", err, out)
	}
	return "CA certificate installed to Windows root store.", nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
