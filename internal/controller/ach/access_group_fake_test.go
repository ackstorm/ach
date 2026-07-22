// SPDX-License-Identifier: Apache-2.0

// Envtest fake LiteLLM for the §7 AccessGroupSynced reconciler tests
// (issue #17 — /v1/access_group surface). The fake records:
//   - Create / Update / Delete calls per env.Name
//   - Last-seen create + update requests (for desired-state assertion)
//   - Resolver maps that callers may seed before reconciler fires
//
// Each test resets via accessGroupFake.Reset() before driving the
// reconciler.

package ach

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"

	"github.com/go-logr/logr"

	"github.com/ackstorm/ach/internal/litellm"
)

type accessGroupFakeImpl struct {
	*litellm.NoopClient

	mu sync.Mutex

	createCalls map[string]int
	updateCalls map[string]int
	deleteCalls map[string]int

	lastCreate map[string]litellm.AccessGroupCreateRequest
	lastUpdate map[string]litellm.AccessGroupUpdateRequest

	createErrByName map[string]error
	updateErrByName map[string]error

	// stored simulates the upstream /v1/access_group state. Keyed by
	// access_group_name.
	stored map[string]*litellm.AccessGroupResponse

	// Resolver seeds — tests populate BEFORE creating the Environment CR.
	// mcps / agents are name→id. teamsByAlias is alias→entries (matching
	// the existing ListTeamsByAlias([]TeamListEntry, error) shape).
	mcps         map[string]string
	agents       map[string]string
	teamsByAlias map[string][]litellm.TeamListEntry

	// teamMirror is teamID → access_group_ids (the team-side mirror).
	teamMirror map[string][]string

	// Shell-team state: alias → entry, plus call counters so a test can
	// assert create-once / repair-on-drift.
	teamsByID       map[string]litellm.TeamListEntry
	teamCreateCalls map[string]int
	teamUpdateCalls map[string]int
	teamDeleteCalls map[string]int
	lastTeamCreate  map[string]litellm.NewTeamRequest
	teamCreateErr   error

	// teamUpdateResult, when non-nil, is returned by UpdateTeam VERBATIM
	// instead of the post-write entry state — models a LiteLLM whose POST
	// /team/update 200s without actually applying the write (Fix 1: the
	// caller must re-verify the response rather than trusting the status
	// code).
	teamUpdateResult *litellm.TeamListEntry

	// teamInfoErrByID injects a GetTeamInfo error for a specific team id —
	// drives the entitledUserShellIDs resolveFailed guard (Task 4).
	teamInfoErrByID map[string]error

	// order records the method-name call sequence across CreateTeam /
	// UpdateTeam / DeleteTeam / DeleteAccessGroup / RevokeKey — the
	// TestReconcileDeletionOrder assertion that ek_ revoke + access-group
	// delete both happen strictly before the shell team is deleted.
	order []string

	// mirrorHistory snapshots teamMirror after every UpdateAccessGroup so
	// a test can assert that a HEALTHY team's mirror never went empty at
	// any intermediate step of a repair sequence.
	mirrorHistory []map[string][]string

	// mirrorFrozen simulates a LiteLLM whose delta-driven mirror write
	// disappeared (a semantics change upstream). PUTs still land; the
	// mirror never moves. Drives the MirrorUnconverged guard test.
	mirrorFrozen bool

	listErr error
}

func newAccessGroupFake() *accessGroupFakeImpl {
	return &accessGroupFakeImpl{
		NoopClient:      &litellm.NoopClient{Log: logr.Discard()},
		createCalls:     map[string]int{},
		updateCalls:     map[string]int{},
		deleteCalls:     map[string]int{},
		lastCreate:      map[string]litellm.AccessGroupCreateRequest{},
		lastUpdate:      map[string]litellm.AccessGroupUpdateRequest{},
		createErrByName: map[string]error{},
		updateErrByName: map[string]error{},
		stored:          map[string]*litellm.AccessGroupResponse{},
		mcps:            map[string]string{},
		agents:          map[string]string{},
		teamsByAlias:    map[string][]litellm.TeamListEntry{},
		teamMirror:      map[string][]string{},
		teamsByID:       map[string]litellm.TeamListEntry{},
		teamCreateCalls: map[string]int{},
		teamUpdateCalls: map[string]int{},
		teamDeleteCalls: map[string]int{},
		lastTeamCreate:  map[string]litellm.NewTeamRequest{},
		teamInfoErrByID: map[string]error{},
	}
}

