// Package host is the supported embedding surface for host applications.
//
// Host projects should import this package instead of reaching into
// domain/files/*, config, or di packages directly. Project-specific adapters
// remain responsible for mapping local config, auth context, and deployment
// wiring into these public contracts.
package host
