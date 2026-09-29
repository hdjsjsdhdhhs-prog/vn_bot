package subserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// Full Xray client configs: some panels (Remnawave and similar) answer with a
// JSON array where every element is a complete Xray config ({remarks, dns,
// inbounds, outbounds, routing, ...}) instead of a flat 3x-ui server object.
// Such a config cannot be expressed as one share link (it may balance several
// outbounds), so it is served as raw JSON and identified by its outbounds.

// xrayConfigInfo is the catalogue identity of one full Xray config.
type xrayConfigInfo struct {
	name        string
	protocol    string
	fingerprint string
}

// xrayNonProxyProtocols are outbounds that do not reach a VPN server.
var xrayNonProxyProtocols = map[string]bool{"freedom": true, "blackhole": true, "dns": true, "loopback": true}

type xrayServerRef struct {
	Address string          `json:"address"`
	Port    json.RawMessage `json:"port"`
}

type xrayOutbound struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Settings struct {
		xrayServerRef
		Version json.RawMessage `json:"version"`
		Vnext   []xrayServerRef `json:"vnext"`
		Servers []xrayServerRef `json:"servers"`
		Peers   []struct {
			Endpoint string `json:"endpoint"`
		} `json:"peers"`
	} `json:"settings"`
	StreamSettings struct {
		Network         string `json:"network"`
		Security        string `json:"security"`
		RealitySettings struct {
			ServerName string `json:"serverName"`
		} `json:"realitySettings"`
		TLSSettings struct {
			ServerName string `json:"serverName"`
		} `json:"tlsSettings"`
		WSSettings struct {
			Path string `json:"path"`
			Host string `json:"host"`
		} `json:"wsSettings"`
		GRPCSettings struct {
			ServiceName string `json:"serviceName"`
		} `json:"grpcSettings"`
		XHTTPSettings struct {
			Path string `json:"path"`
			Host string `json:"host"`
			Mode string `json:"mode"`
		} `json:"xhttpSettings"`
		HTTPUpgradeSettings struct {
			Path string `json:"path"`
			Host string `json:"host"`
		} `json:"httpupgradeSettings"`
	} `json:"streamSettings"`
}

type xrayFullConfig struct {
	Remarks   string         `json:"remarks"`
	Remark    string         `json:"remark"`
	Ps        string         `json:"ps"`
	Outbounds []xrayOutbound `json:"outbounds"`
}

