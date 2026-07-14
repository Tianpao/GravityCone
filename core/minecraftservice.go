package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	msAuthorizeURL = "https://login.microsoftonline.com/consumers/oauth2/v2.0/authorize"
	msTokenURL     = "https://login.microsoftonline.com/consumers/oauth2/v2.0/token"
	xblAuthURL     = "https://user.auth.xboxlive.com/user/authenticate"
	xstsAuthURL    = "https://xsts.auth.xboxlive.com/xsts/authorize"
	mcAuthURL      = "https://api.minecraftservices.com/authentication/login_with_xbox"
	mcProfileURL   = "https://api.minecraftservices.com/minecraft/profile"
	msScopes       = "XboxLive.signin offline_access"
	msLoginTimeout = 5 * time.Minute
	msHTTPTimeout  = 15 * time.Second
)

type MinecraftUser struct {
	Username    string `json:"username"`
	UUID        string `json:"uuid"`
	AccessToken string `json:"access_token"`
	AvatarPNG   string `json:"avatar_png"`
}

type msTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

type xblAuthResponse struct {
	Token string `json:"Token"`
}

type xstsAuthResponse struct {
	Token         string `json:"Token"`
	DisplayClaims struct {
		XUI []struct {
			UHS string `json:"uhs"`
		} `json:"xui"`
	} `json:"DisplayClaims"`
}

type mcAuthResponse struct {
	AccessToken string `json:"access_token"`
	Username    string `json:"username"`
	ExpiresIn   int    `json:"expires_in"`
}

type mcProfileResponse struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Skins []struct {
		ID     string `json:"id"`
		State  string `json:"state"`
		URL    string `json:"url"`
	} `json:"skins"`
}

type minecraftSession struct {
	MSAccessToken  string         `json:"ms_access_token"`
	MSRefreshToken string         `json:"ms_refresh_token"`
	User           *MinecraftUser `json:"user"`
}

type MinecraftService struct {
	clientID      string
	clientSecret  string
	msAccessToken  string
	msRefreshToken string
	codeVerifier  string
	User          *MinecraftUser
}

func generateCodeVerifier() string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = charsetForPKCE[i%len(charsetForPKCE)]
	}
	return string(b)
}

var charsetForPKCE = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func NewMinecraftService(clientID, clientSecret string) *MinecraftService {
	return &MinecraftService{
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

func (s *MinecraftService) sessionFilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "GravityCone", "minecraft_session.json"), nil
}

