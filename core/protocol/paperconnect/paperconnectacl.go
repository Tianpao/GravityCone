package paperconnect

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

const PCHostVIP = "10.144.144.1"
const PCMemberSubnet = "10.144.144.0/24"
const PCBroadcastIP1 = "10.144.144.255"
const PCBroadcastIP2 = "255.255.255.255"

// EasyTier ACL action and protocol enums
const (
	ACLActionAllow = 1
	ACLActionDrop  = 2

	ACLProtoTCP = 1
	ACLProtoUDP = 2

	ACLChainInbound  = 1
	ACLChainOutbound = 2
)

// Bedrock app protocol IDs
const (
	AppProtoRakNet     = 10
	AppProtoWebRtc     = 20
	AppProtoWebRtcStun = 21
	AppProtoWebRtcDtls = 22
	AppProtoWebRtcRtp  = 23
)

// Bedrock discovery broadcast ports
var pcDiscoveryBroadcastPorts = []string{"7551", "19132", "19133"}
var pcPermissiveUnicastPorts = []string{"7551"}
var pcBedrockUdpAppProtocols = []int{AppProtoRakNet, AppProtoWebRtc, AppProtoWebRtcStun, AppProtoWebRtcDtls, AppProtoWebRtcRtp}

// ACL TOML structures matching EasyTier's expected format

type PCACLConfig struct {
	ACL PCACLSection `toml:"acl"`
}

type PCACLSection struct {
	V1 PCACLV1 `toml:"acl_v1"`
}

type PCACLV1 struct {
	Chains []PCChain      `toml:"chains"`
	Group  PCGroupSection `toml:"group"`
}

type PCChain struct {
	Name          string   `toml:"name"`
	ChainType     int      `toml:"chain_type"`
	Description   string   `toml:"description"`
	Enabled       bool     `toml:"enabled"`
	DefaultAction int      `toml:"default_action"`
	Rules         []PCRule `toml:"rules"`
}

type PCRule struct {
	Name              string   `toml:"name"`
	Description       string   `toml:"description"`
	Priority          int      `toml:"priority"`
	Enabled           bool     `toml:"enabled"`
	Protocol          int      `toml:"protocol"`
	Ports             []string `toml:"ports"`
	SourceIPs         []string `toml:"source_ips"`
	DestinationIPs    []string `toml:"destination_ips"`
	SourcePorts       []string `toml:"source_ports"`
	AppProtocols      []int    `toml:"app_protocols"`
	PayloadPrefixHex  []string `toml:"payload_prefix_hex"`
	PayloadMinLen     *int     `toml:"payload_min_len"`
	PayloadMaxLen     *int     `toml:"payload_max_len"`
	DstIsBroadcast    *bool    `toml:"dst_is_broadcast"`
	DstIsMulticast    *bool    `toml:"dst_is_multicast"`
	Action            int      `toml:"action"`
	RateLimit         int      `toml:"rate_limit"`
	BurstLimit        int      `toml:"burst_limit"`
	Stateful          bool     `toml:"stateful"`
	SourceGroups      []string `toml:"source_groups"`
	DestinationGroups []string `toml:"destination_groups"`
}

type PCGroupSection struct {
	Declares []string `toml:"declares"`
	Members  []string `toml:"members"`
}

func newPCRule(name string, priority int, protocol int, ports []string, srcIPs []string, dstIPs []string, appProtos []int) PCRule {
	return PCRule{
		Name:              name,
		Description:       "",
		Priority:          priority,
		Enabled:           true,
		Protocol:          protocol,
		Ports:             ports,
		SourceIPs:         srcIPs,
		DestinationIPs:    dstIPs,
		SourcePorts:       []string{},
		AppProtocols:      appProtos,
		PayloadPrefixHex:  []string{},
		PayloadMinLen:     nil,
		PayloadMaxLen:     nil,
		DstIsBroadcast:    nil,
		DstIsMulticast:    nil,
		Action:            ACLActionAllow,
		RateLimit:         0,
		BurstLimit:        0,
		Stateful:          false,
		SourceGroups:      []string{},
		DestinationGroups: []string{},
	}
}

// BuildPaperConnectACL builds the full ACL configuration for PaperConnect.
// isHost=true generates host rules, isHost=false generates client rules.
// hostProtocolPort is the TCP control protocol port (only used for host rules).
func BuildPaperConnectACL(isHost bool, hostVIP string, hostProtocolPort *uint16) *PCACLConfig {
	var inboundRules, outboundRules []PCRule

	if isHost {
		inboundRules = buildHostInboundRules(hostVIP, hostProtocolPort)
		outboundRules = buildHostOutboundRules(hostVIP)
	} else {
		inboundRules = buildClientInboundRules(hostVIP)
		outboundRules = buildClientOutboundRules(hostVIP)
	}

	return &PCACLConfig{
		ACL: PCACLSection{
			V1: PCACLV1{
				Chains: []PCChain{
					{
						Name:          "paperconnect_inbound",
						ChainType:     ACLChainInbound,
						Description:   "Auto-generated PaperConnect inbound ACL",
						Enabled:       true,
						DefaultAction: ACLActionDrop,
						Rules:         inboundRules,
					},
					{
						Name:          "paperconnect_outbound",
						ChainType:     ACLChainOutbound,
						Description:   "Auto-generated PaperConnect outbound ACL",
						Enabled:       true,
						DefaultAction: ACLActionDrop,
						Rules:         outboundRules,
					},
				},
				Group: PCGroupSection{
					Declares: []string{},
					Members:  []string{},
				},
			},
		},
	}
}

