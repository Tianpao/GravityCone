package easytier

import "sync"

type SettingsService struct {
	mu          sync.RWMutex
	customPeers []string
	p2pDisabled bool
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

// GetP2PDisabled 返回是否禁止 P2P 直连（强制走中继节点）。
func (s *SettingsService) GetP2PDisabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.p2pDisabled
}

// SetP2PDisabled 设置禁止 P2P 直连，仅 GUI 使用（CLI 不注入 SettingsService）。
func (s *SettingsService) SetP2PDisabled(disabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.p2pDisabled = disabled
}
