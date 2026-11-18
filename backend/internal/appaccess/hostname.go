package appaccess

import (
	"dsmapp/internal/apps"
	"dsmapp/internal/publicname"
	"strings"
)

func selectHostname(fallback, requested, suffix string) (string, error) {
	if requested == "" {
		requested = fallback
	}
	label, ok := strings.CutSuffix(requested, "."+suffix)
	if !ok || !publicname.LabelPattern.MatchString(label) {
		return "", apps.ErrInvalid
	}
	return requested, nil
}
