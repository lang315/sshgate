package main

import (
	"fmt"
	"os"
)

func route(args []string) (mode string, rest []string) {
	if len(args) > 0 && args[0] == "web" {
		return "web", args[1:]
	}
	return "mcp", args
}

func main() {
	mode, rest := route(os.Args[1:])
	switch mode {
	case "web":
		fmt.Fprintln(os.Stderr, "web mode not yet wired", rest)
	default:
		fmt.Fprintln(os.Stderr, "mcp mode not yet wired", rest)
	}
}
