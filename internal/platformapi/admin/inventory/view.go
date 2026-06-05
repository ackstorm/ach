// SPDX-License-Identifier: Apache-2.0

// Package inventory renders the admin read-only object inventory served under
// /platform/admin/<kind>. Each projection row (internal/db) is mapped to a
// uniform AdminObjectView the CLI (`ach admin list`) decodes verbatim.
//
// SYNC semantics are computed server-side from the projection alone (no live
// LiteLLM / k8s cross-check):
//
//   - content kinds (plugin/prompt/artifact/marketplace/external-ref):
//     last_successful_refresh + max_staleness_seconds staleness →
//     fresh / STALE(<age over>) / never.
//   - bip / litellm-connection: projected (presence; soft-deleted rows are
//     already excluded by the db List helpers).
//
// false-green caveat (memory plugin-scoping-followups): only context.plugins
// content-presence gates the Environment ExecutionResourcesResolved condition.
// prompts/artifacts are name-only / pass-through — their last_successful_refresh
// reflects "name resolved", not "content present + current". So the prompt and
// artifact mappers render a fresh state as "fresh*" (syncFreshFalseGreen); the
// CLI renderer turns the asterisk into a footnote. STALE/never stay honest and
// carry no asterisk. Marketplace plugins and external_refs ARE real
// upstream-fetch tracking, so they keep a bare "fresh".
package inventory

import (
	"strconv"
	"time"

	"github.com/ackstorm/ach/internal/db"
)

// SYNC state vocabulary. The asterisk on syncFreshFalseGreen is data the CLI
// renderer keys on to emit the prompts/artifacts footnote — see package doc.
const (
	syncFresh           = "fresh"
	syncFreshFalseGreen = "fresh*"
	syncStale           = "STALE"
	syncNever           = "never"
	syncProjected       = "projected"
)

// AdminObjectView is the uniform wire shape the server emits and the CLI
// decodes. Kind-specific fields land in Extra so the envelope stays stable
// across all object kinds.
type AdminObjectView struct {
	Kind       string            `json:"kind"`
	Namespace  string            `json:"namespace,omitempty"`
	Name       string            `json:"name"`
	Version    string            `json:"version,omitempty"`
	Sync       string            `json:"sync"`
	SyncReason string            `json:"syncReason,omitempty"`
	UpdatedAt  string            `json:"updatedAt,omitempty"` // RFC3339
	Origin     string            `json:"origin,omitempty"`
	Locked     bool              `json:"locked"`
	Extra      map[string]string `json:"extra,omitempty"`
}

// contentSync derives the staleness state for a refresh-tracked row. last==nil
// means "never refreshed"; past the deadline is STALE with a humanized age;
// otherwise fresh.
func contentSync(last *time.Time, maxStalenessSeconds int64, now time.Time) (state, reason string) {
	if last == nil {
		return syncNever, ""
	}
	deadline := last.Add(time.Duration(maxStalenessSeconds) * time.Second)
	if now.After(deadline) {
		return syncStale, humanizeDuration(now.Sub(deadline)) + " over"
	}
	return syncFresh, ""
}

// markFalseGreen upgrades a bare "fresh" to "fresh*" for kinds whose
// content-presence is NOT actually gated (prompts/artifacts). STALE/never are
// already honest and pass through unchanged.
func markFalseGreen(state string) string {
	if state == syncFresh {
		return syncFreshFalseGreen
	}
	return state
}

// optTime converts a non-nullable projection timestamp (the marketplace_plugins
// / external_refs List helpers COALESCE NULL → unix epoch) into the *time.Time
// "never" sentinel contentSync expects: a value at or before the unix epoch is
// treated as "no refresh has happened".
func optTime(t time.Time) *time.Time {
	if t.Unix() <= 0 {
		return nil
	}
	return &t
}

// humanizeDuration renders an over-staleness age at coarse granularity — exact
// seconds add noise to an at-a-glance inventory.
func humanizeDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	}
}

