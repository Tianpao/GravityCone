package microsoft

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
)

type mcProfileResponse struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Skins []struct {
		ID    string `json:"id"`
		State string `json:"state"`
		URL   string `json:"url"`
	} `json:"skins"`
}

func (s *MinecraftService) fetchMcProfile(accessToken string) (*MinecraftUser, error) {
	req, _ := http.NewRequest("GET", mcProfileURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("the Microsoft account does not own Minecraft Java Edition")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Minecraft profile API error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var profile mcProfileResponse
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, fmt.Errorf("invalid profile response: %s", string(body))
	}
	if profile.ID == "" || profile.Name == "" {
		return nil, fmt.Errorf("incomplete Minecraft profile (user may not own the game)")
	}

	var skinURL string
	for _, s := range profile.Skins {
		if s.State == "ACTIVE" && s.URL != "" {
			skinURL = s.URL
			break
		}
	}

	var avatarPNG string
	if skinURL != "" {
		avatarPNG = cropAvatarFromSkin(skinURL)
	}

	return &MinecraftUser{
		Username:  profile.Name,
		UUID:      profile.ID,
		AvatarPNG: avatarPNG,
	}, nil
}

func cropAvatarFromSkin(skinURL string) string {
	resp, err := (&http.Client{Timeout: msHTTPTimeout}).Get(skinURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	img, err := png.Decode(resp.Body)
	if err != nil {
		return ""
	}

	const scale = 8
	outW := 10 * scale
	outH := 8 * scale
	out := image.NewNRGBA(image.Rect(0, 0, outW, outH))

	drawBlock := func(srcX, srcY, dstCol, w int, skipTransparent bool) {
		for dy := 0; dy < 8; dy++ {
			for dx := 0; dx < w; dx++ {
				c := color.NRGBAModel.Convert(img.At(srcX+dx, srcY+dy)).(color.NRGBA)
				if skipTransparent && c.A == 0 {
					continue
				}
				// Fill the scale×scale block directly in Pix
				baseY := (dy * scale) * out.Stride
				baseX := (dstCol+dx)*scale*4 + baseY
				for sy := 0; sy < scale; sy++ {
					rowOff := baseX + sy*out.Stride
					for sx := 0; sx < scale; sx++ {
						off := rowOff + sx*4
						out.Pix[off] = c.R
						out.Pix[off+1] = c.G
						out.Pix[off+2] = c.B
						out.Pix[off+3] = c.A
					}
				}
			}
		}
	}

	// Base head front: 8×8 at (8,8), centered at output column 1
	drawBlock(8, 8, 1, 8, false)
	// Hat overlay front: 8×8 at (40,8), over the base head
	drawBlock(40, 8, 1, 8, true)

	if img.Bounds().Dy() >= 64 {
		// Hat side extensions (1px wide each)
		drawBlock(39, 8, 0, 1, true)
		drawBlock(48, 8, 9, 1, true)
	}

	var buf bytes.Buffer
	if png.Encode(&buf, out) != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}
