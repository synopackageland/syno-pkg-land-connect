package apps

import (
	"encoding/json"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// ResolveSubject trusts DSM's root-owned package privilege declaration and
// account database, and authenticates the actual peer's nonzero numeric UID.
func VerifyIngressSubject(root, pkg string, uid int) (IngressSubject, error) {
	if !ingressPackageName.MatchString(pkg) || pkg == "SynoPkgConnect" || uid <= 0 || uid == os.Geteuid() {
		return IngressSubject{}, ErrInvalid
	}
	account, err := user.Lookup(pkg)
	if err != nil || account.Uid != strconv.Itoa(uid) {
		return IngressSubject{}, ErrInvalid
	}
	canonical, err := user.LookupId(strconv.Itoa(uid))
	if err != nil || canonical.Username != pkg {
		return IngressSubject{}, ErrInvalid
	}
	path := filepath.Join(root, pkg, "conf", "privilege")
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return IngressSubject{}, ErrInvalid
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0022 != 0 || st.Sys().(*syscall.Stat_t).Uid != 0 || st.Size() > 65536 {
		return IngressSubject{}, ErrInvalid
	}
	var declaration struct {
		Defaults struct {
			RunAs string `json:"run-as"`
		} `json:"defaults"`
	}
	if json.NewDecoder(io.LimitReader(f, 65537)).Decode(&declaration) != nil || declaration.Defaults.RunAs != "package" {
		return IngressSubject{}, ErrInvalid
	}
	return IngressSubject{Package: pkg, UID: uid}, nil
}
