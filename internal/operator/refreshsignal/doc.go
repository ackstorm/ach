// SPDX-License-Identifier: Apache-2.0

// Package refreshsignal subscribes to the ach_refresh Postgres NOTIFY channel
// and pushes a GenericEvent for the named CR into the per-Kind source.Channel
// the matching controller registered. Replaces the annotation-patching path
// the platform-api used to fire on /admin/refresh.
package refreshsignal
