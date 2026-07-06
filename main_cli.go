//go:build cli

package main

import (
	"gravitycone/cli"
	"os"
	"strings"
)

func main() {
	peers := parsePeers(os.Args[1:])
	cli.Run(peers)
}

// parsePeers extracts peer addresses from --peers or -p flags.
// Supports all of:
//
//	--peers addr1 --peers addr2
//	--peers=addr1 --peers=addr2
//	-p addr1 -p addr2
//	-p=addr1 -p=addr2
//	--peers addr1,addr2  (comma-separated)
//	-p addr1,addr2
func parsePeers(args []string) []string {
	var peers []string
	for i := 0; i < len(args); i++ {
		if val, ok := matchFlag(args, &i, "--peers", "-p"); ok {
			for _, p := range strings.Split(val, ",") {
				if p != "" {
					peers = append(peers, p)
				}
			}
		}
	}
	return peers
}

// matchFlag checks if args[i] matches the given long or short flag name.
// Returns the value and true if matched, advances i if the value is the next arg.
func matchFlag(args []string, i *int, long, short string) (string, bool) {
	arg := args[*i]
	// --peers=addr or -p=addr
	if v, ok := splitEqual(arg, long); ok {
		return v, true
	}
	if v, ok := splitEqual(arg, short); ok {
		return v, true
	}
	// --peers addr or -p addr
	if arg == long || arg == short {
		if *i+1 < len(args) {
			*i++
			return args[*i], true
		}
		return "", false
	}
	return "", false
}

// splitEqual splits "--flag=value" into (value, true).
func splitEqual(arg, flag string) (string, bool) {
	prefix := flag + "="
	if len(arg) > len(prefix) && arg[:len(prefix)] == prefix {
		return arg[len(prefix):], true
	}
	return "", false
}
