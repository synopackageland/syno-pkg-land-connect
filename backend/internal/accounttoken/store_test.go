package accounttoken

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSinglePrivateCredentialAndAccountPin(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), "private", "account.json")}
	c := Credential{Account: "account-id", Token: "private-fixture"}
	if e := s.Save(c); e != nil {
		t.Fatal(e)
	}
	got, e := s.Load()
	if e != nil || got != c {
		t.Fatal(e)
	}
	if e = s.Save(Credential{Account: "other-account", Token: "new"}); e == nil {
		t.Fatal("account changed")
	}
	st, e := os.Stat(s.Path)
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal(e)
	}
	if e = s.Remove(); e != nil {
		t.Fatal(e)
	}
}