// isXrayFullConfig reports whether raw is a JSON object with an outbounds array.
func isXrayFullConfig(raw json.RawMessage) bool {
	var probe struct {
		Outbounds json.RawMessage `json:"outbounds"`
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || json.Unmarshal(trimmed, &probe) != nil {
		return false
	}
	o := bytes.TrimSpace(probe.Outbounds)
	return len(o) > 0 && o[0] == '['
}

// parseXrayFullConfig returns the identity of a full Xray config. ok is false
// when raw is not such a config or has no proxy outbound with an address.
// The identity covers protocol, address, port and transport of every proxy
// outbound; credentials (ids, passwords, reality keys, auth) are excluded so
// it survives renames, credential rotation and reordering of the response.
func parseXrayFullConfig(raw json.RawMessage) (xrayConfigInfo, bool) {
	if !isXrayFullConfig(raw) {
		return xrayConfigInfo{}, false
	}
	var cfg xrayFullConfig
	if err := json.Unmarshal(bytes.TrimSpace(raw), &cfg); err != nil {
		return xrayConfigInfo{}, false
	}

	var identities []string
	primary := ""
	for i := range cfg.Outbounds {
		ob := &cfg.Outbounds[i]
		protocol := normalizeProtocol(ob.Protocol)
		if protocol == "" || xrayNonProxyProtocols[protocol] {
			continue
		}
		if protocol == "hysteria" && strings.TrimSpace(string(ob.Settings.Version)) == "2" {
			protocol = "hysteria2"
		}
		ids := xrayOutboundIdentities(ob, protocol)
		if len(ids) == 0 {
			continue
		}
		identities = append(identities, ids...)
		if primary == "" || ob.Tag == "proxy" {
			primary = protocol
		}
	}
	if len(identities) == 0 {
		return xrayConfigInfo{}, false
	}

	sort.Strings(identities)
	identities = dedupeSorted(identities)
	sum := sha256.Sum256([]byte("v1|xray|" + strings.Join(identities, "\n")))

	name := cfg.Remarks
	if name == "" {
		name = cfg.Remark
	}
	if name == "" {
		name = cfg.Ps
	}

	return xrayConfigInfo{name: name, protocol: primary, fingerprint: hex.EncodeToString(sum[:])}, true
}

func xrayOutboundIdentities(ob *xrayOutbound, protocol string) []string {
	servers := make([]xrayServerRef, 0, 1)
	servers = append(servers, ob.Settings.Vnext...)
	servers = append(servers, ob.Settings.Servers...)
	if ob.Settings.Address != "" {
		servers = append(servers, ob.Settings.xrayServerRef)
	}
	for _, p := range ob.Settings.Peers {
		if p.Endpoint != "" {
			servers = append(servers, xrayServerRef{Address: p.Endpoint})
		}
	}

	ss := &ob.StreamSettings
	sni := ss.RealitySettings.ServerName
	if sni == "" {
		sni = ss.TLSSettings.ServerName
	}
	transport := strings.Join([]string{
		"net=" + ss.Network, "security=" + ss.Security, "sni=" + sni,
		"path=" + firstNonEmpty(ss.WSSettings.Path, ss.XHTTPSettings.Path, ss.HTTPUpgradeSettings.Path),
		"host=" + firstNonEmpty(ss.WSSettings.Host, ss.XHTTPSettings.Host, ss.HTTPUpgradeSettings.Host),
		"serviceName=" + ss.GRPCSettings.ServiceName, "mode=" + ss.XHTTPSettings.Mode,
	}, "|")

	out := make([]string, 0, len(servers))
	for _, s := range servers {
		host := strings.ToLower(strings.TrimSpace(s.Address))
		if host == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s|%s|%s|%s", protocol, host, strings.Trim(string(s.Port), `" `), transport))
	}
	return out
}

// disambiguateXrayFingerprints gives distinct identities to full Xray configs
// of one response that share their outbounds but not their name. Such
// configs differ in routing/balancing (e.g. a provider "section" entry that
// reuses a real server's outbounds) and must not collapse into one entry.
// Every member of a collision gets the name mixed in, so the result does not
// depend on the (often shuffled) response order. Configs with the same
// outbounds and the same name stay one entry. Share links are not affected:
// the same server under two names is one server.
func disambiguateXrayFingerprints(entries []builderEntry, countries map[string]string) {
	names := make(map[string]map[string]bool)
	for _, e := range entries {
		if !e.xray {
			continue
		}
		if names[e.fingerprint] == nil {
			names[e.fingerprint] = make(map[string]bool)
		}
		names[e.fingerprint][e.name] = true
	}
	for i := range entries {
		e := &entries[i]
		if !e.xray || len(names[e.fingerprint]) < 2 {
			continue
		}
		sum := sha256.Sum256([]byte("v1|xray-named|" + e.fingerprint + "|" + e.name))
		e.fingerprint = hex.EncodeToString(sum[:])
		e.country = database.EntryCountry(countries[e.fingerprint], e.name)
	}
}

// xrayLinkOutbound carries the fields needed to express one outbound as a
// share link. It is decoded only to build the link served to the customer;
// nothing of it is stored in the catalogue or logged.
type xrayLinkOutbound struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Settings struct {
		Address string          `json:"address"`
		Port    json.RawMessage `json:"port"`
		Version json.RawMessage `json:"version"`
		Vnext   []struct {
			Address string          `json:"address"`
			Port    json.RawMessage `json:"port"`
			Users   []struct {
				ID         string `json:"id"`
				Flow       string `json:"flow"`
				Encryption string `json:"encryption"`
				Security   string `json:"security"`
			} `json:"users"`
		} `json:"vnext"`
		Servers []struct {
			Address  string          `json:"address"`
			Port     json.RawMessage `json:"port"`
			Password string          `json:"password"`
			Method   string          `json:"method"`
		} `json:"servers"`
	} `json:"settings"`
	StreamSettings struct {
		Network         string `json:"network"`
		Security        string `json:"security"`
		RealitySettings struct {
			ServerName  string `json:"serverName"`
			PublicKey   string `json:"publicKey"`
			ShortID     string `json:"shortId"`
			Fingerprint string `json:"fingerprint"`
		} `json:"realitySettings"`
		TLSSettings struct {
			ServerName    string   `json:"serverName"`
			Fingerprint   string   `json:"fingerprint"`
			Alpn          []string `json:"alpn"`
			AllowInsecure bool     `json:"allowInsecure"`
		} `json:"tlsSettings"`
		HysteriaSettings struct {
			Auth string `json:"auth"`
		} `json:"hysteriaSettings"`
		WSSettings struct {
			Path string `json:"path"`
			Host string `json:"host"`
		} `json:"wsSettings"`
		GRPCSettings struct {
			ServiceName string `json:"serviceName"`
		} `json:"grpcSettings"`
		XHTTPSettings struct {
			Path string `json:"path"`
			Host string `json:"host"`
			Mode string `json:"mode"`
		} `json:"xhttpSettings"`
		HTTPUpgradeSettings struct {
			Path string `json:"path"`
			Host string `json:"host"`
		} `json:"httpupgradeSettings"`
	} `json:"streamSettings"`
}

// xrayPrimaryLink expresses a full Xray config as one share link through its
// primary proxy outbound (tag "proxy", else the first proxy outbound), using
// the same link builders as 3x-ui JSON. It is exact for single-outbound
// configs; a balancing config is reduced to its primary server. Used when a
// builder mixes JSON and share-link sources and must answer with links.
func xrayPrimaryLink(raw json.RawMessage, name string) (string, bool) {
	var cfg struct {
		Outbounds []xrayLinkOutbound `json:"outbounds"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &cfg); err != nil {
		return "", false
	}
	var primary *xrayLinkOutbound
	for i := range cfg.Outbounds {
		ob := &cfg.Outbounds[i]
		if xrayNonProxyProtocols[normalizeProtocol(ob.Protocol)] || ob.Protocol == "" {
			continue
		}
		if primary == nil || ob.Tag == "proxy" {
			primary = ob
		}
		if ob.Tag == "proxy" {
			break
		}
	}
	if primary == nil {
		return "", false
	}

	ss := &primary.StreamSettings
	sc := &serverConfig{
		Remark:   name,
		Network:  ss.Network,
		Security: ss.Security,
		Host:     firstNonEmpty(ss.WSSettings.Host, ss.XHTTPSettings.Host, ss.HTTPUpgradeSettings.Host),
		Path:     firstNonEmpty(ss.WSSettings.Path, ss.XHTTPSettings.Path, ss.HTTPUpgradeSettings.Path, ss.GRPCSettings.ServiceName),
		Mode:     ss.XHTTPSettings.Mode,
	}
	switch ss.Security {
	case "reality":
		sc.SNI, sc.PublicKey, sc.ShortID, sc.Fingerprint = ss.RealitySettings.ServerName, ss.RealitySettings.PublicKey, ss.RealitySettings.ShortID, ss.RealitySettings.Fingerprint
	case "tls":
		sc.SNI, sc.Fingerprint, sc.AllowInsecure = ss.TLSSettings.ServerName, ss.TLSSettings.Fingerprint, ss.TLSSettings.AllowInsecure
		sc.Alpn = strings.Join(ss.TLSSettings.Alpn, ",")
	}

	st := &primary.Settings
	port := func(p json.RawMessage) int {
		var n int
		_, _ = fmt.Sscan(strings.Trim(string(p), `" `), &n)
		return n
	}
	switch protocol := normalizeProtocol(primary.Protocol); protocol {
	case "vless", "vmess":
		if len(st.Vnext) == 0 || len(st.Vnext[0].Users) == 0 {
			return "", false
		}
		v, u := st.Vnext[0], st.Vnext[0].Users[0]
		sc.Type, sc.Address, sc.Port, sc.UUID, sc.Flow, sc.Encryption = protocol, v.Address, port(v.Port), u.ID, u.Flow, u.Encryption
		if protocol == "vmess" {
			sc.Scy, sc.TLS = u.Security, ss.Security
		}
	case "trojan", "shadowsocks":
		if len(st.Servers) == 0 {
			return "", false
		}
		s := st.Servers[0]
		sc.Type, sc.Address, sc.Port, sc.Password, sc.Method = protocol, s.Address, port(s.Port), s.Password, s.Method
	case "hysteria":
		sc.Type, sc.Address, sc.Port, sc.Password = "hysteria2", st.Address, port(st.Port), ss.HysteriaSettings.Auth
		if strings.TrimSpace(string(st.Version)) != "2" {
			sc.Type = "hysteria"
		}
	case "hysteria2":
		sc.Type, sc.Address, sc.Port, sc.Password = "hysteria2", st.Address, port(st.Port), ss.HysteriaSettings.Auth
	default:
		return "", false
	}
	if strings.TrimSpace(sc.Address) == "" || sc.Port <= 0 {
		return "", false
	}

	var (
		link string
		err  error
	)
	switch sc.Type {
	case "vless":
		link, err = buildVLESSServerLink(sc)
	case "vmess":
		link, err = buildVMessServerLink(sc)
	case "trojan":
		link, err = buildTrojanServerLink(sc)
	case "shadowsocks":
		link, err = buildShadowsocksServerLink(sc)
	default:
		link, err = buildHysteriaServerLink(sc, sc.Type)
	}
	if err != nil {
		return "", false
	}
	// The link builders query-escape the fragment ("+" for spaces); RenameLink
	// rewrites it path-escaped so EntryName reads the name back unchanged.
	return RenameLink(link, name), true
}

// renameXrayConfig sets the display name (remarks) of a full Xray config and
// keeps every other field untouched.
func renameXrayConfig(raw json.RawMessage, name string) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(raw), &obj); err != nil {
		return raw
	}
	encoded, err := json.Marshal(name)
	if err != nil {
		return raw
	}
	obj["remarks"] = encoded
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}

// normalizeProtocol maps protocol aliases to one catalogue name.
func normalizeProtocol(p string) string {
	switch p = strings.ToLower(strings.TrimSpace(p)); p {
	case "ss":
		return "shadowsocks"
	case "hy2":
		return "hysteria2"
	default:
		return p
	}
}

// linkProtocol returns the catalogue protocol of a share link ("" if none).
func linkProtocol(link string) string {
	scheme, _, ok := strings.Cut(strings.TrimSpace(link), "://")
	if !ok {
		return ""
	}
	return normalizeProtocol(scheme)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func dedupeSorted(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
