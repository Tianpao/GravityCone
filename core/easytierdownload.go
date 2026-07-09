package core

import (
	"archive/zip"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// EasyTierVersion is the expected EasyTier release version.
const EasyTierVersion = "v2.6.4"

// easyTierBaseURL is the base URL for downloading EasyTier releases.
// Override with SetEasyTierBaseURL or the EASYTIER_MIRROR_URL env var.
var easyTierBaseURL = "https://github.com/EasyTier/EasyTier/releases/download"

func init() {
	if envURL := os.Getenv("EASYTIER_MIRROR_URL"); envURL != "" {
		easyTierBaseURL = strings.TrimRight(envURL, "/")
	}
}

// SetEasyTierBaseURL replaces the default download base URL (for mirror/acceleration).
// Pass a URL without trailing slash. Empty string is a no-op.
func SetEasyTierBaseURL(url string) {
	if url != "" {
		easyTierBaseURL = strings.TrimRight(url, "/")
	}
}

// easyTierPlatform holds the OS and arch segments used in the download URL.
type easyTierPlatform struct {
	sys  string // "windows", "macos", "linux", "freebsd"
	arch string // "x86_64", "aarch64", "loongarch64", "riscv64"
}

// detectEasyTierPlatform maps runtime.GOOS/GOARCH to EasyTier release naming.
func detectEasyTierPlatform() (easyTierPlatform, error) {
	switch runtime.GOOS {
	case "windows":
		return easyTierPlatform{sys: "windows", arch: "x86_64"}, nil
	case "darwin":
		switch runtime.GOARCH {
		case "arm64":
			return easyTierPlatform{sys: "macos", arch: "aarch64"}, nil
		case "amd64":
			return easyTierPlatform{sys: "macos", arch: "x86_64"}, nil
		}
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return easyTierPlatform{sys: "linux", arch: "x86_64"}, nil
		case "arm64":
			return easyTierPlatform{sys: "linux", arch: "aarch64"}, nil
		case "loong64":
			return easyTierPlatform{sys: "linux", arch: "loongarch64"}, nil
		case "riscv64":
			return easyTierPlatform{sys: "linux", arch: "riscv64"}, nil
		}
	case "freebsd":
		return easyTierPlatform{sys: "freebsd", arch: "x86_64"}, nil
	}
	return easyTierPlatform{}, fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
}

func (p easyTierPlatform) downloadURL() string {
	return fmt.Sprintf("%s/%s/easytier-%s-%s-%s.zip",
		easyTierBaseURL, EasyTierVersion, p.sys, p.arch, EasyTierVersion)
}

// EnsureEasyTier checks if easytier-core and easytier-cli exist locally,
// and downloads them if missing. Call this at startup before any EasyTier operations.
func EnsureEasyTier() error {
	corePath, err := resolveEasyTierBinary("easytier-core")
	if err == nil {
		cliPath, err2 := resolveEasyTierBinary("easytier-cli")
		if err2 == nil {
			slog.Info("EasyTier binaries found", "core", corePath, "cli", cliPath)
			return nil
		}
	}

	slog.Info("EasyTier binaries not found, starting auto-download")
	_, err = downloadEasyTierBinary("easytier-core")
	if err != nil {
		return fmt.Errorf("auto-download easytier-core failed: %w", err)
	}
	_, err = downloadEasyTierBinary("easytier-cli")
	if err != nil {
		return fmt.Errorf("auto-download easytier-cli failed: %w", err)
	}
	slog.Info("EasyTier binaries ready")
	return nil
}

// downloadMu serializes download+extract to prevent concurrent goroutines from
// downloading the same zip simultaneously.
var downloadMu sync.Mutex

// downloadEasyTierBinary downloads the EasyTier release zip and extracts the
// requested binary (plus supporting DLLs on Windows) into the easytier/ directory.
// Returns the absolute path to the extracted binary.
func downloadEasyTierBinary(name string) (string, error) {
	exeName := name
	if runtime.GOOS == "windows" {
		exeName = name + ".exe"
	}

	// Resolve target directory (same logic as resolveEasyTierBinary)
	targetDir := resolveEasyTierDir()

	downloadMu.Lock()
	defer downloadMu.Unlock()

	// Double-check: another goroutine may have downloaded while we waited
	if p := filepath.Join(targetDir, exeName); fileExists(p) {
		abs, _ := filepath.Abs(p)
		return abs, nil
	}

	plat, err := detectEasyTierPlatform()
	if err != nil {
		return "", err
	}

	url := plat.downloadURL()
	slog.Info("downloading EasyTier", "url", url, "target", targetDir)

	// Download zip to temp file
	tmpFile, err := os.CreateTemp("", "easytier-*.zip")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := downloadFile(tmpFile, url); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("download failed: %w", err)
	}
	tmpFile.Close()

	// Extract needed files from zip
	if err := extractEasyTierZip(tmpPath, targetDir); err != nil {
		return "", fmt.Errorf("extract failed: %w", err)
	}

	// Verify the binary we need is now present
	result := filepath.Join(targetDir, exeName)
	if !fileExists(result) {
		return "", fmt.Errorf("%s not found in archive", exeName)
	}
	abs, _ := filepath.Abs(result)
	slog.Info("EasyTier binary extracted", "path", abs)
	return abs, nil
}

// resolveEasyTierDir returns the easytier/ directory where binaries should be placed.
// Prefers next to the executable (matching resolveEasyTierBinary's search order),
// falls back to relative path.
func resolveEasyTierDir() string {
	if exeDir, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exeDir), "easytier")
	}
	abs, _ := filepath.Abs("easytier")
	return abs
}

// downloadFile downloads url into the given writer with a 60-second timeout.
func downloadFile(dst io.Writer, url string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	_, err = io.Copy(dst, resp.Body)
	return err
}

// extractEasyTierZip extracts easytier-core, easytier-cli, and (on Windows)
// .dll/.sys files from the zip into targetDir.
func extractEasyTierZip(zipPath, targetDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	for _, f := range r.File {
		base := filepath.Base(f.Name)
		if strings.Contains(base, "..") {
			continue
		}

		shouldExtract := false
		mode := os.FileMode(0755)

		switch {
		case base == "easytier-core" || base == "easytier-core.exe":
			shouldExtract = true
		case base == "easytier-cli" || base == "easytier-cli.exe":
			shouldExtract = true
		case runtime.GOOS == "windows" && (strings.HasSuffix(base, ".dll") || strings.HasSuffix(base, ".sys")):
			shouldExtract = true
			mode = 0644
		}

		if !shouldExtract {
			continue
		}

		dstPath := filepath.Join(targetDir, base)
		if err := extractZipEntry(f, dstPath, mode); err != nil {
			slog.Warn("failed to extract zip entry", "name", f.Name, "error", err)
			continue
		}
	}
	return nil
}

// extractZipEntry writes a single zip file entry to dstPath with the given mode.
func extractZipEntry(f *zip.File, dstPath string, mode os.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, rc)
	return err
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
