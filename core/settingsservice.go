package core

import "sync"

var defaultPublicPeers = []string{
	"https://etnode.zkitefly.eu.org/node1",
}

type SettingsService struct {
	mu           sync.RWMutex
	customPeers  []string
}

func (s *SettingsService) GetPublicPeers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.customPeers) > 0 {
		result := make([]string, len(defaultPublicPeers), len(defaultPublicPeers)+len(s.customPeers))
		copy(result, defaultPublicPeers)
		result = append(result, s.customPeers...)
		return result
	}
	return defaultPublicPeers
}

func (s *SettingsService) GetCustomPeers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.customPeers
}

func (s *SettingsService) SetCustomPeers(peers []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.customPeers = peers
	// Combine default + custom peers and apply to EasyTier
	combined := make([]string, len(defaultPublicPeers), len(defaultPublicPeers)+len(peers))
	copy(combined, defaultPublicPeers)
	combined = append(combined, peers...)
	SetPublicPeers(combined)
}
