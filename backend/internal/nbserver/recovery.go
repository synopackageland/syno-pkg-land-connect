package nbserver

import "dsmapp/internal/connector"

// Only prerequisite conditions become stale when their upstream is repaired.
// Resource conflicts retain their own exact cause and recovery information.
func isDependencyFailure(code string) bool {
	switch code {
	case "cloudflare_not_connected", "cloudflare_connecting", "cloudflare_own_domain_required", "cloudflare_reconnect_required", "workers_not_connected", "workers_token_invalid", "workers_zone_account", "netbird_server_workers_required", "netbird_server_bridge_missing":
		return true
	default:
		return false
	}
}

func (s *Server) failStep(id string, e error) {
	s.smu.Lock()
	defer s.smu.Unlock()
	for i := range s.steps {
		if s.steps[i].ID == id {
			s.steps[i].State = "failed"
			s.steps[i].Code = code(e)
			s.steps[i].Conflict = connector.Conflict(e)
			if c := s.steps[i].Conflict; c != nil && c.ObjectType != "worker" {
				// Renaming a Worker preserves the server URL. Changing an existing
				// server hostname would also migrate registered clients, so this
				// action cannot promise that. Keep its exact guide and Retry instead.
				c.SuggestedName = ""
			}
		}
	}
	s.code = code(e)
}
