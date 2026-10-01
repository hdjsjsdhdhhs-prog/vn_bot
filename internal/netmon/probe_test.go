package netmon

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/subserver"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if err := testutil.InitLogger(m); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to initialize logger:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func selfSignedTLS(t *testing.T, alpn ...string) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "probe.test"},
		DNSNames: []string{"probe.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:   alpn,
		MinVersion:   tls.VersionTLS12,
	}
}

func endpoint(probe, host string, port int) subserver.MonitorEndpoint {
	return subserver.MonitorEndpoint{Protocol: "vless", Host: host, Port: port, Probe: probe, SNI: "probe.test"}
}

func hostPort(t *testing.T, addr net.Addr) (string, int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr.String())
	require.NoError(t, err)
	port, err := strconv.Atoi(p)
	require.NoError(t, err)
	return h, port
}

func TestProbeTCP_UpAndRefused(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	host, port := hostPort(t, ln.Addr())

	r := NetProber{Timeout: 2 * time.Second}.Probe(context.Background(), endpoint(subserver.ProbeTCP, host, port))
	assert.True(t, r.OK)
	assert.GreaterOrEqual(t, r.LatencyMS, 1)

	// A closed port answers with RST.
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, cport := hostPort(t, closed.Addr())
	require.NoError(t, closed.Close())
	r = NetProber{Timeout: 2 * time.Second}.Probe(context.Background(), endpoint(subserver.ProbeTCP, host, cport))
	assert.False(t, r.OK)
	assert.Equal(t, ErrCodeRefused, r.ErrorCode)
	assert.Zero(t, r.LatencyMS)
}

func TestProbeTLS_HandshakeTimeoutAndPlainServer(t *testing.T) {
	t.Parallel()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", selfSignedTLS(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = c.(*tls.Conn).Handshake(); _ = c.Close() }()
		}
	}()
	host, port := hostPort(t, ln.Addr())
	r := NetProber{Timeout: 2 * time.Second}.Probe(context.Background(), endpoint(subserver.ProbeTLS, host, port))
	assert.True(t, r.OK, "a self-signed (or Reality-borrowed) certificate still proves the TLS inbound")
	assert.GreaterOrEqual(t, r.LatencyMS, 1)

	// Accepts TCP but never speaks TLS: the handshake times out (TCP alone
	// is not enough for a TLS endpoint).
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = silent.Close() })
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	_, sport := hostPort(t, silent.Addr())
	r = NetProber{Timeout: 500 * time.Millisecond}.Probe(context.Background(), endpoint(subserver.ProbeTLS, host, sport))
	assert.False(t, r.OK)
	assert.Equal(t, ErrCodeTimeout, r.ErrorCode)

	// Speaks something that is not TLS: handshake error, not a timeout.
	garbage, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = garbage.Close() })
	go func() {
		for {
			c, err := garbage.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			_ = c.Close()
		}
	}()
	_, gport := hostPort(t, garbage.Addr())
	r = NetProber{Timeout: 2 * time.Second}.Probe(context.Background(), endpoint(subserver.ProbeTLS, host, gport))
	assert.False(t, r.OK)
	assert.Contains(t, []string{ErrCodeTLS, ErrCodeReset}, r.ErrorCode)
}

// quicServer runs a QUIC listener (optionally salamander-obfuscated) that
// accepts connections like a Hysteria2 server.
func quicServer(t *testing.T, obfsKey string) (string, int) {
	t.Helper()
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	var pc net.PacketConn = udp
	if obfsKey != "" {
		pc = newSalamanderConn(udp, []byte(obfsKey))
	}
	tr := &quic.Transport{Conn: pc}
	ln, err := tr.Listen(selfSignedTLS(t, "h3"), &quic.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close(); _ = tr.Close() })
	go func() {
		for {
			c, err := ln.Accept(context.Background())
			if err != nil {
				return
			}
			go func() { <-c.Context().Done() }()
		}
	}()
	return hostPort(t, udp.LocalAddr())
}

