package binaries

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"runtime"
	"strings"
	"testing"
)

// executable is the smallest ELF header debug/elf accepts for goarch,
// followed by tag so fixtures stay distinguishable.
func executable(goarch, tag string) []byte {
	machine := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64, "arm": elf.EM_ARM}[goarch]
	class, size := elf.ELFCLASS64, 64
	if goarch == "arm" {
		class, size = elf.ELFCLASS32, 52
	}
	h := make([]byte, size)
	copy(h, elf.ELFMAG)
	h[elf.EI_CLASS], h[elf.EI_DATA], h[elf.EI_VERSION] = byte(class), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)
	binary.LittleEndian.PutUint16(h[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(h[18:], uint16(machine))
	binary.LittleEndian.PutUint32(h[20:], uint32(elf.EV_CURRENT))
	return append(h, tag...)
}

func hostExecutable(tag string) []byte { return executable(runtime.GOARCH, tag) }

func TestDownloadedExecutableMustMatchTheNASArchitecture(t *testing.T) {
	for _, x := range []struct {
		arch, built string
		ok          bool
	}{{"arm", "arm", true}, {"arm", "arm64", false}, {"arm64", "arm", false}, {"amd64", "arm64", false}, {"amd64", "amd64", true}} {
		t.Run(x.arch+"/"+x.built, func(t *testing.T) {
			m, server, b := fixture(t)
			m.source.(*OfficialSource).arch = x.arch
			*b = executable(x.built, "cloudflared")
			e := m.install(context.Background(), release(server.URL, "1.0.0", *b))
			if (e == nil) != x.ok {
				t.Fatal("install", e)
			}
			if s, _ := m.Status("cloudflared"); !x.ok && s.Current != "" {
				t.Fatal("wrong architecture became current", s)
			}
		})
	}
	m, server, b := fixture(t)
	*b = []byte("#!/bin/sh\n")
	if e := m.install(context.Background(), release(server.URL, "1.0.0", *b)); e == nil || !strings.Contains(e.Error(), "executable") {
		t.Fatal("non-ELF download accepted", e)
	}
}