func (s *MinecraftService) saveSession() error {
	path, err := s.sessionFilePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data := minecraftSession{
		MSAccessToken:  s.msAccessToken,
		MSRefreshToken: s.msRefreshToken,
		User:           s.User,
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

func (s *MinecraftService) loadSession() error {
	path, err := s.sessionFilePath()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var data minecraftSession
	if err := json.Unmarshal(b, &data); err != nil {
		return err
	}
	if data.MSAccessToken != "" && data.User != nil {
		s.msAccessToken = data.MSAccessToken
		s.msRefreshToken = data.MSRefreshToken
		s.User = data.User
	}
	return nil
}

func (s *MinecraftService) clearSession() error {
	path, err := s.sessionFilePath()
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (s *MinecraftService) RestoreSession() error {
	if err := s.loadSession(); err != nil {
		return err
	}
	if s.msAccessToken == "" || s.User == nil {
		return nil
	}
	// Try the cached Minecraft token directly.
	_, err := s.fetchMcProfile(s.User.AccessToken)
	if err == nil {
		_ = s.saveSession()
		return nil
	}
	// Minecraft token expired — try refreshing the MS token and re-running the chain.
	if refreshErr := s.refreshMsToken(); refreshErr != nil {
		s.msAccessToken = ""
		s.msRefreshToken = ""
		s.User = nil
		_ = s.clearSession()
		return nil
	}
	mcToken, chainErr := s.runTokenChain()
	if chainErr != nil {
		s.msAccessToken = ""
		s.msRefreshToken = ""
		s.User = nil
		_ = s.clearSession()
		return nil
	}
	user, profileErr := s.fetchMcProfile(mcToken)
	if profileErr != nil {
		s.msAccessToken = ""
		s.msRefreshToken = ""
		s.User = nil
		_ = s.clearSession()
		return nil
	}
	user.AccessToken = mcToken
	s.User = user
	_ = s.saveSession()
	return nil
}

func (s *MinecraftService) StartLogin() (*MinecraftUser, error) {
	if s.clientID == "" || s.clientSecret == "" {
		return nil, fmt.Errorf("Microsoft OAuth2 credentials not configured")
	}

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, fmt.Errorf("failed to start callback server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	// Generate PKCE data
	s.codeVerifier = generateCodeVerifier()
	codeChallenge := generateCodeChallenge(s.codeVerifier)

	resultCh := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Missing authorization code"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html><html><body style="background:#06070f;color:#fff;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;font-family:Inter,sans-serif"><h3>Login successful! You can close this tab.</h3></body></html>`))
		resultCh <- code
	})}

	go srv.Serve(listener)

	params := url.Values{}
	params.Set("client_id", s.clientID)
	params.Set("response_type", "code")
	params.Set("redirect_uri", redirectURI)
	params.Set("response_mode", "query")
	params.Set("scope", msScopes)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")

	authURL := msAuthorizeURL + "?" + params.Encode()

	if err := openBrowser(authURL); err != nil {
		srv.Shutdown(context.Background())
		return nil, fmt.Errorf("failed to open browser: %w", err)
	}

	select {
	case code := <-resultCh:
		srv.Shutdown(context.Background())
		if err := s.exchangeCode(code, redirectURI); err != nil {
			return nil, fmt.Errorf("token exchange failed: %w", err)
		}
		mcToken, err := s.runTokenChain()
		if err != nil {
			return nil, fmt.Errorf("Minecraft auth chain failed: %w", err)
		}
		user, err := s.fetchMcProfile(mcToken)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch Minecraft profile: %w", err)
		}
		user.AccessToken = mcToken
		s.User = user
		_ = s.saveSession()
		return user, nil
	case <-time.After(msLoginTimeout):
		srv.Shutdown(context.Background())
		return nil, fmt.Errorf("login timed out after 5 minutes")
	}
}

func (s *MinecraftService) GetCurrentUser() *MinecraftUser {
	return s.User
}

func (s *MinecraftService) Logout() {
	s.msAccessToken = ""
	s.msRefreshToken = ""
	s.User = nil
	_ = s.clearSession()
}

func (s *MinecraftService) exchangeCode(code, redirectURI string) error {
	data := url.Values{}
	data.Set("client_id", s.clientID)
	data.Set("code", code)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", redirectURI)
	data.Set("client_secret", s.clientSecret)
	data.Set("scope", msScopes)
	if s.codeVerifier != "" {
		data.Set("code_verifier", s.codeVerifier)
	}

	req, err := http.NewRequest("POST", msTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: msHTTPTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var tokenResp msTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return fmt.Errorf("invalid MS token response: %s", string(body))
	}
	if tokenResp.Error != "" {
		return fmt.Errorf("MS OAuth error: %s - %s", tokenResp.Error, tokenResp.ErrorDesc)
	}
	if tokenResp.AccessToken == "" {
		return fmt.Errorf("empty MS access token in response")
	}

	s.msAccessToken = tokenResp.AccessToken
	s.msRefreshToken = tokenResp.RefreshToken
	return nil
}

