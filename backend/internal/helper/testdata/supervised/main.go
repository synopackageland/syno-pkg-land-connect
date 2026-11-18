// This executable is only for M1C_HELPER_FIXTURE in a disposable root container.
package main

import (
	"dsmapp/internal/helper"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	i := helper.Inventory{Package: "root", Version: "1.0.0-0001", Protocol: 1,
		RegistryDigest: strings.Repeat("b", 64)}
	if os.Args[1] == "serve-child" {
		go func() {
			for {
				if _, err := os.Stat(filepath.Join(helper.NewClient(i).RunDir, "fixture-exit")); err == nil {
					os.Exit(0)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}
	if helper.RunLinux(i, os.Args[1]) != nil {
		os.Exit(1)
	}
}
