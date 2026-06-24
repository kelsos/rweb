package main

import (
	"fmt"
	"os"

	"github.com/kelsos/rweb/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "rweb:", err)
		os.Exit(1)
	}
}