func (f *accessGroupFakeImpl) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls = map[string]int{}
	f.updateCalls = map[string]int{}
	f.deleteCalls = map[string]int{}
	f.lastCreate = map[string]litellm.AccessGroupCreateRequest{}
	f.lastUpdate = map[string]litellm.AccessGroupUpdateRequest{}
	f.createErrByName = map[string]error{}
	f.updateErrByName = map[string]error{}
	f.stored = map[string]*litellm.AccessGroupResponse{}
	f.mcps = map[string]string{}
	f.agents = map[string]string{}
	f.teamsByAlias = map[string][]litellm.TeamListEntry{}
	f.teamMirror = map[string][]string{}
	f.mirrorHistory = nil
	f.mirrorFrozen = false
	f.listErr = nil
	f.teamsByID = map[string]litellm.TeamListEntry{}
	f.teamCreateCalls = map[string]int{}
	f.teamUpdateCalls = map[string]int{}
	f.teamDeleteCalls = map[string]int{}
	f.lastTeamCreate = map[string]litellm.NewTeamRequest{}
	f.teamCreateErr = nil
	f.teamUpdateResult = nil
	f.teamInfoErrByID = map[string]error{}
	f.order = nil
}

func (f *accessGroupFakeImpl) CreateAccessGroup(_ context.Context, req litellm.AccessGroupCreateRequest) (*litellm.AccessGroupResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls[req.AccessGroupName]++
	f.lastCreate[req.AccessGroupName] = req
	if err := f.createErrByName[req.AccessGroupName]; err != nil {
		return nil, err
	}
	resp := &litellm.AccessGroupResponse{
		AccessGroupID:      "ag-uuid-" + req.AccessGroupName,
		AccessGroupName:    req.AccessGroupName,
		AccessModelNames:   append([]string{}, req.AccessModelNames...),
		AccessMCPServerIDs: append([]string{}, req.AccessMCPServerIDs...),
		AccessAgentIDs:     append([]string{}, req.AccessAgentIDs...),
		AssignedTeamIDs:    append([]string{}, req.AssignedTeamIDs...),
		AssignedKeyIDs:     append([]string{}, req.AssignedKeyIDs...),
	}
	f.stored[req.AccessGroupName] = resp
	// A team named in assigned_team_ids at CREATE time is entering that
	// relationship for the first time — the same ENTER delta an UpdateAccessGroup
	// PUT produces (see the delta-driven mirror doc on UpdateAccessGroup below).
	// Without this a freshly created group's own mirror looks drifted on the
	// very next reconcile pass (status write → watch → immediate re-reconcile).
	if !f.mirrorFrozen {
		for _, t := range req.AssignedTeamIDs {
			if !slices.Contains(f.teamMirror[t], resp.AccessGroupID) {
				f.teamMirror[t] = append(f.teamMirror[t], resp.AccessGroupID)
			}
		}
	}
	return resp, nil
}

func (f *accessGroupFakeImpl) GetAccessGroupByName(_ context.Context, name string) (*litellm.AccessGroupResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	if r, ok := f.stored[name]; ok {
		out := *r
		return &out, nil
	}
	return nil, nil
}

