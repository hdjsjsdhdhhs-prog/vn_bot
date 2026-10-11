package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// vpnCatalogPath is the shared VPN client catalog. The Mini App imports the
// same file at build time, so platforms, apps, download links and import
// prefixes have exactly one source. See doc/vpn-apps.md.
const vpnCatalogPath = "templates/vpn_apps.json"

// Import modes. "direct" means the developer documents the deep link for this
// platform (or the project already ships it); "best_effort" means the scheme
// exists but its OS registration is not confirmed for this platform.
const (
	vpnImportDirect     = "direct"
	vpnImportBestEffort = "best_effort"
)

type vpnCatalog struct {
	Platforms []vpnPlatform `json:"platforms"`
	Apps      []vpnApp      `json:"apps"`
}

type vpnPlatform struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type vpnApp struct {
	ID           string                    `json:"id"`
	Name         string                    `json:"name"`
	Description  string                    `json:"description"`
	ImportPrefix string                    `json:"import_prefix"`
	EnableSteps  []string                  `json:"enable_steps"`
	Platforms    map[string]vpnAppPlatform `json:"platforms"`
}

type vpnAppPlatform struct {
	Import    string        `json:"import"`
	Downloads []vpnDownload `json:"downloads"`
}

type vpnDownload struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

var (
	vpnIDPattern     = regexp.MustCompile(`^[a-z0-9]{2,16}$`)
	vpnPrefixPattern = regexp.MustCompile(`^[a-z][a-z0-9]{1,15}://[a-z0-9]{2,16}/$`)
	// The subscription URL is appended verbatim (developer docs show the raw
	// URL after the prefix). Only unreserved path characters are accepted, so
	// nothing can be reinterpreted as a query, fragment or second URL.
	vpnSubURLPattern = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~-]*)*$`)
)

// vpnDownloadHosts allowlists official developer/store hosts. A new host must
// be verified against the developer's own site before it is added here.
var vpnDownloadHosts = []string{"apps.apple.com", "play.google.com", "github.com", "happ.info", "incy.cc", "v2raytun.com"}

func mustLoadVPNCatalog(files fs.FS) *vpnCatalog {
	catalog, err := loadVPNCatalog(files)
	if err != nil {
		panic(err)
	}

	return catalog
}

func loadVPNCatalog(files fs.FS) (*vpnCatalog, error) {
	raw, err := fs.ReadFile(files, vpnCatalogPath)
	if err != nil {
		return nil, fmt.Errorf("read VPN app catalog: %w", err)
	}
	var catalog vpnCatalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, fmt.Errorf("parse VPN app catalog: %w", err)
	}
	if err := catalog.validate(); err != nil {
		return nil, fmt.Errorf("invalid VPN app catalog: %w", err)
	}

	return &catalog, nil
}

func (c *vpnCatalog) validate() error {
	if len(c.Platforms) == 0 || len(c.Apps) == 0 {
		return errors.New("platforms and apps are required")
	}
	platforms := map[string]bool{}
	for _, p := range c.Platforms {
		if !vpnIDPattern.MatchString(p.ID) || p.Label == "" || platforms[p.ID] {
			return fmt.Errorf("bad platform %q", p.ID)
		}
		platforms[p.ID] = true
	}
	covered := map[string]bool{}
	apps := map[string]bool{}
	for _, a := range c.Apps {
		if !vpnIDPattern.MatchString(a.ID) || a.Name == "" || apps[a.ID] {
			return fmt.Errorf("bad app %q", a.ID)
		}
		apps[a.ID] = true
		if !isAppImportPrefix(a.ImportPrefix) {
			return fmt.Errorf("app %q: bad import prefix", a.ID)
		}
		if len(a.Platforms) == 0 {
			return fmt.Errorf("app %q: no platforms", a.ID)
		}
		for id, p := range a.Platforms {
			if !platforms[id] {
				return fmt.Errorf("app %q: unknown platform %q", a.ID, id)
			}
			if p.Import != vpnImportDirect && p.Import != vpnImportBestEffort {
				return fmt.Errorf("app %q/%s: bad import mode", a.ID, id)
			}
			if len(p.Downloads) == 0 {
				return fmt.Errorf("app %q/%s: no downloads", a.ID, id)
			}
			for _, d := range p.Downloads {
				if d.Label == "" || !isOfficialDownloadURL(d.URL) {
					return fmt.Errorf("app %q/%s: bad download %q", a.ID, id, d.URL)
				}
			}
			covered[id] = true
		}
	}
	for id := range platforms {
		if !covered[id] {
			return fmt.Errorf("platform %q has no apps", id)
		}
	}

	return nil
}

func isOfficialDownloadURL(raw string) bool {
	u, err := url.Parse(raw)

	return err == nil && u.Scheme == "https" && u.User == nil && u.Fragment == "" &&
		slices.Contains(vpnDownloadHosts, u.Hostname()) && u.Port() == ""
}

// vpnWebSchemes must never be used as an "app" scheme: a deep link has to hand
// the subscription URL to a native client, not to a website or script.
var vpnWebSchemes = []string{"http", "https", "javascript", "data", "file", "blob", "about", "ftp", "ws", "wss", "vbscript", "intent"}

func isAppImportPrefix(prefix string) bool {
	scheme, _, _ := strings.Cut(prefix, "://")

	return vpnPrefixPattern.MatchString(prefix) && !slices.Contains(vpnWebSchemes, scheme)
}

// vpnImportLink builds the client deep link for the canonical public
// subscription URL. It returns false when the URL contains anything but plain
// https path characters; callers then show only QR/copy fallbacks.
func vpnImportLink(prefix, subURL string) (string, bool) {
	if !isAppImportPrefix(prefix) || !vpnSubURLPattern.MatchString(subURL) {
		return "", false
	}
	if u, err := url.Parse(subURL); err != nil || u.String() != subURL || u.Host == "" {
		return "", false
	}

	return prefix + subURL, true
}

// connectPickerView is the server-rendered state of the connection steps.
// Deep links live only in href attributes, never in scripts.
type connectPickerView struct {
	Platforms       []connectPlatformView
	Apps            []connectAppView
	Panels          []connectPanelView
	DefaultPlatform string
	DefaultApp      string
}

type connectPlatformView struct {
	ID, Label string
	Selected  bool
}

type connectAppView struct {
	ID, Name, Description, Initial string
	PlatformList                   string
	Selected, Visible              bool
}

type connectPanelView struct {
	AppID, AppName, PlatformID, PlatformLabel string
	Downloads                                 []vpnDownload
	ImportLink                                template.URL
	HasImport, BestEffort, Visible            bool
	EnableSteps                               []string
}

func (c *vpnCatalog) pickerView(subURL string) connectPickerView {
	view := connectPickerView{DefaultPlatform: c.Platforms[0].ID}
	for _, a := range c.Apps {
		if _, ok := a.Platforms[view.DefaultPlatform]; ok {
			view.DefaultApp = a.ID
			break
		}
	}
	for _, p := range c.Platforms {
		view.Platforms = append(view.Platforms, connectPlatformView{ID: p.ID, Label: p.Label, Selected: p.ID == view.DefaultPlatform})
	}
	for _, a := range c.Apps {
		var ids []string
		for _, p := range c.Platforms {
			platform, ok := a.Platforms[p.ID]
			if !ok {
				continue
			}
			ids = append(ids, p.ID)
			panel := connectPanelView{
				AppID: a.ID, AppName: a.Name, PlatformID: p.ID, PlatformLabel: p.Label,
				Downloads: platform.Downloads, BestEffort: platform.Import == vpnImportBestEffort,
				EnableSteps: a.EnableSteps,
				Visible:     p.ID == view.DefaultPlatform && a.ID == view.DefaultApp,
			}
			if link, ok := vpnImportLink(a.ImportPrefix, subURL); ok {
				panel.ImportLink = template.URL(link) // #nosec G203 -- validated catalog prefix + validated https subscription URL
				panel.HasImport = true
			}
			view.Panels = append(view.Panels, panel)
		}
		initial := []rune(a.Name)[0]
		view.Apps = append(view.Apps, connectAppView{
			ID: a.ID, Name: a.Name, Description: a.Description, Initial: strings.ToUpper(string(initial)),
			PlatformList: strings.Join(ids, " "),
			Selected:     a.ID == view.DefaultApp,
			Visible:      slices.Contains(ids, view.DefaultPlatform),
		})
	}

	return view
}
