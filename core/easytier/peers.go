package easytier

import "sync"

// PeerConfig combines fixed protocol peers with configured and runtime peers.
type PeerConfig struct {
	mu         sync.RWMutex
	override   []string
	additional []string
	settings   *SettingsService
}

func (c *PeerConfig) SetSettingsService(settings *SettingsService) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settings = settings
}

func (c *PeerConfig) SetCLIOverride(peers []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.override = append([]string(nil), peers...)
}

func (c *PeerConfig) Add(peers []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.additional = append(c.additional, peers...)
}

func (c *PeerConfig) Resolve(builtin []string) []string {
	c.mu.RLock()
	override := append([]string(nil), c.override...)
	additional := append([]string(nil), c.additional...)
	settings := c.settings
	c.mu.RUnlock()

	if len(override) > 0 {
		return append(override, additional...)
	}

	peers := append([]string(nil), builtin...)
	if settings != nil {
		peers = append(peers, settings.GetCustomPeers()...)
	}
	return append(peers, additional...)
}
