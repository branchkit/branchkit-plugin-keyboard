package main

// newTestHost is a host with no platform, the shape every handler test
// used to get from the package globals before they moved here.
func newTestHost() *Host { return newHost(nil) }
