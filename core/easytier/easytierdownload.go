//go:build !cli && !et_ffi

package easytier

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

	"gravitycone/core/utils"
)

const (
	EventDownloadProgress = "download.progress"
	EventDownloadError    = "download.error"
)

type DownloadProgressData struct {
	Step      string `json:"step"`
	Percent   int    `json:"percent"`
	TotalSize int64  `json:"total_size"`
	Speed     int64  `json:"speed"`
}

type DownloadErrorData struct {
	Error string `json:"error"`
}

var easyTierBaseURL = "https://github.com/EasyTier/EasyTier/releases/download"

func init() {
	if envURL := os.Getenv("EASYTIER_MIRROR_URL"); envURL != "" {
		easyTierBaseURL = strings.TrimRight(envURL, "/")
	}
}

func SetEasyTierBaseURL(url string) {
	if url != "" {
		easyTierBaseURL = strings.TrimRight(url, "/")
	}
}

// easyTierPlatform holds the OS and arch segments used in the download URL.
type easyTierPlatform struct {
	sys  string
	arch string
}

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

var ensureEasyTierEmitter utils.EventEmitter = utils.NilEventEmitter{}

func SetEnsureEasyTierEmitter(emitter utils.EventEmitter) {
	if emitter != nil {
		ensureEasyTierEmitter = emitter
	}
}

var ensureMu sync.Mutex

// EnsureEasyTier checks if easytier-core and easytier-cli exist locally,
// and downloads them if missing. Emits "download.progress" and "download.error"
// events via the configured emitter. Call this at startup before any EasyTier operations.
func EnsureEasyTier() error {
	ensureMu.Lock()
	defer ensureMu.Unlock()

	corePath, err := resolveEasyTierBinary("easytier-core")
	if err == nil {
		cliPath, err2 := resolveEasyTierBinary("easytier-cli")
		if err2 == nil {
			slog.Info("EasyTier binaries found", "core", corePath, "cli", cliPath)
			return nil
		}
	}

	slog.Info("EasyTier binaries not found, starting auto-download")
	if err := downloadAndExtractEasyTier(); err != nil {
		return emitDownloadError(fmt.Errorf("auto-download failed: %w", err))
	}
	slog.Info("EasyTier binaries ready")
	return nil
}

func emitDownloadError(err error) error {
	ensureEasyTierEmitter.Emit(EventDownloadError, DownloadErrorData{Error: err.Error()})
	return err
}

var downloadClient = &http.Client{Timeout: 120 * time.Second}

func downloadAndExtractEasyTier() error {
	targetDir := easyTierBaseDir()
	if targetDir == "" {
		return fmt.Errorf("cannot determine easytier directory")
	}

	plat, err := detectEasyTierPlatform()
	if err != nil {
		return err
	}

	url := plat.downloadURL()
	slog.Info("downloading EasyTier", "url", url, "target", targetDir)

	tmpFile, err := os.CreateTemp("", "easytier-*.zip")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := downloadFileWithProgress(tmpFile, url); err != nil {
		tmpFile.Close()
		return fmt.Errorf("download failed: %w", err)
	}
	tmpFile.Close()

	ensureEasyTierEmitter.Emit(EventDownloadProgress, DownloadProgressData{
		Step:    "extracting",
		Percent: 0,
	})
	if err := extractEasyTierZip(tmpPath, targetDir); err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}
	ensureEasyTierEmitter.Emit(EventDownloadProgress, DownloadProgressData{
		Step:    "extracting",
		Percent: 100,
	})

	for _, name := range []string{"easytier-core", "easytier-cli"} {
		exeName := PlatformExeName(name)
		if _, err := os.Stat(filepath.Join(targetDir, exeName)); err != nil {
			return fmt.Errorf("%s not found in archive", exeName)
		}
	}

	return nil
}

func downloadFileWithProgress(dst io.Writer, url string) error {
	resp, err := downloadClient.Get(url)
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

			ensureEasyTierEmitter.Emit(EventDownloadProgress, DownloadProgressData{
				Step:      "downloading",
				Percent:   percent,
				TotalSize: total,
				Speed:     speed,
			})
			lastReport = now
			lastWritten = written
		}

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	ensureEasyTierEmitter.Emit(EventDownloadProgress, DownloadProgressData{
		Step:      "downloading",
		Percent:   100,
		TotalSize: total,
		Speed:     0,
	})

	return nil
}

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

		mode := os.FileMode(0755)
		shouldExtract := false

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

// EasyTierDownloadService exposes the binary auto-download as a Wails service.
type EasyTierDownloadService struct{}

func (s *EasyTierDownloadService) Ensure() error {
	return EnsureEasyTier()
}