func (f *accessGroupFakeImpl) UpdateAccessGroup(_ context.Context, id string, req litellm.AccessGroupUpdateRequest) (*litellm.AccessGroupResponse, error) {
	// Round-trip the request through the JSON marshaler so the fake
	// observes the SAME wire semantics LiteLLM sees, instead of the raw
	// Go struct. This is the gap that let the omitempty bug ship: a
	// struct-level fake is blind to `omitempty`, so an empty managed list
	// looked non-nil here even though it was dropped on the wire. After
	// the round-trip the `!= nil` guards below mean absent=keep / `[]`=clear
	// exactly like the upstream PUT (absent → omitempty/null → nil → keep;
	// `[]` → non-nil empty → clear).
	if raw, err := json.Marshal(req); err == nil {
		var wire litellm.AccessGroupUpdateRequest
		if err := json.Unmarshal(raw, &wire); err == nil {
			req = wire
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var found *litellm.AccessGroupResponse
	var name string
	for n, r := range f.stored {
		if r.AccessGroupID == id {
			found = r
			name = n
			break
		}
	}
	f.updateCalls[name]++
	f.lastUpdate[name] = req
	if err := f.updateErrByName[name]; err != nil {
		return nil, err
	}
	if found == nil {
		return nil, errors.New("fake: UpdateAccessGroup id not found")
	}
	if req.AccessModelNames != nil {
		found.AccessModelNames = append([]string{}, req.AccessModelNames...)
	}
	if req.AccessMCPServerIDs != nil {
		found.AccessMCPServerIDs = append([]string{}, req.AccessMCPServerIDs...)
	}
	if req.AccessAgentIDs != nil {
		found.AccessAgentIDs = append([]string{}, req.AccessAgentIDs...)
	}
	if req.AssignedTeamIDs != nil {
		// Delta-driven mirror, exactly as measured against prod LiteLLM
		// on 2026-07-21: only teams crossing the assignment boundary get
		// their access_group_ids rewritten. An identical list is a no-op.
		if !f.mirrorFrozen {
			prev := map[string]struct{}{}
			for _, t := range found.AssignedTeamIDs {
				prev[t] = struct{}{}
			}
			next := map[string]struct{}{}
			for _, t := range req.AssignedTeamIDs {
				next[t] = struct{}{}
			}
			for t := range next {
				if _, was := prev[t]; !was { // ENTER
					if !slices.Contains(f.teamMirror[t], found.AccessGroupID) {
						f.teamMirror[t] = append(f.teamMirror[t], found.AccessGroupID)
					}
				}
			}
			for t := range prev {
				if _, still := next[t]; !still { // LEAVE
					f.teamMirror[t] = slices.DeleteFunc(
						append([]string{}, f.teamMirror[t]...),
						func(g string) bool { return g == found.AccessGroupID })
				}
			}
		}
		found.AssignedTeamIDs = append([]string{}, req.AssignedTeamIDs...)
	}
	f.mirrorHistory = append(f.mirrorHistory, cloneMirror(f.teamMirror))
	out := *found
	return &out, nil
}

func (f *accessGroupFakeImpl) DeleteAccessGroupByID(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for n, r := range f.stored {
		if r.AccessGroupID == id {
			f.deleteCalls[n]++
			delete(f.stored, n)
			return nil
		}
	}
	return nil
}

func (f *accessGroupFakeImpl) DeleteAccessGroup(ctx context.Context, name string) error {
	f.mu.Lock()
	f.order = append(f.order, "DeleteAccessGroup")
	r, ok := f.stored[name]
	f.mu.Unlock()
	if !ok {
		return nil
	}
	return f.DeleteAccessGroupByID(ctx, r.AccessGroupID)
}

// RevokeKey records the call for TestReconcileDeletionOrder and returns nil —
// the ek_-revocation tests that need error injection live elsewhere; this
// fake's LiteLLM state has no concept of environment_keys rows to revoke.
func (f *accessGroupFakeImpl) RevokeKey(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.order = append(f.order, "RevokeKey")
	return nil
}

// Resolver overrides — the reconciler calls these on each pass to build
// name→ID maps for the AccessGroupCreateRequest body.

// ListMCPServers mirrors the real RESTClient contract: an empty list is
// reported as litellm.ErrNotFound (REL-05 length-check), NOT an empty
// slice. Callers MUST translate ErrNotFound → empty. A prior empty-slice
// fake masked the env-controller bug where a LiteLLM with zero registered
// MCP servers wedged every Environment at AccessGroupSynced=False/
// ResolveFailed — see TestAccessGroupSynced_True_OnEmptyLiteLLMLists.
func (f *accessGroupFakeImpl) ListMCPServers(_ context.Context) ([]litellm.MCPServerEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.mcps) == 0 {
		return nil, litellm.ErrNotFound
	}
	out := make([]litellm.MCPServerEntry, 0, len(f.mcps))
	for name, id := range f.mcps {
		out = append(out, litellm.MCPServerEntry{ServerID: id, ServerName: name})
	}
	return out, nil
}

// ListA2AAgents mirrors the real RESTClient contract: empty → ErrNotFound.
func (f *accessGroupFakeImpl) ListA2AAgents(_ context.Context) ([]litellm.AgentEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.agents) == 0 {
		return nil, litellm.ErrNotFound
	}
	out := make([]litellm.AgentEntry, 0, len(f.agents))
	for name, id := range f.agents {
		out = append(out, litellm.AgentEntry{AgentID: id, AgentName: name})
	}
	return out, nil
}

func (f *accessGroupFakeImpl) ListTeamsByAlias(_ context.Context, alias string) ([]litellm.TeamListEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if entries, ok := f.teamsByAlias[alias]; ok {
		out := make([]litellm.TeamListEntry, len(entries))
		copy(out, entries)
		return out, nil
	}
	return nil, nil
}

