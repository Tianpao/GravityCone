package app

import (
	"bytes"
	"embed"
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
	"golang.org/x/image/webp"

	"gravitycone/core/protocol/paperconnect"
	"gravitycone/core/protocol/scaffolding"
)

//go:embed images/*
var embeddedImages embed.FS

const embeddedPrefix = "embedded:"

func init() {
	// The standard library cannot decode WebP; register the golang.org/x/image
	// decoder so image.Decode handles .webp sources transparently.
	image.RegisterFormat("webp", "RIFF????WEBP", webp.Decode, webp.DecodeConfig)
}

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

// The resulting image looks identical to the original to the naked eye.
func (w *WatermarkService) EncodeRoomCode(sourcePath string, roomCode string) (*WatermarkResult, error) {
	slog.Info("EncodeRoomCode", "source", sourcePath, "roomCode", roomCode)

	var srcData []byte
	var err error

	if strings.HasPrefix(sourcePath, embeddedPrefix) {
		name := strings.TrimPrefix(sourcePath, embeddedPrefix)
		srcData, err = embeddedImages.ReadFile("images/" + name)
	} else {
		srcData, err = os.ReadFile(sourcePath)
	}
	if err != nil {
		return nil, fmt.Errorf("读取源图片失败: %w", err)
	}
	srcImg, _, err := image.Decode(bytes.NewReader(srcData))
	if err != nil {
		return nil, fmt.Errorf("解码源图片失败: %w", err)
	}

	payload := padPayload(roomCode)

	engine := bwm.New(seedImg, seedWm)
	engine.D1 = 45.0 // higher = more robust against compression

	wmBits := bwm.TextToBits(payload)
	watermarkedImg, err := engine.Embed(srcImg, wmBits)
	if err != nil {
		return nil, fmt.Errorf("嵌入房间信息失败: %w", err)
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, watermarkedImg)
	outputData := buf.Bytes()

	baseName := sourcePath
	if strings.HasPrefix(sourcePath, embeddedPrefix) {
		baseName = strings.TrimPrefix(sourcePath, embeddedPrefix)
	}
	baseName = filepath.Base(baseName)
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
		return "", fmt.Errorf("图片解码失败，请确认拖入的是有效的PNG/JPEG/WebP图片")
	}
	slog.Info("image decoded", "bounds", img.Bounds())

	engine := bwm.New(seedImg, seedWm)
	engine.D1 = 45.0

	wmBits, err := engine.Extract(img, payloadBytes*8)
	if err != nil {
		return "", fmt.Errorf("提取房间信息失败: %w", err)
	}
	slog.Info("extracted bits", "count", len(wmBits))

	text := bwm.BitsToText(wmBits)
	slog.Info("raw extracted text", "len", len(text), "text", text)

	code := unpadPayload(text)
	slog.Info("unpad result", "code", code)

	if _, err := scaffolding.ParseRoomCode(code); err == nil {
		slog.Info("valid Scaffolding room code", "code", code)
	} else if _, err := paperconnect.ParsePaperConnectRoomCode(code); err == nil {
		slog.Info("valid PaperConnect room code", "code", code)
	} else {
		// Try adding U/ prefix — if code already has a prefix it's unrecoverable
		if strings.HasPrefix(strings.ToUpper(code), "U/") || strings.HasPrefix(strings.ToUpper(code), "P/") {
			return "", fmt.Errorf("图片中的房间代码无效，可能图片未包含房间信息或被过度压缩")
		}
		uCode := "U/" + code
		if _, err := scaffolding.ParseRoomCode(uCode); err == nil {
			code = uCode
			slog.Info("added U/ prefix", "code", code)
		} else {
			pCode := "P/" + code
			if _, err := paperconnect.ParsePaperConnectRoomCode(pCode); err == nil {
				code = pCode
				slog.Info("added P/ prefix", "code", code)
			} else {
				return "", fmt.Errorf("图片中的房间代码无效，可能图片未包含房间信息或被过度压缩")
			}
		}
	}

	slog.Info("final room code", "code", code)
	return code, nil
}

func (w *WatermarkService) ListDemoImages() ([]string, error) {
	entries, err := embeddedImages.ReadDir("images")
	if err != nil {
		return nil, fmt.Errorf("读取内置图片失败: %w", err)
	}

	var images []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".jpg") || strings.HasSuffix(name, ".jpeg") || strings.HasSuffix(name, ".webp") {
			images = append(images, embeddedPrefix+entry.Name())
		}
	}

	if len(images) == 0 {
		return nil, fmt.Errorf("未找到内置演示图片")
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
