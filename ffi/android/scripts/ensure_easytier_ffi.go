//go:build ignore

// +build ignore

// ensure_easytier_ffi downloads libeasytier_ffi.so from the qteasytier
// release for the given target platform. Run as:
//
//	go run ensure_easytier_ffi.go <arch> <output_dir>
//
// arch: arm64 (aarch64) or amd64 (x86_64)
// output_dir: where to place libeasytier_ffi.so (e.g. jniLibs/arm64-v8a)
package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	easyTierFFIVersion = "v2.6.4"
	releaseBaseURL     = "https://github.com/qteasytier/easytier-ffi-bin/releases/download"
	envMirrorURL       = "EASYTIER_FFI_MIRROR_URL"
)

// archMapping maps our arch names to the release asset names.
var archMapping = map[string]string{
	"arm64": "linux-aarch64",
	"amd64": "linux-amd64",
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: go run ensure_easytier_ffi.go <arch> <output_dir>\n")
		fmt.Fprintf(os.Stderr, "  arch: arm64 (aarch64) or amd64 (x86_64)\n")
		fmt.Fprintf(os.Stderr, "  output_dir: jniLibs directory\n")
		os.Exit(1)
	}

	arch := os.Args[1]
	outputDir := os.Args[2]

	releaseArch, ok := archMapping[arch]
	if !ok {
		fmt.Fprintf(os.Stderr, "Unsupported arch: %s (use: arm64, amd64)\n", arch)
		os.Exit(1)
	}

	if err := ensureEasyTierFFI(releaseArch, outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func ensureEasyTierFFI(releaseArch, outputDir string) error {
	soName := "libeasytier_ffi.so"
	destPath := filepath.Join(outputDir, soName)

	// Already exists?
	if _, err := os.Stat(destPath); err == nil {
		fmt.Printf("EasyTier FFI already exists: %s\n", destPath)
		return nil
	}

	// Construct download URL
	baseURL := releaseBaseURL
	if mirror := os.Getenv(envMirrorURL); mirror != "" {
		baseURL = strings.TrimRight(mirror, "/")
	}

	assetName := fmt.Sprintf("easytier-ffi-%s.tar.gz", releaseArch)
	url := fmt.Sprintf("%s/easytier-ffi-%s/%s",
		baseURL, easyTierFFIVersion, assetName)

	fmt.Printf("Downloading EasyTier FFI %s (%s)...\n", easyTierFFIVersion, releaseArch)

	// Download
	resp, err := httpGet(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}

	// Extract .so from tar.gz
	soBytes, err := extractFromTarGz(resp.Body, soName)
	if err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}

	// Create output directory
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outputDir, err)
	}

	// Write the .so file
	if err := os.WriteFile(destPath, soBytes, 0644); err != nil {
		return fmt.Errorf("write %s: %w", destPath, err)
	}

	fmt.Printf("EasyTier FFI downloaded to: %s (%.1f MB)\n",
		destPath, float64(len(soBytes))/1024/1024)
	return nil
}

func httpGet(url string) (*http.Response, error) {
	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GravityCone-FFI-Setup/1.0")
	return client.Do(req)
}

func extractFromTarGz(r io.Reader, targetFile string) ([]byte, error) {
	gzReader, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read: %w", err)
		}

		if strings.HasSuffix(header.Name, targetFile) && header.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(tarReader)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", header.Name, err)
			}
			return data, nil
		}
	}

	return nil, fmt.Errorf("%s not found in archive", targetFile)
}
