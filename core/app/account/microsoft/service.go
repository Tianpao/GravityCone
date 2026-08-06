package microsoft

import "net/http"

type MinecraftUser struct {
	Username    string `json:"username"`
	UUID        string `json:"uuid"`
	AccessToken string `json:"access_token"`
	AvatarPNG   string `json:"avatar_png"`
}

type MinecraftService struct {
	clientID       string
	clientSecret   string
	msAccessToken  string
	msRefreshToken string
	client         *http.Client
	User           *MinecraftUser
}

func NewMinecraftService(clientID, clientSecret string) *MinecraftService {
	return &MinecraftService{
		clientID:     clientID,
		clientSecret: clientSecret,
		client:       &http.Client{Timeout: msHTTPTimeout},
	}
}

func (s *MinecraftService) GetCurrentUser() *MinecraftUser {
	return s.User
}

func (s *MinecraftService) Logout() {
	s.clearState()
}
