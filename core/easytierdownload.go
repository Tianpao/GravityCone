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

// DownloadProgressData is the data shape emitted during download progress events.
type DownloadProgressData struct {
	Step      string `json:"step"`       // "downloading" or "extracting"
	Percent   int    `json:"percent"`    // 0-100
	TotalSize int64  `json:"total_size"` // total bytes (0 if unknown)
	Speed     int64  `json:"speed"`      // bytes/sec (download step only)
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

// ensureEasyTierEmitter is the event emitter used by EnsureEasyTier for progress reporting.
var ensureEasyTierEmitter EventEmitter = NilEventEmitter{}

// SetEnsureEasyTierEmitter sets the event emitter for download progress reporting.
func SetEnsureEasyTierEmitter(emitter EventEmitter) {
	if emitter != nil {
		ensureEasyTierEmitter = emitter
	}
}

// EnsureEasyTier checks if easytier-core and easytier-cli exist locally,
// and downloads them if missing. Emits "download.progress" events via the
// configured emitter. Call this at startup before any EasyTier operations.
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

	// Download zip to temp file with progress tracking
	tmpFile, err := os.CreateTemp("", "easytier-*.zip")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := downloadFileWithProgress(tmpFile, url); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("download failed: %w", err)
	}
	tmpFile.Close()

	// Extract needed files from zip
	ensureEasyTierEmitter.Emit("download.progress", DownloadProgressData{
		Step:    "extracting",
		Percent: 0,
	})
	if err := extractEasyTierZip(tmpPath, targetDir); err != nil {
		return "", fmt.Errorf("extract failed: %w", err)
	}
	ensureEasyTierEmitter.Emit("download.progress", DownloadProgressData{
		Step:    "extracting",
		Percent: 100,
	})

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

// downloadFileWithProgress downloads url into dst with progress events emitted every second.
func downloadFileWithProgress(dst io.Writer, url string) error {
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	total := resp.ContentLength
	var written int64
	var lastReport time.Time
	lastWritten := int64(0)

	buf := make([]byte, 32*1024)
	for {
		nr, readErr := resp.Body.Read(buf)
		if nr > 0 {
			nw, writeErr := dst.Write(buf[:nr])
			if writeErr != nil {
				return writeErr
			}
			written += int64(nw)
		}

		now := time.Now()
		if now.Sub(lastReport) >= time.Second {
			elapsed := now.Sub(lastReport).Seconds()
			if elapsed <= 0 {
				elapsed = 1
			}
			speed := int64(float64(written-lastWritten) / elapsed)

			percent := 0
			if total > 0 {
				percent = int(written * 100 / total)
			}

			ensureEasyTierEmitter.Emit("download.progress", DownloadProgressData{
				Step:      "downloading",
				Percent:   percent,
				TotalSize: total,
				Speed:     speed,
			})
			lastReport = now
			lastWritten = written
		}

		if readErr != nil {
			break
		}
	}

	// Final progress event
	ensureEasyTierEmitter.Emit("download.progress", DownloadProgressData{
		Step:      "downloading",
		Percent:   100,
		TotalSize: total,
		Speed:     0,
	})

	return nil
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