func hy2(host string, port int, obfs, key string) subserver.MonitorEndpoint {
	ep, ok := subserver.MonitorEndpointFromLink(fmt.Sprintf("hysteria2://pw@%s:%d/?sni=probe.test&obfs=%s&obfs-password=%s#hy", host, port, obfs, key))
	if !ok {
		panic("bad test link")
	}
	return ep
}

func TestProbeQUIC_Hysteria2(t *testing.T) {
	t.Parallel()

	host, port := quicServer(t, "")
	r := NetProber{Timeout: 3 * time.Second}.Probe(context.Background(), hy2(host, port, "", ""))
	assert.True(t, r.OK, "QUIC handshake with ALPN h3")
	assert.GreaterOrEqual(t, r.LatencyMS, 1)

	// Nothing listens: the QUIC handshake times out (UDP has no refusal).
	free, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	_, fport := hostPort(t, free.LocalAddr())
	require.NoError(t, free.Close())
	r = NetProber{Timeout: 700 * time.Millisecond}.Probe(context.Background(), hy2(host, fport, "", ""))
	assert.False(t, r.OK)
	assert.Contains(t, []string{ErrCodeTimeout, ErrCodeRefused, ErrCodeQUIC}, r.ErrorCode)
}

func TestProbeQUIC_SalamanderObfuscation(t *testing.T) {
	t.Parallel()

	host, port := quicServer(t, "correct-key")
	r := NetProber{Timeout: 3 * time.Second}.Probe(context.Background(), hy2(host, port, "salamander", "correct-key"))
	assert.True(t, r.OK, "obfuscated server answers with the right key")

	r = NetProber{Timeout: 700 * time.Millisecond}.Probe(context.Background(), hy2(host, port, "salamander", "wrong-key"))
	assert.False(t, r.OK, "wrong key: the server drops every packet")
	assert.Equal(t, ErrCodeTimeout, r.ErrorCode)

	r = NetProber{Timeout: 700 * time.Millisecond}.Probe(context.Background(), hy2(host, port, "", ""))
	assert.False(t, r.OK, "an obfuscated server does not answer plain QUIC")

	missing := hy2(host, port, "salamander", "")
	r = NetProber{Timeout: time.Second}.Probe(context.Background(), missing)
	assert.Equal(t, ErrCodeObfsKey, r.ErrorCode)
}

func TestProbe_DNSAndUnsupported(t *testing.T) {
	t.Parallel()

	r := NetProber{Timeout: 2 * time.Second}.Probe(context.Background(), endpoint(subserver.ProbeTCP, "does-not-exist.invalid", 443))
	assert.False(t, r.OK)
	assert.Contains(t, []string{ErrCodeDNS, ErrCodeTimeout}, r.ErrorCode)

	r = NetProber{}.Probe(context.Background(), endpoint(subserver.ProbeUnsupported, "127.0.0.1", 443))
	assert.Equal(t, Result{ErrorCode: ErrCodeUnsupported}, r)
}

func TestSalamanderConn_RoundTrip(t *testing.T) {
	t.Parallel()

	a, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	b, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	sa, sb := newSalamanderConn(a, []byte("k")), newSalamanderConn(b, []byte("k"))

	_, err = sa.WriteTo([]byte("hello quic"), b.LocalAddr())
	require.NoError(t, err)

	// On the wire the payload is obfuscated (salt + XOR).
	raw := make([]byte, 64)
	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = sa.WriteTo([]byte("hello quic"), b.LocalAddr())
	require.NoError(t, err)
	buf := make([]byte, 64)
	n, _, err := sb.ReadFrom(buf)
	require.NoError(t, err)
	assert.Equal(t, "hello quic", string(buf[:n]))
	n, _, err = b.ReadFrom(raw)
	require.NoError(t, err)
	assert.Equal(t, len("hello quic")+salamanderSaltLen, n)
	assert.NotContains(t, string(raw[:n]), "hello quic")
}
