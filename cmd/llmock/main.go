// Command llmock is a mock LLM server for load-testing LLM infrastructure.
package main

import (
	"fmt"
	"os"
	"strings"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		args = append([]string{"serve"}, args...)
	}
	switch args[0] {
	case "serve":
		os.Exit(runServe(args[1:]))
	case "bench":
		os.Exit(runBench(args[1:]))
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "usage: llmock [serve] [flags] | bench [flags] | version\nunknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}
