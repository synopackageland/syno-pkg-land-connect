// Built only when dsm-package.yaml explicitly opts into integration.
package main

import (
	"context"
	"dsmapp/internal/helper"
	"encoding/json"
	"fmt"
	"os"
)

var helperInventory string

func main() {
	var i helper.Inventory
	if len(os.Args) != 2 || json.Unmarshal([]byte(helperInventory), &i) != nil {
		os.Exit(2)
	}
	if os.Args[1] == "check-stopped" || os.Args[1] == "diagnose" {
		c, err := helper.LocalClient(i)
		if err != nil {
			os.Exit(1)
		}
		if os.Args[1] == "diagnose" {
			json.NewEncoder(os.Stdout).Encode(c.Status(context.Background()))
			return
		}
		if c.CheckStopped(context.Background()) != nil {
			os.Exit(1)
		}
		return
	}
	if err := helper.RunLinux(i, os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "helper lifecycle refused")
		os.Exit(1)
	}
	fmt.Println(`{"success":true}`)
}
