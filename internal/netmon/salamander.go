package netmon

import (
	"crypto/rand"
	"net"
	"sync"

	"golang.org/x/crypto/blake2b"
)

// Salamander is the Hysteria2 packet obfuscation: every UDP packet is an
// 8-byte random salt followed by the payload XOR-ed with
// BLAKE2b-256(key || salt), the 32-byte hash repeated. Without the right key
// an obfuscated server drops every packet, so the probe needs the key; it is
// used here in memory only.

const (
	salamanderSaltLen = 8
	salamanderMaxPkt  = 2048
)

type salamanderConn struct {
	net.PacketConn
	key []byte

	readMu  sync.Mutex
	readBuf []byte
	writeMu sync.Mutex
	writeBf []byte
}

func newSalamanderConn(pc net.PacketConn, key []byte) *salamanderConn {
	return &salamanderConn{
		PacketConn: pc,
		key:        append([]byte(nil), key...),
		readBuf:    make([]byte, salamanderMaxPkt),
		writeBf:    make([]byte, salamanderMaxPkt+salamanderSaltLen),
	}
}

func (c *salamanderConn) mask(salt []byte) [blake2b.Size256]byte {
	buf := make([]byte, 0, len(c.key)+len(salt))
	buf = append(buf, c.key...)
	buf = append(buf, salt...)
	return blake2b.Sum256(buf)
}

func (c *salamanderConn) ReadFrom(p []byte) (int, net.Addr, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for {
		n, addr, err := c.PacketConn.ReadFrom(c.readBuf)
		if err != nil {
			return 0, addr, err
		}
		if n <= salamanderSaltLen {
			continue // not a salamander packet
		}
		h := c.mask(c.readBuf[:salamanderSaltLen])
		payload := c.readBuf[salamanderSaltLen:n]
		out := copy(p, payload)
		for i := 0; i < out; i++ {
			p[i] ^= h[i%blake2b.Size256]
		}
		return out, addr, nil
	}
}

func (c *salamanderConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if len(p)+salamanderSaltLen > len(c.writeBf) {
		c.writeBf = make([]byte, len(p)+salamanderSaltLen)
	}
	out := c.writeBf[:len(p)+salamanderSaltLen]
	if _, err := rand.Read(out[:salamanderSaltLen]); err != nil {
		return 0, err
	}
	h := c.mask(out[:salamanderSaltLen])
	for i, b := range p {
		out[salamanderSaltLen+i] = b ^ h[i%blake2b.Size256]
	}
	if _, err := c.PacketConn.WriteTo(out, addr); err != nil {
		return 0, err
	}
	return len(p), nil
}
