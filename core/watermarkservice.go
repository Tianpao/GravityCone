package core

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/kirklin/go-blind-watermark/bwm"
)

const demoImagesDir = "images"

// Fixed seeds — same seeds mean anyone with the app can decode room codes.
const seedImg = 12345
const seedWm = 67890

// We always encode 32 bytes (256 bits) for consistent extraction.
const payloadBytes = 32

type WatermarkResult struct {
	OutputPath string `json:"output_path"`
	Base64PNG  string `json:"base64_png"`
}

type WatermarkService struct{}

// EncodeRoomCode embeds a room code into a source image using blind watermarking.
// The resulting image looks identical to the original to the naked eye.
func (w *WatermarkService) EncodeRoomCode(sourcePath string, roomCode string) (*WatermarkResult, error) {
	if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("源图片不存在: %s", sourcePath)
	}
	slog.Info("EncodeRoomCode", "source", sourcePath, "roomCode", roomCode)

	// 1. Decode source image
	srcData, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("读取源图片失败: %w", err)
	}
	srcImg, _, err := image.Decode(bytes.NewReader(srcData))
	if err != nil {
		return nil, fmt.Errorf("解码源图片失败: %w", err)
	}

	// 2. Prepare fixed-length payload (pad room code to 32 bytes)
	payload := padPayload(roomCode)

	// 3. Embed blind watermark
	engine := bwm.New(seedImg, seedWm)
	engine.D1 = 45.0 // higher = more robust against compression

	wmBits := bwm.TextToBits(payload)
	watermarkedImg, err := engine.Embed(srcImg, wmBits)
	if err != nil {
		return nil, fmt.Errorf("嵌入房间信息失败: %w", err)
	}

	// 4. Encode result to PNG in memory
	var buf bytes.Buffer
	if err := png.Encode(&buf, watermarkedImg); err != nil {
		return nil, fmt.Errorf("编码输出图片失败: %w", err)
	}
	outputData := buf.Bytes()

	// 5. Save to persistent location
	baseName := filepath.Base(sourcePath)
	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)
	outputName := nameWithoutExt + "_watermarked.png"

	persistentDir := filepath.Join(os.TempDir(), "gravitycone_watermarks")
	os.MkdirAll(persistentDir, 0755)
	persistentPath := filepath.Join(persistentDir, outputName)
	if err := os.WriteFile(persistentPath, outputData, 0644); err != nil {
		return nil, fmt.Errorf("保存图片失败: %w", err)
	}

	result := &WatermarkResult{
		OutputPath: persistentPath,
		Base64PNG:  base64.StdEncoding.EncodeToString(outputData),
	}
	slog.Info("EncodeRoomCode done", "output", persistentPath, "base64_len", len(result.Base64PNG))
	return result, nil
}

// DecodeRoomCode extracts a room code from a blind-watermarked image (base64 encoded).
func (w *WatermarkService) DecodeRoomCode(imageBase64 string) (string, error) {
	slog.Info("DecodeRoomCode", "base64_len", len(imageBase64))

	data, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		return "", fmt.Errorf("图片数据解码失败: %w", err)
	}
	slog.Info("decoded base64", "bytes", len(data))

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("图片解码失败，请确认拖入的是有效的PNG/JPEG图片")
	}
	slog.Info("image decoded", "bounds", img.Bounds())

	// Extract blind watermark (32 bytes = 256 bits)
	engine := bwm.New(seedImg, seedWm)
	engine.D1 = 45.0

	wmBits, err := engine.Extract(img, payloadBytes*8)
	if err != nil {
		return "", fmt.Errorf("提取房间信息失败: %w", err)
	}
	slog.Info("extracted bits", "count", len(wmBits))

	text := bwm.BitsToText(wmBits)
	slog.Info("raw extracted text", "len", len(text), "text", text)

	// Unpad: remove trailing spaces and null bytes
	code := unpadPayload(text)
	slog.Info("unpad result", "code", code)

	// Validate the room code
	if _, err := ParseRoomCode(code); err != nil {
		// Try without U/ prefix
		if !strings.HasPrefix(strings.ToUpper(code), "U/") {
			code = "U/" + code
			slog.Info("added U/ prefix", "code", code)
			if _, err := ParseRoomCode(code); err != nil {
				return "", fmt.Errorf("图片中的房间代码无效，可能图片未包含房间信息或被过度压缩")
			}
		} else {
			return "", fmt.Errorf("图片中的房间代码无效，可能图片未包含房间信息或被过度压缩")
		}
	}

	slog.Info("final room code", "code", code)
	return code, nil
}

// ListDemoImages returns absolute paths to images in the images directory.
func (w *WatermarkService) ListDemoImages() ([]string, error) {
	entries, err := os.ReadDir(demoImagesDir)
	if err != nil {
		return nil, fmt.Errorf("images 目录不存在，请在项目根目录创建 images 文件夹并放入演示图片")
	}

	var images []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") {
			absPath, err := filepath.Abs(filepath.Join(demoImagesDir, entry.Name()))
			if err == nil {
				images = append(images, absPath)
			}
		}
	}

	if len(images) == 0 {
		return nil, fmt.Errorf("images 目录中没有找到图片文件（支持 PNG/JPG/JPEG）")
	}

	return images, nil
}

func padPayload(s string) string {
	b := make([]byte, payloadBytes)
	copy(b, s)
	return string(b)
}

func unpadPayload(s string) string {
	return strings.TrimRight(s, "\x00 ")
}
