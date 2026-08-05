package scaffolding

import "gravitycone/core/easytier"

// ConfigureSettingsPeers provides GUI custom peers for future EasyTier starts.
func ConfigureSettingsPeers(s *ScaffoldingService, settingsSvc *easytier.SettingsService) {
	s.relay.SetSettingsService(settingsSvc)
	s.peerConfig.SetSettingsService(settingsSvc)
}

// ConfigureCLIPeers replaces the built-in peers for CLI starts.
func ConfigureCLIPeers(s *ScaffoldingService, peers []string) {
	s.peerConfig.SetCLIOverride(peers)
}

// EnableUptime 启用 Uptime 节点自动分发。仅 GUI 调用；CLI/FFI 不启用，
// 中继由启动器传入，不传时使用内置节点。
func EnableUptime(s *ScaffoldingService) {
	s.relay.EnableUptime()
}

// ConfigureExternalRelay sets the relay node provided by the caller
// (CLI/FFI mode): nodeID is embedded into the room code on the host side,
// and url is used directly as an EasyTier peer on both sides. Passing an
// empty url or a negative nodeID clears the override, reverting to the
// automatic uptime node fetch.
func ConfigureExternalRelay(s *ScaffoldingService, nodeID int, url string) {
	s.relay.SetExternal(nodeID, url)
}

func (s *ScaffoldingService) resolvePeers() []string {
	return s.peerConfig.Resolve(scaffoldingBuiltinPeers)
}

// AddPeers appends peer addresses for future EasyTier starts.
func (s *ScaffoldingService) AddPeers(addrs []string) {
	s.peerConfig.Add(addrs)
}