// ListAllTeams returns every seeded team across all aliases, carrying
// the per-team access_group_ids mirror. This is the call the Environment
// reconciler uses to resolve authorizedTeams AND to read the mirror.
func (f *accessGroupFakeImpl) ListAllTeams(_ context.Context) ([]litellm.TeamListEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []litellm.TeamListEntry
	for _, entries := range f.teamsByAlias {
		for _, e := range entries {
			e.AccessGroupIDs = append([]string{}, f.teamMirror[e.TeamID]...)
			out = append(out, e)
		}
	}
	return out, nil
}

// CreateTeam / UpdateTeam / GetTeamInfo / DeleteTeam back the shell-team
// reconciler (environment_shellteam.go). teamsByAlias is reseeded here too
// so ListAllTeams (which flattens teamsByAlias) sees a freshly created shell.
func (f *accessGroupFakeImpl) CreateTeam(_ context.Context, req *litellm.NewTeamRequest) (*litellm.TeamListEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.order = append(f.order, "CreateTeam")
	if f.teamCreateErr != nil {
		return nil, f.teamCreateErr
	}
	f.teamCreateCalls[req.TeamAlias]++
	f.lastTeamCreate[req.TeamAlias] = *req
	id := "id-" + req.TeamAlias
	entry := litellm.TeamListEntry{
		TeamID:           id,
		TeamAlias:        req.TeamAlias,
		Models:           req.Models,
		ObjectPermission: req.ObjectPermission,
		Metadata:         marshalTeamMetadata(req.Metadata),
	}
	f.teamsByID[id] = entry
	f.teamsByAlias[req.TeamAlias] = []litellm.TeamListEntry{entry}
	return &entry, nil
}

func (f *accessGroupFakeImpl) UpdateTeam(_ context.Context, req *litellm.TeamUpdateRequest) (*litellm.TeamListEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.order = append(f.order, "UpdateTeam")
	f.teamUpdateCalls[req.TeamID]++
	entry := f.teamsByID[req.TeamID]
	entry.TeamID = req.TeamID
	entry.Models = req.Models
	entry.ObjectPermission = req.ObjectPermission
	if req.Metadata != nil {
		entry.Metadata = marshalTeamMetadata(req.Metadata)
	}
	f.teamsByID[req.TeamID] = entry
	if entry.TeamAlias != "" {
		f.teamsByAlias[entry.TeamAlias] = []litellm.TeamListEntry{entry}
	}
	if f.teamUpdateResult != nil {
		out := *f.teamUpdateResult
		return &out, nil
	}
	out := entry
	return &out, nil
}

// marshalTeamMetadata mirrors how the real RESTClient receives metadata back
// from LiteLLM: ACH sends a map[string]any on the request, LiteLLM echoes it
// as the TeamListEntry.Metadata json.RawMessage on read-back.
func marshalTeamMetadata(m map[string]any) json.RawMessage {
	if len(m) == 0 {
		return nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return raw
}

func (f *accessGroupFakeImpl) GetTeamInfo(_ context.Context, teamID string) (*litellm.TeamListEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.teamInfoErrByID[teamID]; err != nil {
		return nil, err
	}
	entry, ok := f.teamsByID[teamID]
	if !ok {
		return nil, nil
	}
	return &entry, nil
}

// InjectTeamInfoErr makes GetTeamInfo(teamID) fail — drives the
// entitledUserShellIDs resolveFailed guard test.
func (f *accessGroupFakeImpl) InjectTeamInfoErr(teamID string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.teamInfoErrByID[teamID] = err
}

// SeedTeamMembers registers a human team's members_with_roles (Task 4 —
// entitledUserShellIDs reads this via GetTeamInfo). Locked, unlike a direct
// teamsByID/teamsByAlias write: the envtest suite runs a real manager, so a
// lingering reconcile goroutine from an adjacent test can read these same
// maps concurrently with a new test's seeding.
func (f *accessGroupFakeImpl) SeedTeamMembers(teamID, alias string, members ...litellm.TeamMemberRole) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := litellm.TeamListEntry{TeamID: teamID, TeamAlias: alias, MembersWithRoles: members}
	f.teamsByID[teamID] = entry
	if alias != "" {
		f.teamsByAlias[alias] = []litellm.TeamListEntry{entry}
	}
}

