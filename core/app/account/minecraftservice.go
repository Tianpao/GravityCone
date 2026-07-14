package account

import (
	"bytes"
	"context"
	"crypto/rand"
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
	clientID       string
	clientSecret   string
	msAccessToken  string
	msRefreshToken string
	client         *http.Client
	User           *MinecraftUser
}

var charsetForPKCE = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"

func generateCodeVerifier() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Fallback is better than an empty verifier — use time-based randomness
		for i := range b {
			b[i] = byte(time.Now().UnixNano()>>(i%8)) ^ byte(i*37)
		}
	}
	for i := range b {
		b[i] = charsetForPKCE[b[i]%byte(len(charsetForPKCE))]
	}
	return string(b)
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func NewMinecraftService(clientID, clientSecret string) *MinecraftService {
	return &MinecraftService{
		clientID:     clientID,
		clientSecret: clientSecret,
		client:       &http.Client{Timeout: msHTTPTimeout},
	}
}

func (s *MinecraftService) sessionFilePath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "GravityCone", "minecraft_session.json")
}

func (s *MinecraftService) saveSession() {
	path := s.sessionFilePath()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	data := minecraftSession{
		MSAccessToken:  s.msAccessToken,
		MSRefreshToken: s.msRefreshToken,
		User:           s.User,
	}
	b, _ := json.Marshal(data)
	_ = os.WriteFile(path, b, 0600)
}

func (s *MinecraftService) loadSession() {
	path := s.sessionFilePath()
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var data minecraftSession
	if json.Unmarshal(b, &data) != nil {
		return
	}
	if data.MSAccessToken != "" && data.User != nil {
		s.msAccessToken = data.MSAccessToken
		s.msRefreshToken = data.MSRefreshToken
		s.User = data.User
	}
}

func (s *MinecraftService) clearState() {
	s.msAccessToken = ""
	s.msRefreshToken = ""
	s.User = nil
	_ = os.Remove(s.sessionFilePath())
}

func (s *MinecraftService) RestoreSession() error {
	s.loadSession()
	if s.msAccessToken == "" || s.User == nil {
		return nil
	}
	if _, err := s.fetchMcProfile(s.User.AccessToken); err == nil {
		s.saveSession()
		return nil
	}
	if err := s.refreshMsToken(); err != nil {
		s.clearState()
		return nil
	}
	mcToken, err := s.runTokenChain()
	if err != nil {
		s.clearState()
		return nil
	}
	user, err := s.fetchMcProfile(mcToken)
	if err != nil {
		s.clearState()
		return nil
	}
	user.AccessToken = mcToken
	s.User = user
	s.saveSession()
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

	codeVerifier := generateCodeVerifier()
	codeChallenge := generateCodeChallenge(codeVerifier)

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
		if err := s.exchangeCode(code, redirectURI, codeVerifier); err != nil {
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
		s.saveSession()
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
	s.clearState()
}

func (s *MinecraftService) postTokenForm(data url.Values) (*msTokenResponse, error) {
	req, _ := http.NewRequest("POST", msTokenURL, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var tokenResp msTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("invalid MS token response: %s", string(body))
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("MS OAuth error: %s - %s", tokenResp.Error, tokenResp.ErrorDesc)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("empty MS access token in response")
	}
	return &tokenResp, nil
}

func (s *MinecraftService) exchangeCode(code, redirectURI, codeVerifier string) error {
	data := url.Values{}
	data.Set("client_id", s.clientID)
	data.Set("code", code)
	data.Set("grant_type", "authorization_code")
	data.Set("redirect_uri", redirectURI)
	data.Set("client_secret", s.clientSecret)
	data.Set("scope", msScopes)
	data.Set("code_verifier", codeVerifier)

	tokenResp, err := s.postTokenForm(data)
	if err != nil {
		return err
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

	tokenResp, err := s.postTokenForm(data)
	if err != nil {
		return err
	}
	s.msAccessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		s.msRefreshToken = tokenResp.RefreshToken
	}
	return nil
}

func (s *MinecraftService) postJSON(endpoint string, reqBody any, resp any) error {
	encoded, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if strings.HasPrefix(endpoint, "https://user.auth.xboxlive.com") || strings.HasPrefix(endpoint, "https://xsts.auth.xboxlive.com") {
		req.Header.Set("x-xbl-contract-version", "1")
	}

	httpResp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return err
	}

	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s: %s", httpResp.StatusCode, endpoint, string(body))
	}

	if err := json.Unmarshal(body, resp); err != nil {
		return fmt.Errorf("invalid response from %s: %s", endpoint, string(body))
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
		return "", fmt.Errorf("Xsts exchange failed: %w", err)
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

	var xblResp xblAuthResponse
	if err := s.postJSON(xblAuthURL, &reqBody, &xblResp); err != nil {
		return "", err
	}
	if xblResp.Token == "" {
		return "", fmt.Errorf("empty XBL token in response")
	}
	return xblResp.Token, nil
}

func (s *MinecraftService) exchangeXblForXsts(xblToken string) (string, string, error) {
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

	var xstsResp xstsAuthResponse
	if err := s.postJSON(xstsAuthURL, &reqBody, &xstsResp); err != nil {
		return "", "", err
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
		IdentityToken       string `json:"identityToken"`
		EnsureLegacyEnabled bool   `json:"ensureLegacyEnabled"`
	}{
		IdentityToken:       fmt.Sprintf("XBL3.0 x=%s;%s", userhash, xstsToken),
		EnsureLegacyEnabled: true,
	}

	var mcResp mcAuthResponse
	if err := s.postJSON(mcAuthURL, &reqBody, &mcResp); err != nil {
		return "", err
	}
	if mcResp.AccessToken == "" {
		return "", fmt.Errorf("empty Minecraft access token in response")
	}
	return mcResp.AccessToken, nil
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