func buildHostInboundRules(hostVIP string, hostProtocolPort *uint16) []PCRule {
	var rules []PCRule

	// Allow UDP to host on permissive unicast ports (7551)
	rules = append(rules, newPCRule(
		"allow_udp_to_host_unicast_permissive", 5200,
		ACLProtoUDP, pcPermissiveUnicastPorts,
		[]string{}, []string{hostVIP},
		[]int{},
	))

	// Allow UDP discovery broadcast in (7551, 19132, 19133 -> broadcast IPs)
	rules = append(rules, newPCRule(
		"allow_udp_discovery_broadcast_in", 5000,
		ACLProtoUDP, pcDiscoveryBroadcastPorts,
		[]string{}, []string{PCBroadcastIP1, PCBroadcastIP2},
		[]int{},
	))

	// Allow UDP to host on all ports with Bedrock app protocols
	rules = append(rules, newPCRule(
		"allow_udp_to_host", 4500,
		ACLProtoUDP, []string{"0-65535"},
		[]string{}, []string{hostVIP},
		pcBedrockUdpAppProtocols,
	))

	// Allow TCP to host
	if hostProtocolPort != nil {
		rules = append(rules, newPCRule(
			"allow_tcp_to_host_protocol_port", 4000,
			ACLProtoTCP, []string{fmt.Sprintf("%d", *hostProtocolPort)},
			[]string{}, []string{hostVIP},
			[]int{},
		))
	} else {
		rules = append(rules, newPCRule(
			"allow_tcp_to_host", 3500,
			ACLProtoTCP, []string{"0-65535"},
			[]string{}, []string{hostVIP},
			[]int{},
		))
	}

	return rules
}

func buildHostOutboundRules(hostVIP string) []PCRule {
	var rules []PCRule

	// Allow UDP from host to members on permissive unicast ports
	rules = append(rules, newPCRule(
		"allow_udp_from_host_to_members_unicast_permissive", 5200,
		ACLProtoUDP, pcPermissiveUnicastPorts,
		[]string{hostVIP}, []string{PCMemberSubnet},
		[]int{},
	))

	// Allow UDP from host to members with Bedrock app protocols
	rules = append(rules, newPCRule(
		"allow_udp_from_host_to_members", 5000,
		ACLProtoUDP, []string{"0-65535"},
		[]string{hostVIP}, []string{PCMemberSubnet},
		pcBedrockUdpAppProtocols,
	))

	// Allow TCP from host to members
	rules = append(rules, newPCRule(
		"allow_tcp_from_host_to_members", 4800,
		ACLProtoTCP, []string{"0-65535"},
		[]string{hostVIP}, []string{PCMemberSubnet},
		[]int{},
	))

	// Allow UDP discovery broadcast out
	rules = append(rules, newPCRule(
		"allow_udp_discovery_broadcast_out", 4500,
		ACLProtoUDP, pcDiscoveryBroadcastPorts,
		[]string{hostVIP}, []string{PCBroadcastIP1, PCBroadcastIP2},
		[]int{},
	))

	return rules
}

func buildClientInboundRules(hostVIP string) []PCRule {
	var rules []PCRule

	// Allow UDP from host on permissive unicast ports
	rules = append(rules, newPCRule(
		"allow_udp_from_host_unicast_permissive", 5200,
		ACLProtoUDP, pcPermissiveUnicastPorts,
		[]string{hostVIP}, []string{PCMemberSubnet},
		[]int{},
	))

	// Allow UDP from host with Bedrock app protocols
	rules = append(rules, newPCRule(
		"allow_udp_from_host", 5000,
		ACLProtoUDP, []string{"0-65535"},
		[]string{hostVIP}, []string{PCMemberSubnet},
		pcBedrockUdpAppProtocols,
	))

	// Allow TCP from host
	rules = append(rules, newPCRule(
		"allow_tcp_from_host", 4500,
		ACLProtoTCP, []string{"0-65535"},
		[]string{hostVIP}, []string{PCMemberSubnet},
		[]int{},
	))

	return rules
}

func buildClientOutboundRules(hostVIP string) []PCRule {
	var rules []PCRule

	// Allow UDP to host on permissive unicast ports
	rules = append(rules, newPCRule(
		"allow_udp_to_host_unicast_permissive", 5200,
		ACLProtoUDP, pcPermissiveUnicastPorts,
		[]string{}, []string{hostVIP},
		[]int{},
	))

	// Allow UDP to host with Bedrock app protocols
	rules = append(rules, newPCRule(
		"allow_udp_to_host", 5000,
		ACLProtoUDP, []string{"0-65535"},
		[]string{}, []string{hostVIP},
		pcBedrockUdpAppProtocols,
	))

	// Allow TCP to host
	rules = append(rules, newPCRule(
		"allow_tcp_to_host", 4500,
		ACLProtoTCP, []string{"0-65535"},
		[]string{}, []string{hostVIP},
		[]int{},
	))

	// Allow UDP discovery broadcast out
	rules = append(rules, newPCRule(
		"allow_udp_discovery_broadcast_out", 4000,
		ACLProtoUDP, pcDiscoveryBroadcastPorts,
		[]string{}, []string{PCBroadcastIP1, PCBroadcastIP2},
		[]int{},
	))

	return rules
}

// WritePaperConnectACL writes the ACL config to a TOML file.
func WritePaperConnectACL(config *PCACLConfig, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create ACL config file: %w", err)
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	if err := enc.Encode(config); err != nil {
		return fmt.Errorf("failed to encode ACL config: %w", err)
	}

	return nil
}
