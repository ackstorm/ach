// SPDX-License-Identifier: Apache-2.0

// Package featuregate holds compile-time feature flags for the ach binaries.
//
// These are deliberately Go consts (not env vars or runtime config): a flag's
// value is fixed at build time so the compiler can dead-code-eliminate the
// gated surfaces, and so disabling a feature cannot be undone by a stray
// environment variable in production.
package featuregate

// PluginsEnabled gates every Plugin / PluginMarketplace surface across the
// operator, content-service, platform-api, environment resolution, and the
// ach-cli local package manager. It is currently OFF: the Plugin and
// PluginMarketplace CRDs are not shipped in the Helm chart, the operator does
// not wire their reconcilers, the content-service does not serve
// /content/plugin, the admin inventory hides plugin/marketplace rows, an
// Environment's context.plugins refs are skipped (not failed), and the
// `ach-cli local plugin` command is not registered.
//
// Skill / SkillMarketplace are unaffected — they are the supported content
// kinds. Flip this to true (and run `make helm-sync`) to re-enable plugins; no
// other code change is required.
const PluginsEnabled = false
