package config

import (
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const AdminMinBcryptCost = 10
const AdminMaxBcryptCost = 14

var adminHashFormat = regexp.MustCompile(`^\$2[aby]\$(10|11|12|13|14)\$[./A-Za-z0-9]{53}$`)
var adminNameFormat = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,64}$`)
var adminHostLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
var ErrAdminConfiguration = errors.New("invalid admin configuration")

// AdminConfiguration validates optional browser administrator credentials and
// returns the canonical HTTPS origin. Errors never contain credential values.
// HTTP callers must also validate manually constructed Config values, since
// tests and embedders may bypass Load.
func (c *Config) AdminConfiguration() (enabled bool, origin string, err error) {
	if c == nil || (c.AdminUsername == "" && c.AdminPasswordHash == "") {
		return false, "", nil
	}
	if !adminNameFormat.MatchString(c.AdminUsername) || !adminHashFormat.MatchString(c.AdminPasswordHash) {
		return false, "", ErrAdminConfiguration
	}
	cost, e := bcrypt.Cost([]byte(c.AdminPasswordHash))
	if e != nil || cost < AdminMinBcryptCost || cost > AdminMaxBcryptCost {
		return false, "", ErrAdminConfiguration
	}
	// Cost only parses the prefix. Validate the salt and digest too, including
	// unused trailing bits, rather than enabling admin with a malformed hash.
	encoding := base64.NewEncoding("./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789").WithPadding(base64.NoPadding).Strict()
	if _, e := encoding.DecodeString(c.AdminPasswordHash[7:29]); e != nil {
		return false, "", ErrAdminConfiguration
	}
	if _, e := encoding.DecodeString(c.AdminPasswordHash[29:]); e != nil {
		return false, "", ErrAdminConfiguration
	}
	u, e := url.Parse(c.SiteURL)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(c.SiteURL, "#") || u.Opaque != "" {
		return false, "", ErrAdminConfiguration
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	// net/url accepts bracketed DNS/IPv4 and some unbracketed IPv6 authorities.
	// Only IPv6 literals may use brackets, and they must use them. Reject these
	// malformed authorities rather than turning them into a different origin.
	ipv6 := ip != nil && strings.Contains(host, ":")
	if strings.HasPrefix(u.Host, "[") != ipv6 {
		return false, "", ErrAdminConfiguration
	}
	if ip == nil {
		if len(host) > 253 {
			return false, "", ErrAdminConfiguration
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || !adminHostLabel.MatchString(label) {
				return false, "", ErrAdminConfiguration
			}
		}
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return false, "", ErrAdminConfiguration
		}
		if number != 443 {
			host += ":" + strconv.Itoa(number)
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return false, "", ErrAdminConfiguration
	}
	return true, "https://" + host, nil
}
