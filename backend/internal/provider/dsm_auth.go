package provider

// dsmAuthDescriptor reports whether an API.Info SYNO.API.Auth entry is DSM's.
func dsmAuthDescriptor(path string, minVersion, maxVersion int) bool {
	return (path == "entry.cgi" || path == "auth.cgi") && minVersion >= 1 && maxVersion >= minVersion
}