// rfc3339OrEmpty formats t as RFC3339 (UTC), or "" for the Go-zero / unix-epoch
// "never" sentinels so the CLI AGE column stays blank rather than showing 1970.
func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() || t.Unix() <= 0 {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func pluginRowToView(r db.PluginRow, now time.Time) AdminObjectView {
	sync, reason := contentSync(r.LastSuccessfulRefresh, r.MaxStalenessSeconds, now)
	return AdminObjectView{
		Kind:       "plugin",
		Namespace:  r.Namespace,
		Name:       r.Name,
		Version:    r.ResourceVersion,
		Sync:       sync,
		SyncReason: reason,
		UpdatedAt:  rfc3339OrEmpty(r.UpdatedAt),
		Extra:      map[string]string{"source": "plugin"},
	}
}

func skillRowToView(r db.SkillRow, now time.Time) AdminObjectView {
	// Skills are content-gated (like plugins): a bare "fresh" only after the
	// directory tar.gz is fetched (last_successful_refresh non-null), so NO
	// markFalseGreen upgrade. source=skill distinguishes a standalone Skill CR
	// from a marketplace-discovered skill in the merged SKILLS section.
	sync, reason := contentSync(r.LastSuccessfulRefresh, r.MaxStalenessSeconds, now)
	return AdminObjectView{
		Kind:       "skill",
		Namespace:  r.Namespace,
		Name:       r.Name,
		Version:    r.ResourceVersion,
		Sync:       sync,
		SyncReason: reason,
		UpdatedAt:  rfc3339OrEmpty(r.UpdatedAt),
		Extra:      map[string]string{"source": "skill"},
	}
}

// skillMarketplaceSkillAsSkillView maps a skill_marketplace_skills row into the
// merged SKILLS section: name is scoped as "<skill>@<marketplace>" and the
// source is "marketplace" so the CLI can distinguish it from a standalone Skill
// CR. Mirrors marketplacePluginAsPluginView.
func skillMarketplaceSkillAsSkillView(r db.SkillMarketplaceSkill, now time.Time) AdminObjectView {
	sync, reason := contentSync(optTime(r.LastSuccessfulRefresh), r.MaxStalenessSeconds, now)
	return AdminObjectView{
		Kind:       "skill",
		Name:       r.Name + "@" + r.MarketplaceName,
		Version:    r.UpstreamRev,
		Sync:       sync,
		SyncReason: reason,
		UpdatedAt:  rfc3339OrEmpty(r.LastSuccessfulRefresh),
		Extra:      map[string]string{"source": "marketplace", "marketplace": r.MarketplaceName},
	}
}

// skillMarketplaceRowToView maps a skill_marketplaces (object) row into the
// SKILLMARKETPLACES section. Mirrors marketplaceRowToView (reuses marketplaceSync).
func skillMarketplaceRowToView(r db.SkillMarketplaceRow) AdminObjectView {
	sync, reason := marketplaceSync(r.SyncedStatus, r.SyncedReason)
	return AdminObjectView{
		Kind:       "skill-marketplace",
		Namespace:  r.Namespace,
		Name:       r.Name,
		Version:    r.ResourceVersion,
		Sync:       sync,
		SyncReason: reason,
		UpdatedAt:  rfc3339OrEmpty(r.UpdatedAt),
		Extra:      map[string]string{"skillsCount": strconv.Itoa(r.SkillsCount)},
	}
}

func promptRowToView(r db.PromptRow, now time.Time) AdminObjectView {
	sync, reason := contentSync(r.LastSuccessfulRefresh, r.MaxStalenessSeconds, now)
	v := AdminObjectView{
		Kind:       "prompt",
		Namespace:  r.Namespace,
		Name:       r.Name,
		Version:    r.ResourceVersion,
		Sync:       markFalseGreen(sync), // prompts are name-only / not content-gated
		SyncReason: reason,
		UpdatedAt:  rfc3339OrEmpty(r.UpdatedAt),
	}
	if r.ContentType != nil && *r.ContentType != "" {
		v.Extra = map[string]string{"contentType": *r.ContentType}
	}
	return v
}

func artifactRowToView(r db.ArtifactRow, now time.Time) AdminObjectView {
	sync, reason := contentSync(r.LastSuccessfulRefresh, r.MaxStalenessSeconds, now)
	return AdminObjectView{
		Kind:       "artifact",
		Namespace:  r.Namespace,
		Name:       r.Name,
		Version:    r.ResourceVersion,
		Sync:       markFalseGreen(sync), // artifacts are pass-through / not content-gated
		SyncReason: reason,
		UpdatedAt:  rfc3339OrEmpty(r.UpdatedAt),
		Extra:      map[string]string{"scope": r.Scope},
	}
}

// marketplacePluginAsPluginView maps a marketplace_plugins row into the PLUGINS
// section: the name is scoped as "<plugin>@<marketplace>" and the source is
// "marketplace" so the CLI can distinguish it from a standalone Plugin CR.
// Version is the upstream rev (no resource_version on marketplace plugins).
func marketplacePluginAsPluginView(r db.MarketplacePlugin, now time.Time) AdminObjectView {
	sync, reason := contentSync(optTime(r.LastSuccessfulRefresh), r.MaxStalenessSeconds, now)
	return AdminObjectView{
		Kind:       "plugin",
		Name:       r.Name + "@" + r.MarketplaceName,
		Version:    r.UpstreamRev,
		Sync:       sync,
		SyncReason: reason,
		UpdatedAt:  rfc3339OrEmpty(r.LastSuccessfulRefresh),
		Extra:      map[string]string{"source": "marketplace", "marketplace": r.MarketplaceName},
	}
}

// marketplaceRowToView maps a marketplaces (object) row into the MARKETPLACES
// section. The Synced condition status/reason collapses into the SYNC
// vocabulary: True→Synced, False→Degraded(reason), anything else→Pending —
// the same vocabulary the environments inventory uses.
func marketplaceRowToView(r db.MarketplaceRow) AdminObjectView {
	sync, reason := marketplaceSync(r.SyncedStatus, r.SyncedReason)
	return AdminObjectView{
		Kind:       "marketplace",
		Namespace:  r.Namespace,
		Name:       r.Name,
		Version:    r.ResourceVersion,
		Sync:       sync,
		SyncReason: reason,
		UpdatedAt:  rfc3339OrEmpty(r.UpdatedAt),
		Extra:      map[string]string{"pluginsCount": strconv.Itoa(r.PluginsCount)},
	}
}

// marketplaceSync collapses the projected Synced condition into the inventory
// SYNC vocabulary.
func marketplaceSync(status, reason string) (sync, syncReason string) {
	switch status {
	case "True":
		return "Synced", ""
	case "False":
		return "Degraded", reason
	default:
		return "Pending", ""
	}
}

func bipRowToView(r db.BIPRow) AdminObjectView {
	return AdminObjectView{
		Kind:      "bip",
		Namespace: r.Namespace,
		Name:      r.Name,
		Version:   strconv.FormatInt(r.ObservedGeneration, 10),
		Sync:      syncProjected,
		UpdatedAt: rfc3339OrEmpty(r.UpdatedAt),
		Origin:    r.Origin,
		Locked:    r.Locked,
		Extra: map[string]string{
			"target":     r.TargetKind + "/" + r.TargetName,
			"forwardJWT": strconv.FormatBool(r.ForwardIdentityJWT),
		},
	}
}

func litellmConnToView(r db.LiteLLMConnectionRow) AdminObjectView {
	return AdminObjectView{
		Kind:      "litellm-connection",
		Namespace: r.Namespace,
		Name:      r.Name,
		Version:   r.ResourceVersion,
		Sync:      syncProjected,
		UpdatedAt: rfc3339OrEmpty(r.UpdatedAt),
		Origin:    r.Origin,
		Locked:    r.Locked,
		Extra:     map[string]string{"endpoint": r.Endpoint},
	}
}

func externalRefToView(r db.ExternalRef, now time.Time) AdminObjectView {
	sync, reason := contentSync(optTime(r.LastSuccessfulRefresh), r.MaxStalenessSeconds, now)
	return AdminObjectView{
		Kind:       "external-ref",
		Name:       r.Name,
		Version:    r.UpstreamRev, // upstream rev (commit SHA / ETag / generation)
		Sync:       sync,
		SyncReason: reason,
		UpdatedAt:  rfc3339OrEmpty(r.LastSuccessfulRefresh),
		Extra:      map[string]string{"refKind": r.Kind},
	}
}
