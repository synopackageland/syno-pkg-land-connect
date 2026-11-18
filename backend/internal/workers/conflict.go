package workers

import (
	"dsmapp/internal/connector"
	"net/url"
)

func bridgeConflict(code, kind, name, account, zone string) error {
	dashboard := "https://dash.cloudflare.com/" + account + "/workers/services/view/" + url.PathEscape(name) + "/production/settings"
	if kind == "route" {
		dashboard = "https://dash.cloudflare.com/" + account + "/" + url.PathEscape(zone) + "/workers"
	}
	return connector.WithNameConflict(failure(code), connector.NameConflict{Reason: "other", ObjectType: kind, ConflictName: name, DashboardURL: dashboard})
}
