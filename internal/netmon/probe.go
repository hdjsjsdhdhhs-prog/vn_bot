// Package netmon is the network monitor behind the "Мониторинг" admin page:
// it resolves the servers builders actually serve, checks every distinct
// endpoint once per round with a handshake-level probe and records state,
// outages and latency. See doc/network-monitoring.md for what each probe
// proves and its limits.
package netmon

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/subserver"

	"github.com/quic-go/quic-go"
)

// Stable, credential-free error codes of a failed check.
const (
	ErrCodeDNS          = "dns"
	ErrCodeTimeout      = "timeout"
	ErrCodeRefused      = "refused"
	ErrCodeUnreachable  = "unreachable"
	ErrCodeReset        = "reset"
	ErrCodeTLS          = "tls_handshake"
	ErrCodeQUIC         = "quic_handshake"
	ErrCodeObfsKey      = "obfs_key_missing"
	ErrCodeUnsupported  = "unsupported"
	ErrCodeNetwork      = "network_error"
	defaultProbeTimeout = 5 * time.Second
)

// Result is the outcome of one probe. LatencyMS is the duration of the
// handshake that proves the endpoint (TCP connect, TCP+TLS, QUIC), without
// DNS resolution.
type Result struct {
	OK        bool
	LatencyMS int
	ErrorCode string
}

// Prober checks one endpoint.
type Prober interface {
	Probe(ctx context.Context, ep subserver.MonitorEndpoint) Result
}

// NetProber is the production prober. Zero value is usable.
type NetProber struct {
	// Timeout bounds the whole probe including DNS (default 5s).
	Timeout time.Duration
	// Resolver resolves host names (default net.DefaultResolver).
	Resolver *net.Resolver
}

// Probe dispatches on ep.Probe.
func (p NetProber) Probe(ctx context.Context, ep subserver.MonitorEndpoint) Result {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if ep.Probe == subserver.ProbeUnsupported || ep.Probe == "" {
		return Result{ErrorCode: ErrCodeUnsupported}
	}

	ip, err := p.resolve(ctx, ep.Host)
	if err != nil {
		if ctx.Err() != nil {
			return Result{ErrorCode: ErrCodeTimeout}
		}
		return Result{ErrorCode: ErrCodeDNS}
	}
	addr := net.JoinHostPort(ip.String(), strconv.Itoa(ep.Port))

	switch ep.Probe {
	case subserver.ProbeTCP:
		return probeTCP(ctx, addr)
	case subserver.ProbeTLS:
		return probeTLS(ctx, addr, tlsConfigFor(ep))
	case subserver.ProbeQUIC:
		return probeQUIC(ctx, addr, ep)
	default:
		return Result{ErrorCode: ErrCodeUnsupported}
	}
}

func (p NetProber) resolve(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}
	resolver := p.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs { // prefer IPv4: IPv6 is often unrouted on VPSes
		if a.IP.To4() != nil {
			return a.IP, nil
		}
	}
	if len(addrs) == 0 {
		return nil, errors.New("no addresses")
	}
	return addrs[0].IP, nil
}

// probeTCP proves that something accepts TCP connections on the endpoint.
func probeTCP(ctx context.Context, addr string) Result {
	var d net.Dialer
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return Result{ErrorCode: classify(ctx, err, "")}
	}
	latency := time.Since(start)
	_ = conn.Close()
	return Result{OK: true, LatencyMS: millis(latency)}
}

// probeTLS proves a TLS (or Reality) server completes a handshake for the
// endpoint's SNI. Certificates are not verified: the check is reachability
// of the VPN inbound, and many servers use self-signed or borrowed
// (Reality) certificates.
func probeTLS(ctx context.Context, addr string, cfg *tls.Config) Result {
	var d net.Dialer
	start := time.Now()
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return Result{ErrorCode: classify(ctx, err, "")}
	}
	defer func() { _ = raw.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(deadline)
	}
	conn := tls.Client(raw, cfg)
	if err := conn.HandshakeContext(ctx); err != nil {
		return Result{ErrorCode: classify(ctx, err, ErrCodeTLS)}
	}
	latency := time.Since(start)
	_ = conn.Close()
	return Result{OK: true, LatencyMS: millis(latency)}
}

func tlsConfigFor(ep subserver.MonitorEndpoint) *tls.Config {
	serverName := ep.SNI
	if serverName == "" && net.ParseIP(ep.Host) == nil {
		serverName = ep.Host
	}
	// #nosec G402 -- reachability probe of a VPN inbound; no data is sent and
	// Reality/self-signed servers never present a verifiable certificate.
	return &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: true,
		NextProtos:         append([]string(nil), ep.ALPN...),
		MinVersion:         tls.VersionTLS12,
	}
}

// probeQUIC proves a QUIC server (Hysteria2) completes a handshake. With
// salamander obfuscation the packets are obfuscated with the server's key;
// without the right key the server silently drops them (timeout).
func probeQUIC(ctx context.Context, addr string, ep subserver.MonitorEndpoint) Result {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return Result{ErrorCode: ErrCodeDNS}
	}
	pc, err := net.ListenUDP("udp", nil)
	if err != nil {
		return Result{ErrorCode: ErrCodeNetwork}
	}
	var conn net.PacketConn = pc
	if ep.Obfs != "" {
		if ep.Obfs != "salamander" {
			_ = pc.Close()
			return Result{ErrorCode: ErrCodeUnsupported}
		}
		if ep.ObfsPassword() == "" {
			_ = pc.Close()
			return Result{ErrorCode: ErrCodeObfsKey}
		}
		conn = newSalamanderConn(pc, []byte(ep.ObfsPassword()))
	}
	tr := &quic.Transport{Conn: conn}
	defer func() { _ = tr.Close() }()

	cfg := tlsConfigFor(ep)
	if len(cfg.NextProtos) == 0 {
		cfg.NextProtos = []string{"h3"} // Hysteria2 speaks HTTP/3 for auth
	}
	cfg.MinVersion = tls.VersionTLS13
	qcfg := &quic.Config{HandshakeIdleTimeout: remaining(ctx), MaxIdleTimeout: remaining(ctx)}

	start := time.Now()
	qc, err := tr.Dial(ctx, udpAddr, cfg, qcfg)
	if err != nil {
		return Result{ErrorCode: classify(ctx, err, ErrCodeQUIC)}
	}
	latency := time.Since(start)
	_ = qc.CloseWithError(0, "")
	return Result{OK: true, LatencyMS: millis(latency)}
}

func remaining(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d > 0 {
			return d
		}
	}
	return defaultProbeTimeout
}

// classify maps an error to a stable code; fallback is used for protocol
// errors (handshake rejected) that are not transport failures.
func classify(ctx context.Context, err error, fallback string) string {
	var idle *quic.IdleTimeoutError
	var hs *quic.HandshakeTimeoutError
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded),
		errors.As(err, &idle), errors.As(err, &hs), ctx.Err() != nil:
		return ErrCodeTimeout
	case errors.As(err, &netErr) && netErr.Timeout(), isAny(err, errnoPlatformTimeout):
		return ErrCodeTimeout
	case isAny(err, errnoRefused):
		return ErrCodeRefused
	case isAny(err, errnoUnreachable):
		return ErrCodeUnreachable
	case isAny(err, errnoReset), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return ErrCodeReset
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ErrCodeDNS
	}
	if fallback != "" {
		return fallback
	}
	return ErrCodeNetwork
}

func isAny(err error, targets []error) bool {
	for _, target := range targets {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

func millis(d time.Duration) int {
	ms := int(d / time.Millisecond)
	if ms < 1 {
		return 1
	}
	return ms
}