// SeedUserShellPresent registers email's ach-user-<email> shell as already
// existing (team_id == alias, mirrors CreateTeam's convention) — locked
// seeding for the entitled-user-shell attachment tests (Task 4).
func (f *accessGroupFakeImpl) SeedUserShellPresent(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	alias := litellm.UserShellAlias(email)
	f.teamsByAlias[alias] = []litellm.TeamListEntry{{TeamID: alias, TeamAlias: alias}}
}

func (f *accessGroupFakeImpl) DeleteTeam(_ context.Context, teamID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.order = append(f.order, "DeleteTeam")
	f.teamDeleteCalls[teamID]++
	entry := f.teamsByID[teamID]
	delete(f.teamsByID, teamID)
	delete(f.teamsByAlias, entry.TeamAlias)
	return nil
}

func (f *accessGroupFakeImpl) CreateCallsFor(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCalls[name]
}

func (f *accessGroupFakeImpl) UpdateCallsFor(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.updateCalls[name]
}

func (f *accessGroupFakeImpl) LastCreate(name string) litellm.AccessGroupCreateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastCreate[name]
}

func (f *accessGroupFakeImpl) SeedMCP(name, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mcps[name] = id
}

func (f *accessGroupFakeImpl) SeedAgent(name, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents[name] = id
}

// SeedTeam registers a team under an alias with an EMPTY mirror. Pair it
// with SeedTeamMirror to model a healthy binding.
func (f *accessGroupFakeImpl) SeedTeam(alias, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.teamsByAlias[alias] = append(f.teamsByAlias[alias], litellm.TeamListEntry{TeamID: id, TeamAlias: alias})
}

// SeedTeamMirror sets team.access_group_ids for one team — the LiteLLM
// side that actually enforces grants. Tests use it to model a mirror
// that agrees with, or diverges from, access_group.assigned_team_ids.
func (f *accessGroupFakeImpl) SeedTeamMirror(teamID string, accessGroupIDs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.teamMirror[teamID] = append([]string{}, accessGroupIDs...)
}

// SeedShellTeam pre-registers env's deny-all shell team as already existing
// and healthy (sentinels intact), using the same "id-<alias>" convention
// CreateTeam assigns. Lets a test model a fully-converged Environment (shell
// already created) instead of exercising ensureShellTeam's create path.
// Pair with SeedTeamMirror(id, ...) to also mark its group binding healthy.
func (f *accessGroupFakeImpl) SeedShellTeam(env string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	alias := litellm.ShellTeamAlias(env)
	id := "id-" + alias
	entry := litellm.TeamListEntry{
		TeamID:           id,
		TeamAlias:        alias,
		Models:           []string{litellm.ShellTeamDenyAllModel},
		ObjectPermission: litellm.ShellTeamPermissions(),
	}
	f.teamsByID[id] = entry
	f.teamsByAlias[alias] = []litellm.TeamListEntry{entry}
	return id
}

// SeedExisting pre-populates the stored access-group state for
// drift-correction tests. The seeded response must carry a stable
// AccessGroupID ("ag-uuid-<name>" by convention).
func (f *accessGroupFakeImpl) SeedExisting(resp *litellm.AccessGroupResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	clone := *resp
	f.stored[resp.AccessGroupName] = &clone
}

func (f *accessGroupFakeImpl) InjectCreateErr(name string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createErrByName[name] = err
}

func (f *accessGroupFakeImpl) InjectUpdateErr(name string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateErrByName[name] = err
}

func cloneMirror(m map[string][]string) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = append([]string{}, v...)
	}
	return out
}

// MirrorEverEmpty reports whether teamID's mirror was empty in ANY
// post-PUT snapshot — the blast-radius assertion for a co-authorized
// healthy team during a repair sequence.
func (f *accessGroupFakeImpl) MirrorEverEmpty(teamID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, snap := range f.mirrorHistory {
		if len(snap[teamID]) == 0 {
			return true
		}
	}
	return false
}

// Mirror returns the current access_group_ids for one team.
func (f *accessGroupFakeImpl) Mirror(teamID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.teamMirror[teamID]...)
}

func (f *accessGroupFakeImpl) LastUpdate(name string) litellm.AccessGroupUpdateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastUpdate[name]
}

// FreezeMirror simulates a LiteLLM whose delta-driven mirror write has
// disappeared — PUTs land but the mirror never moves. Drives the
// MirrorUnconverged guard test.
func (f *accessGroupFakeImpl) FreezeMirror() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mirrorFrozen = true
}

var _ litellm.Client = (*accessGroupFakeImpl)(nil)
