package binaries

import (
	"debug/elf"
	"errors"
)

// executableChecker is implemented by sources that know which CPU the NAS
// runs, so a verified download built for another one never becomes current.
type executableChecker interface {
	CheckExecutable(path string) error
}

var executableFormats = map[string]struct {
	machine elf.Machine
	class   elf.Class
}{
	"amd64": {elf.EM_X86_64, elf.ELFCLASS64},
	"arm64": {elf.EM_AARCH64, elf.ELFCLASS64},
	"arm":   {elf.EM_ARM, elf.ELFCLASS32},
}

func (s *OfficialSource) CheckExecutable(path string) error {
	want, ok := executableFormats[s.arch]
	if !ok {
		return errors.New("unsupported architecture")
	}
	f, e := elf.Open(path)
	if e != nil {
		return errors.New("downloaded file is not a Linux executable")
	}
	defer f.Close()
	if f.Machine != want.machine || f.Class != want.class || f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
		return errors.New("downloaded executable is for another architecture")
	}
	return nil
}
