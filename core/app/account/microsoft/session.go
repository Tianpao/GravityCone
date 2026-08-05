package microsoft

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type minecraftSession struct {
	MSAccessToken  string         `json:"ms_access_token"`
	MSRefreshToken string         `json:"ms_refresh_token"`
	User           *MinecraftUser `json:"user"`
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
