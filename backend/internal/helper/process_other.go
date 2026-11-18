//go:build !linux

package helper

func RunLinux(Inventory, string) error { return errTrust }