func (s *MinecraftService) refreshMsToken() error {
	data := url.Values{}
	data.Set("client_id", s.clientID)
	data.Set("client_secret", s.clientSecret)
	data.Set("refresh_token", s.msRefreshToken)
	data.Set("grant_type", "refresh_token")
	data.Set("scope", msScopes)

	req, err := http.NewRequest("POST", msTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: msHTTPTimeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var tokenResp msTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return fmt.Errorf("invalid MS refresh response: %s", string(body))
	}
	if tokenResp.Error != "" {
		return fmt.Errorf("MS refresh error: %s - %s", tokenResp.Error, tokenResp.ErrorDesc)
	}
	if tokenResp.AccessToken == "" {
		return fmt.Errorf("empty MS access token in refresh response")
	}

	s.msAccessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		s.msRefreshToken = tokenResp.RefreshToken
	}
	return nil
}

func (s *MinecraftService) runTokenChain() (string, error) {
	xblToken, err := s.exchangeMsTokenForXbl()
	if err != nil {
		return "", fmt.Errorf("XBL exchange failed: %w", err)
	}
	xstsToken, userhash, err := s.exchangeXblForXsts(xblToken)
	if err != nil {
		return "", fmt.Errorf("XSTS exchange failed: %w", err)
	}
	mcToken, err := s.exchangeXstsForMcToken(xstsToken, userhash)
	if err != nil {
		return "", fmt.Errorf("Minecraft token exchange failed: %w", err)
	}
	return mcToken, nil
}

