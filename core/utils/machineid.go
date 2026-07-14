package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	machineIDOnce sync.Once
	machineIDVal  string
	machineIDErr  error
)

func GetMachineID() (string, error) {
	machineIDOnce.Do(func() {
		machineIDVal, machineIDErr = loadOrGenerateMachineID()
	})
	return machineIDVal, machineIDErr
}

func loadOrGenerateMachineID() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("无法获取配置目录: %w", err)
	}

	dir := filepath.Join(configDir, "GravityCone")
	path := filepath.Join(dir, "machine_id")

	data, err := os.ReadFile(path)
	if err == nil && len(data) > 0 {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	}

	id, err := generateMachineID()
	if err != nil {
		return "", fmt.Errorf("无法生成机器ID: %w", err)
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return id, nil // return ID even if persist fails
	}
	os.WriteFile(path, []byte(id+"\n"), 0600)
	return id, nil
}

func generateMachineID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	h := hex.EncodeToString(buf[:])
	return fmt.Sprintf("%s-%s-%s-%s", h[:4], h[4:8], h[8:12], h[12:16]), nil
}
