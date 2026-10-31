package vpn

import (
	"regexp"
	"slices"
	"strings"
)

type TailscaleConnection struct {
	Tailnet  string   `json:"tailnet"`
	ClientID string   `json:"clientId"`
	Tags     []string `json:"tags"`
}
type TailscaleInput struct {
	Tailnet      string   `json:"tailnet"`
	ClientID     string   `json:"clientId"`
	ClientSecret *string  `json:"clientSecret,omitempty"`
	Tags         []string `json:"tags"`
}

func tailscaleMetadata(in *TailscaleInput) *TailscaleConnection {
	if in == nil {
		return nil
	}
	return &TailscaleConnection{Tailnet: in.Tailnet, ClientID: in.ClientID, Tags: slices.Clone(in.Tags)}
}
func tailscaleMetadataInput(in *TailscaleConnection) *TailscaleInput {
	if in == nil {
		return nil
	}
	return &TailscaleInput{Tailnet: in.Tailnet, ClientID: in.ClientID, Tags: slices.Clone(in.Tags)}
}

var tailnetRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.@_-]{0,252}$`)
var tagRE = regexp.MustCompile(`^tag:[a-zA-Z][a-zA-Z0-9-]{0,62}$`)

func normalizeTailscaleInput(in Input, create bool) (Input, error) {
	if in.Country != "" || in.Username != nil || in.Password != nil {
		return Input{}, invalid("tailscale", "NordVPN fields cannot be combined with Tailscale")
	}
	if in.Tailscale == nil {
		return Input{}, invalid("tailscale", "configuration is required")
	}
	v := *in.Tailscale
	v.Tailnet = strings.TrimSpace(v.Tailnet)
	v.ClientID = strings.TrimSpace(v.ClientID)
	v.Tags = slices.Clone(v.Tags)
	if !tailnetRE.MatchString(v.Tailnet) {
		return Input{}, invalid("tailscale.tailnet", "enter a tailnet name or ID")
	}
	if v.ClientID == "" || len(v.ClientID) > 256 || strings.ContainsAny(v.ClientID, "\x00\r\n") {
		return Input{}, invalid("tailscale.clientId", "enter an OAuth client ID")
	}
	if len(v.Tags) == 0 || len(v.Tags) > 32 {
		return Input{}, invalid("tailscale.tags", "choose 1–32 device tags")
	}
	for i, tag := range v.Tags {
		v.Tags[i] = strings.TrimSpace(tag)
		if !tagRE.MatchString(v.Tags[i]) {
			return Input{}, invalid("tailscale.tags", "use tags such as tag:deskyard")
		}
	}
	slices.Sort(v.Tags)
	v.Tags = slices.Compact(v.Tags)
	if create && v.ClientSecret == nil {
		return Input{}, invalid("tailscale.clientSecret", "OAuth client secret is required")
	}
	if v.ClientSecret != nil && (strings.TrimSpace(*v.ClientSecret) == "" || len(*v.ClientSecret) > 4096 || strings.ContainsAny(*v.ClientSecret, "\x00\r\n")) {
		return Input{}, invalid("tailscale.clientSecret", "enter a single-line OAuth secret")
	}
	in.Tailscale = &v
	// Existing immutable two-file secret revisions store the OAuth client ID and
	// secret. These paths never become public provider metadata.
	in.Username = &v.ClientID
	in.Password = v.ClientSecret
	return in, nil
}