func (s *MinecraftService) exchangeMsTokenForXbl() (string, error) {
	reqBody := struct {
		Properties struct {
			AuthMethod string `json:"AuthMethod"`
			SiteName   string `json:"SiteName"`
			RpsTicket  string `json:"RpsTicket"`
		} `json:"Properties"`
		RelyingParty string `json:"RelyingParty"`
		TokenType    string `json:"TokenType"`
	}{
		RelyingParty: "http://auth.xboxlive.com",
		TokenType:    "JWT",
	}
	reqBody.Properties.AuthMethod = "RPS"
	reqBody.Properties.SiteName = "user.auth.xboxlive.com"
	reqBody.Properties.RpsTicket = "d=" + s.msAccessToken

	encoded, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", xblAuthURL, bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-xbl-contract-version", "1")

	resp, err := (&http.Client{Timeout: msHTTPTimeout}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("XBL auth failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var xblResp xblAuthResponse
	if err := json.Unmarshal(body, &xblResp); err != nil {
		return "", fmt.Errorf("invalid XBL response: %s", string(body))
	}
	if xblResp.Token == "" {
		return "", fmt.Errorf("empty XBL token in response")
	}
	return xblResp.Token, nil
}

func (s *MinecraftService) exchangeXblForXsts(xblToken string) (xstsToken string, userhash string, err error) {
	reqBody := struct {
		Properties struct {
			SandboxId  string   `json:"SandboxId"`
			UserTokens []string `json:"UserTokens"`
		} `json:"Properties"`
		RelyingParty string `json:"RelyingParty"`
		TokenType    string `json:"TokenType"`
	}{
		RelyingParty: "rp://api.minecraftservices.com/",
		TokenType:    "JWT",
	}
	reqBody.Properties.SandboxId = "RETAIL"
	reqBody.Properties.UserTokens = []string{xblToken}

	encoded, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", err
	}

	req, err := http.NewRequest("POST", xstsAuthURL, bytes.NewReader(encoded))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-xbl-contract-version", "1")

	resp, err := (&http.Client{Timeout: msHTTPTimeout}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("XSTS auth failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var xstsResp xstsAuthResponse
	if err := json.Unmarshal(body, &xstsResp); err != nil {
		return "", "", fmt.Errorf("invalid XSTS response: %s", string(body))
	}
	if xstsResp.Token == "" {
		return "", "", fmt.Errorf("empty XSTS token in response")
	}
	if len(xstsResp.DisplayClaims.XUI) == 0 || xstsResp.DisplayClaims.XUI[0].UHS == "" {
		return "", "", fmt.Errorf("missing user hash (UHS) in XSTS response")
	}

	return xstsResp.Token, xstsResp.DisplayClaims.XUI[0].UHS, nil
}

func (s *MinecraftService) exchangeXstsForMcToken(xstsToken, userhash string) (string, error) {
	reqBody := struct {
		IdentityToken      string `json:"identityToken"`
		EnsureLegacyEnabled bool  `json:"ensureLegacyEnabled"`
	}{
		IdentityToken:      fmt.Sprintf("XBL3.0 x=%s;%s", userhash, xstsToken),
		EnsureLegacyEnabled: true,
	}

	encoded, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", mcAuthURL, bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: msHTTPTimeout}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Minecraft auth failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var mcResp mcAuthResponse
	if err := json.Unmarshal(body, &mcResp); err != nil {
		return "", fmt.Errorf("invalid Minecraft auth response: %s", string(body))
	}
	if mcResp.AccessToken == "" {
		return "", fmt.Errorf("empty Minecraft access token in response")
	}
	return mcResp.AccessToken, nil
}

func (s *MinecraftService) fetchMcProfile(accessToken string) (*MinecraftUser, error) {
	req, err := http.NewRequest("GET", mcProfileURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := (&http.Client{Timeout: msHTTPTimeout}).Do(req)
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
		avatarPNG, _ = cropAvatarFromSkin(skinURL)
	}

	return &MinecraftUser{
		Username:  profile.Name,
		UUID:      profile.ID,
		AvatarPNG: avatarPNG,
	}, nil
}

// cropAvatarFromSkin downloads a Minecraft skin texture and crops the head
// region into a base64-encoded PNG. The output is 10x8 (scaled) to include
// the hat/overlay side extensions visible in 3D front view.
func cropAvatarFromSkin(skinURL string) (string, error) {
	resp, err := (&http.Client{Timeout: msHTTPTimeout}).Get(skinURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	img, err := png.Decode(resp.Body)
	if err != nil {
		return "", err
	}

	bounds := img.Bounds()
	is64x64 := bounds.Dy() >= 64

	// Output dimensions: 10x8 to include hat side extensions (1px each side)
	scale := 8
	faceW, faceH := 10, 8
	outW := faceW * scale
	outH := faceH * scale
	out := image.NewNRGBA(image.Rect(0, 0, outW, outH))

	// Helper to draw a source column to a destination column
	drawColumn := func(srcX, srcYBase, dstCol int) {
		for dy := 0; dy < 8; dy++ {
			c := color.NRGBAModel.Convert(img.At(srcX, srcYBase+dy)).(color.NRGBA)
			if c.A == 0 {
				continue
			}
			for sy := 0; sy < scale; sy++ {
				for sx := 0; sx < scale; sx++ {
					out.SetNRGBA(dstCol*scale+sx, dy*scale+sy, c)
				}
			}
		}
	}

	// Helper to draw an 8x8 block at a destination offset
	drawBlock := func(srcX, srcYBase, dstColOffset int, skipTransparent bool) {
		for dy := 0; dy < 8; dy++ {
			for dx := 0; dx < 8; dx++ {
				c := color.NRGBAModel.Convert(img.At(srcX+dx, srcYBase+dy)).(color.NRGBA)
				if skipTransparent && c.A == 0 {
					continue
				}
				for sy := 0; sy < scale; sy++ {
					for sx := 0; sx < scale; sx++ {
						out.SetNRGBA((dx+dstColOffset)*scale+sx, dy*scale+sy, c)
					}
				}
			}
		}
	}

	// Base head layer: 8x8 at (8,8), centered in the 10x8 output (offset by 1 col)
	drawBlock(8, 8, 1, false)

	// Hat overlay front: 8x8 at (40,8), centered over the base head
	drawBlock(40, 8, 1, true)

	// Hat side extensions for 64x64 skins (Modern 1.8+ format)
	if is64x64 {
		// Column 0 (left of face): rightmost column of hat right side (x=39)
		// Column 9 (right of face): leftmost column of hat left side (x=48)
		drawColumn(39, 8, 0)
		drawColumn(48, 8, 9)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
