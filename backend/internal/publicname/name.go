// Package publicname builds readable DNS labels without installation identifiers.
package publicname

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var LabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var nonLabel = regexp.MustCompile(`[^a-z0-9-]+`)
var ErrTaken = errors.New("no available public name")

func NASLabel(hostname string) string {
	label := strings.Trim(nonLabel.ReplaceAllString(strings.ToLower(hostname), "-"), "-")
	if label == "" {
		return "dsm"
	}
	return strings.TrimRight(label[:min(len(label), 63)], "-")
}

func NASName() string {
	hostname, _ := os.Hostname()
	return NASLabel(hostname)
}

// ShortName matches the Free link's App name; DSM's Free link starts at dsm.
// A custom App's ID drops its "custom." prefix: custom.plex starts at plex.
func ShortName(pkg string) string {
	label := NASLabel(strings.TrimPrefix(pkg, "custom."))
	if short := strings.TrimLeft(strings.TrimPrefix(label, "synopkg"), "-"); short != "" {
		label = short
	}
	return strings.TrimRight(label[:min(len(label), 50)], "-")
}

// Candidate follows app, app-nas, app-nas-2, app-nas-3, ... . Keep both
// readable parts when truncating, and reserve room for the numeric suffix.
func Candidate(base, nas string, attempt int) string {
	base, nas = NASLabel(base), NASLabel(nas)
	if attempt == 0 {
		return base
	}
	suffix := ""
	if attempt > 1 {
		suffix = "-" + strconv.Itoa(attempt)
	}
	nas = strings.TrimRight(nas[:min(len(nas), 30)], "-")
	base = strings.TrimRight(base[:min(len(base), 62-len(nas)-len(suffix))], "-")
	return base + "-" + nas + suffix
}

// Choose bounds cloud lookups and stops on any failure other than a collision.
func Choose(ctx context.Context, base, nas string, free func(string) (bool, error)) (string, error) {
	for attempt := 0; attempt < 1000; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name := Candidate(base, nas, attempt)
		available, err := free(name)
		if err != nil {
			return "", err
		}
		if available {
			return name, nil
		}
	}
	return "", ErrTaken
}
