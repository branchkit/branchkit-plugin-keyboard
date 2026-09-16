package main

import "github.com/branchkit/plugin-sdk-go"

// newTestHost is a host on a detached plugin: no platform behind it, so a
// handler that reaches for one fails at once instead of being skipped by a
// nil guard. Tests that need an answer replace the seam they need.
func newTestHost() *Host { return newHost(branchkit.NewDetachedPlugin()) }
