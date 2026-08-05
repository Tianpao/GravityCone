package microsoft

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gravitycone/core/app/account"
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

var charsetForPKCE = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"

func generateCodeVerifier() string {
	b := make([]byte, 32)
	rand.Read(b)
	for i := range b {
		b[i] = charsetForPKCE[b[i]%byte(len(charsetForPKCE))]
	}
	return string(b)
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
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

	if err := account.OpenBrowser(authURL); err != nil {
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
