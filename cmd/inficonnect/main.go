package main

import (
	"fmt"
	"os"
	"slices"

	"github.com/inficonnect/inficonnect/app"
	"github.com/inficonnect/inficonnect/core/rpc"
)

func main() {
	if slices.Contains(os.Args[1:], "--core") {
		if err := rpc.RunCoreWorker(); err != nil {
			fmt.Fprintf(os.Stderr, "core worker exited with error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "inficonnect error: %v\n", err)
		os.Exit(1)
	}
}
