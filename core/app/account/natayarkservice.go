package account

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	natayarkAuthorizeURL = "https://account.naids.com/oauth2/authorize"
	natayarkTokenURL     = "https://account.naids.com/api/oauth2/token"
	natayarkUserDataURL  = "https://account.naids.com/api/api/user/data"
)

type NatayarkUser struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	Realname  bool   `json:"realname"`
	Status    int    `json:"status"`
	LastLogin string `json:"last_login"`
	RegTime   string `json:"regtime"`
}

type natayarkAPIResponse struct {
	Code int          `json:"code"`
	Msg  string       `json:"msg"`
	Data NatayarkUser `json:"data"`
	Flag bool         `json:"flag"`
}

type NatayarkService struct {
	clientID     string
	clientSecret string
	accessToken  string
	User         *NatayarkUser
}

func NewNatayarkService(clientID, clientSecret string) *NatayarkService {
	return &NatayarkService{
		clientID:     clientID,
		clientSecret: clientSecret,
	}
}

func (s *NatayarkService) sessionFilePath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "GravityCone", "natayark_session.json")
}

type natayarkSession struct {
	AccessToken string        `json:"access_token"`
	User        *NatayarkUser `json:"user"`
}

func (s *NatayarkService) saveSession() {
	path := s.sessionFilePath()
	os.MkdirAll(filepath.Dir(path), 0700)
	data := natayarkSession{AccessToken: s.accessToken, User: s.User}
	b, _ := json.Marshal(data)
	os.WriteFile(path, b, 0600)
}

func (s *NatayarkService) loadSession() error {
	path := s.sessionFilePath()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var data natayarkSession
	if err := json.Unmarshal(b, &data); err != nil {
		return err
	}
	if data.AccessToken != "" && data.User != nil {
		s.accessToken = data.AccessToken
		s.User = data.User
	}
	return nil
}

// RestoreSession is called at startup to reload the persisted session.
func (s *NatayarkService) RestoreSession() error {
	if err := s.loadSession(); err != nil {
		return err
	}
	// Validate the saved token by fetching fresh user data.
	if s.accessToken != "" {
		user, err := s.fetchUserData(s.accessToken)
		if err != nil {
			// Token is invalid/expired — clear session.
			s.accessToken = ""
			s.User = nil
			s.clearSession()
			return nil
		}
		s.User = user
		s.saveSession()
	}
	return nil
}

func (s *NatayarkService) clearSession() {
	os.Remove(s.sessionFilePath())
}

func (s *NatayarkService) StartLogin() (*NatayarkUser, error) {
	if s.clientID == "" || s.clientSecret == "" {
		return nil, fmt.Errorf("Naids OAuth2 credentials not configured")
	}

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, fmt.Errorf("failed to start callback server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

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

	authURL := fmt.Sprintf("%s?response_type=code&redirect_uri=%s&client_id=%s",
		natayarkAuthorizeURL, redirectURI, s.clientID)

	if err := openBrowser(authURL); err != nil {
		srv.Shutdown(context.Background())
		return nil, fmt.Errorf("failed to open browser: %w", err)
	}

	select {
	case code := <-resultCh:
		srv.Shutdown(context.Background())
		token, err := s.exchangeCode(code, redirectURI)
		if err != nil {
			return nil, fmt.Errorf("token exchange failed: %w", err)
		}
		s.accessToken = token
		user, err := s.fetchUserData(token)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch user data: %w", err)
		}
		s.User = user
		s.saveSession()
		return user, nil
	case <-time.After(5 * time.Minute):
		srv.Shutdown(context.Background())
		return nil, fmt.Errorf("login timed out after 5 minutes")
	}
}

func (s *NatayarkService) GetCurrentUser() *NatayarkUser {
	return s.User
}

func (s *NatayarkService) Logout() {
	s.accessToken = ""
	s.User = nil
	s.clearSession()
}

func (s *NatayarkService) exchangeCode(code string, redirectURI string) (string, error) {
	hashedSecret, err := bcrypt.GenerateFromPassword([]byte(s.clientSecret), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash client_secret: %w", err)
	}
	hashed := string(hashedSecret)
	// PHP bcrypt uses $2y$ prefix; Go uses $2a$
	if strings.HasPrefix(hashed, "$2a$") {
		hashed = "$2y$" + hashed[4:]
	}

	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("client_id", s.clientID)
	data.Set("client_secret", hashed)
	data.Set("redirect_uri", redirectURI)

	req, err := http.NewRequest("POST", natayarkTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("invalid token response: %s", string(body))
	}
	if tokenResp.Error != "" {
		return "", fmt.Errorf("token error: %s", tokenResp.Error)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("empty access token in response: %s", string(body))
	}
	return tokenResp.AccessToken, nil
}

func (s *NatayarkService) fetchUserData(token string) (*NatayarkUser, error) {
	req, err := http.NewRequest("GET", natayarkUserDataURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp natayarkAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("invalid user data response: %s", string(body))
	}
	if apiResp.Code != 200 || !apiResp.Flag {
		return nil, fmt.Errorf("API error: %s (code %d)", apiResp.Msg, apiResp.Code)
	}
	return &apiResp.Data, nil
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
