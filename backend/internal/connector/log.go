package connector

import (
	"io"
	"log"
)

// The daemon log (stderr, kept by the package supervisor as daemon.log)
// records lifecycle outcomes and failure reasons only. Callers pass provider
// IDs, codes and package-owned error text; never requests, tokens or child
// process output.
var daemonLog = log.New(io.Discard, "connectd: ", log.LstdFlags)

func SetLog(w io.Writer)              { daemonLog.SetOutput(w) }
func Logf(format string, args ...any) { daemonLog.Printf(format, args...) }
