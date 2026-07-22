package paperconnect

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

const PCHostVIP = "10.144.144.1"
const PCMemberSubnet = "10.144.144.0/24"
const PCRakNetPortStr = "19133" // RakNet port as string for ACL TOML rules
const PCRakNetPort = 19133      // RakNet port as uint16 for service code

// EasyTier ACL action and protocol enums
const (
	ACLActionAllow = 1
	ACLActionDrop  = 2

	ACLProtoTCP = 1
	ACLProtoUDP = 2

	ACLChainInbound  = 1
	ACLChainOutbound = 2
)

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

func newPCRule(name string, priority int, protocol int, ports []string, srcIPs []string, dstIPs []string) PCRule {
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
		AppProtocols:      []int{},
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

// BuildPaperConnectACL builds the ACL configuration for PaperConnect.
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

	// Allow UDP RakNet to host
	rules = append(rules, newPCRule(
		"allow_udp_raknet_to_host", 5000,
		ACLProtoUDP, []string{PCRakNetPortStr},
		[]string{}, []string{hostVIP},
	))

	// Allow TCP control to host
	if hostProtocolPort != nil {
		rules = append(rules, newPCRule(
			"allow_tcp_control_to_host", 4500,
			ACLProtoTCP, []string{fmt.Sprintf("%d", *hostProtocolPort)},
			[]string{}, []string{hostVIP},
		))
	}

	return rules
}

func buildHostOutboundRules(hostVIP string) []PCRule {
	var rules []PCRule

	// Allow UDP RakNet from host to members
	rules = append(rules, newPCRule(
		"allow_udp_raknet_from_host", 5000,
		ACLProtoUDP, []string{PCRakNetPortStr},
		[]string{hostVIP}, []string{PCMemberSubnet},
	))

	// Allow TCP from host to members
	rules = append(rules, newPCRule(
		"allow_tcp_from_host", 4500,
		ACLProtoTCP, []string{"0-65535"},
		[]string{hostVIP}, []string{PCMemberSubnet},
	))

	return rules
}

func buildClientInboundRules(hostVIP string) []PCRule {
	var rules []PCRule

	// Allow UDP RakNet from host
	rules = append(rules, newPCRule(
		"allow_udp_raknet_from_host", 5000,
		ACLProtoUDP, []string{PCRakNetPortStr},
		[]string{hostVIP}, []string{PCMemberSubnet},
	))

	// Allow TCP from host
	rules = append(rules, newPCRule(
		"allow_tcp_from_host", 4500,
		ACLProtoTCP, []string{"0-65535"},
		[]string{hostVIP}, []string{PCMemberSubnet},
	))

	return rules
}

func buildClientOutboundRules(hostVIP string) []PCRule {
	var rules []PCRule

	// Allow UDP RakNet to host
	rules = append(rules, newPCRule(
		"allow_udp_raknet_to_host", 5000,
		ACLProtoUDP, []string{PCRakNetPortStr},
		[]string{}, []string{hostVIP},
	))

	// Allow TCP control to host
	rules = append(rules, newPCRule(
		"allow_tcp_control_to_host", 4500,
		ACLProtoTCP, []string{"0-65535"},
		[]string{}, []string{hostVIP},
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
