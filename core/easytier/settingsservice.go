package easytier

import "sync"

type SettingsService struct {
	mu          sync.RWMutex
	customPeers []string
}

func (s *SettingsService) GetCustomPeers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.customPeers...)
}

func (s *SettingsService) SetCustomPeers(peers []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.customPeers = append([]string(nil), peers...)
}
