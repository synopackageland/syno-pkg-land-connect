package transient

import (
	"errors"
	"syscall"
)

func checkTmpfs(path string) error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return err
	}
	if st.Type != 0x01021994 {
		return errors.New("runtime storage must be tmpfs")
	}
	return nil
}
