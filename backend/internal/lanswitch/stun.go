package lanswitch

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"hash/crc32"
	"net/netip"
)

// The subset of STUN (RFC 8489) an ICE-lite agent needs to answer a
// browser's connectivity check: a Binding request carrying USERNAME and
// MESSAGE-INTEGRITY, answered with XOR-MAPPED-ADDRESS, MESSAGE-INTEGRITY and
// FINGERPRINT.
const (
	magicCookie     = 0x2112A442
	headerLen       = 20
	bindingRequest  = 0x0001
	bindingSuccess  = 0x0101
	attrUsername    = 0x0006
	attrIntegrity   = 0x0008
	attrXorMapped   = 0x0020
	attrFingerprint = 0x8028
	fingerprintXor  = 0x5354554e
	maxPacket       = 1500
)

// binding is a parsed Binding request. integrity is the offset of the
// MESSAGE-INTEGRITY attribute, or -1 when there is none.
type binding struct {
	txid      [12]byte
	username  string
	integrity int
}

// parseBinding accepts a well-formed Binding request. A FINGERPRINT, when
// present, must be the last attribute and must match.
func parseBinding(b []byte) (binding, bool) {
	out := binding{integrity: -1}
	if len(b) < headerLen || len(b) > maxPacket || b[0]&0xc0 != 0 {
		return out, false
	}
	if binary.BigEndian.Uint16(b[0:2]) != bindingRequest || binary.BigEndian.Uint32(b[4:8]) != magicCookie {
		return out, false
	}
	n := int(binary.BigEndian.Uint16(b[2:4]))
	if n%4 != 0 || headerLen+n != len(b) {
		return out, false
	}
	copy(out.txid[:], b[8:20])
	for i := headerLen; i < len(b); {
		if i+4 > len(b) {
			return out, false
		}
		t := binary.BigEndian.Uint16(b[i : i+2])
		l := int(binary.BigEndian.Uint16(b[i+2 : i+4]))
		end := i + 4 + l
		if end > len(b) {
			return out, false
		}
		v := b[i+4 : end]
		switch {
		case t == attrFingerprint:
			if l != 4 || end != len(b) || binary.BigEndian.Uint32(v) != fingerprint(b[:i], len(b)) {
				return out, false
			}
		case out.integrity >= 0:
			// Only FINGERPRINT may follow MESSAGE-INTEGRITY.
			return out, false
		case t == attrUsername:
			if l == 0 || l > 513 {
				return out, false
			}
			out.username = string(v)
		case t == attrIntegrity:
			if l != sha1.Size {
				return out, false
			}
			out.integrity = i
		}
		i = end + (4-l%4)%4
	}
	return out, out.username != "" && out.integrity >= 0
}

// validIntegrity checks the request's MESSAGE-INTEGRITY with key: an
// HMAC-SHA1 over the message up to the attribute, with the header length
// counting through it.
func validIntegrity(b []byte, req binding, key []byte) bool {
	off := req.integrity
	if off < headerLen || off+4+sha1.Size > len(b) {
		return false
	}
	return hmac.Equal(integrity(b[:off], key), b[off+4:off+4+sha1.Size])
}

// integrity returns HMAC-SHA1(key, msg) with msg's length field set to
// include a MESSAGE-INTEGRITY attribute appended after it.
func integrity(msg, key []byte) []byte {
	pre := append([]byte(nil), msg...)
	binary.BigEndian.PutUint16(pre[2:4], uint16(len(msg)-headerLen+4+sha1.Size))
	m := hmac.New(sha1.New, key)
	m.Write(pre)
	return m.Sum(nil)
}

// fingerprint returns the FINGERPRINT value for msg when the full message is total bytes long.
func fingerprint(msg []byte, total int) uint32 {
	pre := append([]byte(nil), msg...)
	binary.BigEndian.PutUint16(pre[2:4], uint16(total-headerLen))
	return crc32.ChecksumIEEE(pre) ^ fingerprintXor
}

// successResponse answers req from peer, signed with key.
func successResponse(req binding, peer netip.AddrPort, key []byte) []byte {
	addr := peer.Addr().Unmap()
	var xa []byte
	port := peer.Port() ^ uint16(magicCookie>>16)
	if addr.Is4() {
		ip := addr.As4()
		xa = make([]byte, 8)
		xa[1] = 1
		binary.BigEndian.PutUint16(xa[2:4], port)
		binary.BigEndian.PutUint32(xa[4:8], binary.BigEndian.Uint32(ip[:])^magicCookie)
	} else {
		ip := addr.As16()
		var mask [16]byte
		binary.BigEndian.PutUint32(mask[0:4], magicCookie)
		copy(mask[4:], req.txid[:])
		xa = make([]byte, 20)
		xa[1] = 2
		binary.BigEndian.PutUint16(xa[2:4], port)
		for i := range ip {
			xa[4+i] = ip[i] ^ mask[i]
		}
	}
	msg := make([]byte, headerLen, headerLen+4+len(xa)+4+sha1.Size+8)
	binary.BigEndian.PutUint16(msg[0:2], bindingSuccess)
	binary.BigEndian.PutUint32(msg[4:8], magicCookie)
	copy(msg[8:20], req.txid[:])
	msg = appendAttr(msg, attrXorMapped, xa)
	msg = appendAttr(msg, attrIntegrity, integrity(msg, key))
	var fp [4]byte
	binary.BigEndian.PutUint32(fp[:], fingerprint(msg, len(msg)+8))
	msg = appendAttr(msg, attrFingerprint, fp[:])
	return msg
}

// appendAttr appends one attribute (all values here are 4-byte aligned) and updates the header length.
func appendAttr(msg []byte, t uint16, v []byte) []byte {
	var h [4]byte
	binary.BigEndian.PutUint16(h[0:2], t)
	binary.BigEndian.PutUint16(h[2:4], uint16(len(v)))
	msg = append(append(msg, h[:]...), v...)
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(msg)-headerLen))
	return msg
}
