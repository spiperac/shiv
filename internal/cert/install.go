package cert

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// InstallCA attempts to install the Shiv CA into the user-level trust store.
// Never requires sudo. Returns a human-readable status string, or an error
// with manual instructions if automatic installation is not possible.
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
		return "", manualInstructions(certPath, fmt.Sprintf("unsupported OS: %s", runtime.GOOS))
	}
}

func installLinux(certPath string) (string, error) {
	if _, err := exec.LookPath("certutil"); err != nil {
		return "", manualInstructions(certPath, "certutil not found (install libnss3-tools on Debian/Ubuntu or nss on Arch)")
	}

	var installed []string
	var errs []string

	// Chrome/Chromium user NSS database.
	home, err := os.UserHomeDir()
	if err == nil {
		nssdb := filepath.Join(home, ".pki", "nssdb")
		if err := os.MkdirAll(nssdb, 0700); err == nil {
			cmd := exec.Command("certutil", "-A", "-n", "Shiv CA", "-t", "CT,,", "-i", certPath, "-d", "sql:"+nssdb)
			if out, err := cmd.CombinedOutput(); err != nil {
				errs = append(errs, fmt.Sprintf("Chrome NSS: %v\n%s", err, out))
			} else {
				installed = append(installed, "Chrome/Chromium")
			}
		}
	}

	// Firefox profiles.
	profiles, _ := firefoxProfiles()
	for _, profile := range profiles {
		cmd := exec.Command("certutil", "-A", "-n", "Shiv CA", "-t", "CT,,", "-i", certPath, "-d", profile)
		if out, err := cmd.CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("Firefox profile %s: %v\n%s", profile, err, out))
		} else {
			installed = append(installed, "Firefox ("+filepath.Base(profile)+")")
		}
	}

	if len(installed) == 0 {
		msg := "certutil ran but no stores were updated"
		if len(errs) > 0 {
			msg = strings.Join(errs, "\n")
		}
		return "", manualInstructions(certPath, msg)
	}

	result := "CA certificate installed for: " + strings.Join(installed, ", ") + "."
	if len(errs) > 0 {
		result += "\nSome stores failed:\n" + strings.Join(errs, "\n")
	}
	return result, nil
}

func installDarwin(certPath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", manualInstructions(certPath, fmt.Sprintf("cannot find home directory: %v", err))
	}

	loginKeychain := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	cmd := exec.Command("security", "add-trusted-cert",
		"-d", "-r", "trustRoot",
		"-k", loginKeychain,
		certPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", manualInstructions(certPath, fmt.Sprintf("security add-trusted-cert failed: %v\n%s", err, out))
	}
	return "CA certificate installed to macOS login keychain.", nil
}

func installWindows(certPath string) (string, error) {
	// certutil is built into Windows, no extra tools needed.
	// -user flag installs to current user store, no admin required.
	cmd := exec.Command("certutil", "-addstore", "-user", "-f", "ROOT", certPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", manualInstructions(certPath, fmt.Sprintf("certutil failed: %v\n%s", err, out))
	}
	return "CA certificate installed to Windows user root store.", nil
}

func firefoxProfiles() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
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

func manualInstructions(certPath, reason string) error {
	return errors.New(reason + "\n\n" +
		"To install manually, import this file into your browser:\n" +
		"  " + certPath + "\n\n" +
		"Browser instructions:\n" +
		"  Chrome/Chromium: Settings → Privacy → Security → Manage certificates → Authorities → Import\n" +
		"  Firefox:         Settings → Privacy & Security → Certificates → View Certificates → Authorities → Import\n" +
		"  NixOS:           security.pki.certificateFiles = [ \"" + certPath + "\" ]")
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
