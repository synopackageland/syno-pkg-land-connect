// A short-lived wizard renderer, independent of the daemon and provider runtime.
package main

import (
	"dsmapp/internal/uninstallreminder"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

func main() {
	if len(os.Args) != 3 && len(os.Args) != 4 {
		os.Exit(1)
	}
	generic := len(os.Args) == 4 && os.Args[3] == "--generic"
	// Some DSM wizard contexts run as root. Re-exec as the package account
	// before reading any state; never grant the renderer root access to it.
	if os.Geteuid() == 0 && !generic {
		account, err := user.Lookup("SynoPkgConnect")
		if err != nil {
			os.Exit(1)
		}
		uid, e1 := strconv.ParseUint(account.Uid, 10, 32)
		gid, e2 := strconv.ParseUint(account.Gid, 10, 32)
		if e1 != nil || e2 != nil || uid == 0 {
			os.Exit(1)
		}
		bin, err := os.Executable()
		if err != nil {
			os.Exit(1)
		}
		cmd := exec.Command(bin, os.Args[1:]...)
		cmd.Env = []string{}
		cmd.Stdout = os.Stdout
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
		if cmd.Run() != nil {
			os.Exit(1)
		}
		return
	}
	base, err := os.ReadFile(os.Args[2])
	if err != nil {
		os.Exit(1)
	}
	dir := "/var/packages/SynoPkgConnect/var/connect"
	if generic {
		dir = ""
	}
	raw, err := uninstallreminder.Render(base, dir, os.Args[1])
	if err != nil {
		os.Exit(1)
	}
	if _, err = os.Stdout.Write(raw); err != nil {
		os.Exit(1)
	}
}
