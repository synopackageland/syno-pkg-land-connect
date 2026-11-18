package connector

import "dsmapp/internal/daemon"

func ReadPrivate(path string) ([]byte, error)    { return daemon.ReadPrivate(path) }
func WritePrivate(path string, raw []byte) error { return daemon.WritePrivate(path, raw) }
