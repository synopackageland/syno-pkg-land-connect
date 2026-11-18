package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func executableDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != 0 || st.Mode().Perm()&0022 != 0 || !st.Mode().IsRegular() || st.Size() > 64<<20 {
		return "", errTrust
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func inspectSupervisor(pid int, i Inventory) (Process, error) {
	base := fmt.Sprintf("/proc/%d/", pid)
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return Process{}, err
	}
	stat, err := os.ReadFile(base + "stat")
	if err != nil {
		return Process{}, err
	}
	end := strings.LastIndex(string(stat), ") ")
	if end < 0 {
		return Process{}, errTrust
	}
	fields := strings.Fields(string(stat)[end+2:])
	if len(fields) < 20 {
		return Process{}, errTrust
	}
	status, err := os.ReadFile(base + "status")
	if err != nil {
		return Process{}, err
	}
	root := false
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			u := strings.Fields(line)
			root = len(u) == 5 && u[1] == "0" && u[2] == "0" && u[3] == "0" && u[4] == "0"
		}
	}
	if !root {
		return Process{}, errTrust
	}
	cmd, err := os.ReadFile(base + "cmdline")
	if err != nil {
		return Process{}, err
	}
	wantExe := "/var/lib/synopkgland-" + i.Package + "/payload-" + i.PayloadDigest
	if string(cmd) != wantExe+"\x00supervise\x00" {
		return Process{}, errTrust
	}
	st, err := os.Stat(base + "exe")
	if err != nil {
		return Process{}, err
	}
	s := st.Sys().(*syscall.Stat_t)
	digest, err := executableDigest(base + "exe")
	if err != nil || digest != i.PayloadDigest {
		return Process{}, errTrust
	}
	return Process{Inventory: i, Mode: "supervised", PID: pid, Boot: strings.TrimSpace(string(b)), Start: fields[19], Device: uint64(s.Dev), Inode: s.Ino}, nil
}

type LinuxProcesses struct {
	Client
	Binary string
}

func (p LinuxProcesses) Start(i Inventory) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if i != p.Expected {
		return errTrust
	}
	if err := protected(p.RunDir, 0, true); err != nil {
		return err
	}
	cmd := exec.Command(p.Binary, "supervise")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	if waitHealthy(ctx, func(ctx context.Context) bool {
		state, _ := p.probe(ctx, filepath.Join(p.RunDir, "helper.sock"))
		return state == "ready"
	}) == nil {
		return nil
	}
	return errors.New("helper start timeout; residual preserved")
}

// RunLinux runs a fixed control action or the health-only server. Its caller
// must be the hash-verified, root-owned copy, never the SPK staging executable.
func RunLinux(base Inventory, action string) error {
	if os.Geteuid() != 0 {
		return errTrust
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	digest, err := executableDigest(exe)
	if err != nil {
		return err
	}
	base.PayloadDigest = digest
	if !base.Valid() || exe != "/var/lib/synopkgland-"+base.Package+"/payload-"+digest {
		return errTrust
	}
	c := NewClient(base)
	if err := protected(c.StateDir, 0, true); err != nil {
		return err
	}
	if err := protected(c.RunDir, 0, true); err != nil {
		return err
	}
	switch action {
	case "supervise":
		return superviseLinux(c, exe)
	case "serve-child":
		parent, err := strconv.Atoi(os.Getenv("SYNOPKGLAND_PARENT"))
		if err != nil {
			return errTrust
		}
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := armParentDeath(parent, setParentDeath, os.Getppid); err != nil {
			return err
		}
		if _, err := inspectSupervisor(parent, base); err != nil {
			return err
		}
		return serveLinux(c)
	}
	return (Controller{Client: c, Processes: LinuxProcesses{Client: c, Binary: exe}}).Control(context.Background(), action)
}
