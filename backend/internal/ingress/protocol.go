package ingress

import "dsmapp/internal/apps"

const Socket = "/var/packages/SynoPkgConnect/var/ingress/control.sock"

type Request struct {
	Method  string               `json:"method"`
	Package string               `json:"package"`
	Request *apps.IngressRequest `json:"request,omitempty"`
	Ref     *apps.IngressRef     `json:"ref,omitempty"`
}

type Response struct {
	Version      int                 `json:"version,omitempty"`
	Capabilities []string            `json:"capabilities,omitempty"`
	Code         string              `json:"code,omitempty"`
	Result       *apps.IngressStatus `json:"result,omitempty"`
}
