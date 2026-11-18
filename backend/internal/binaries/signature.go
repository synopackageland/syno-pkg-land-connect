package binaries

import (
	"context"
	"crypto/ed25519"
	"embed"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"golang.org/x/crypto/blake2s"
	"io"
	"os"
)

// Pinned from tailscale v1.102.4; see docs/binary-management.md.
//
//go:embed roots/*.pem
var rootFiles embed.FS
var rootPEM = func() []byte {
	var b []byte
	files, _ := rootFiles.ReadDir("roots")
	for _, f := range files {
		raw, _ := rootFiles.ReadFile("roots/" + f.Name())
		b = append(b, raw...)
	}
	return b
}()

func verifyBundle(bundle, msg, sig []byte, kind string) bool {
	for len(bundle) > 0 {
		block, rest := pem.Decode(bundle)
		if block == nil {
			return false
		}
		bundle = rest
		if block.Type == kind && len(block.Bytes) == ed25519.PublicKeySize && ed25519.Verify(ed25519.PublicKey(block.Bytes), msg, sig) {
			return true
		}
	}
	return false
}
func (s *OfficialSource) verifyTailscale(ctx context.Context, addr, path string) error {
	keys, e := s.get(ctx, "https://pkgs.tailscale.com/distsign.pub", 1<<20)
	if e != nil {
		return e
	}
	sig, e := s.get(ctx, "https://pkgs.tailscale.com/distsign.pub.sig", 64)
	if e != nil {
		return e
	}
	if !verifyBundle(s.roots, keys, sig, "ROOT PUBLIC KEY") {
		return errors.New("signing key verification failed")
	}
	sig, e = s.get(ctx, addr+".sig", 64)
	if e != nil {
		return e
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	h, _ := blake2s.New256(nil)
	n, e := io.Copy(h, f)
	if e != nil {
		return e
	}
	msg := binary.LittleEndian.AppendUint64(h.Sum(nil), uint64(n))
	if !verifyBundle(keys, msg, sig, "SIGNING PUBLIC KEY") {
		return errors.New("binary signature verification failed")
	}
	return nil
}
