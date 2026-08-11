//go:build cli

package main

import (
	"gravitycone/cli"
	"gravitycone/core/utils"
	"os"
	"strings"
)

func main() {
	peers, vendorPrefix, motd, easytierDir := parseArgs(os.Args[1:])
	cli.Run(peers, vendorPrefix, motd, easytierDir)
}

// parseArgs extracts --peers/-p, --vendor/-v, --motd/-m and --easytier-dir/-e flags from command-line arguments.
// Supports space-separated values only:
//
//	--peers addr1 --peers addr2
//	-p addr1 -p addr2
//	--peers addr1,addr2  (comma-separated)
//	--vendor MyPrefix
//	-v MyPrefix
//	--motd "Custom MOTD"
//	-m "Custom MOTD"
//	--easytier-dir /path/to/easytier
//	-e /path/to/easytier
func parseArgs(args []string) (peers []string, vendorPrefix string, motd string, easytierDir string) {
	for i := 0; i < len(args); i++ {
		if val, ok := matchFlag(args, &i, "--peers", "-p"); ok {
			for _, p := range strings.Split(val, ",") {
				if p != "" {
					peers = append(peers, p)
				}
			}
		} else if val, ok := matchFlag(args, &i, "--vendor", "-v"); ok {
			vendorPrefix = val
		} else if val, ok := matchFlag(args, &i, "--motd", "-m"); ok {
			// Windows 上部分启动器用 GBK 拼装命令行参数，导致中文 MOTD 变成方块乱码，
			// 这里做一次反向还原（对正常 UTF-8 输入无副作用）。
			motd = utils.FixCommandLineMojibake(val)
		} else if val, ok := matchFlag(args, &i, "--easytier-dir", "-e"); ok {
			easytierDir = val
		}
	}
	return
}

// matchFlag checks if args[i] matches the given long or short flag name.
// Returns the value and true if matched, advances i to skip the value arg.
func matchFlag(args []string, i *int, long, short string) (string, bool) {
	arg := args[*i]
	if arg == long || arg == short {
		if *i+1 < len(args) {
			*i++
			return args[*i], true
		}
		return "", false
	}
	return "", false
}
