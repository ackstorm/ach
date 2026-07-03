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
	f.listErr = nil
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
		found.AssignedTeamIDs = append([]string{}, req.AssignedTeamIDs...)
	}
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
	r, ok := f.stored[name]
	f.mu.Unlock()
	if !ok {
		return nil
	}
	return f.DeleteAccessGroupByID(ctx, r.AccessGroupID)
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

// ListAllTeams returns every team across all aliases — interface
// compliance; no controller test exercises it.
func (f *accessGroupFakeImpl) ListAllTeams(_ context.Context) ([]litellm.TeamListEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []litellm.TeamListEntry
	for _, entries := range f.teamsByAlias {
		out = append(out, entries...)
	}
	return out, nil
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

func (f *accessGroupFakeImpl) SeedTeam(alias, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.teamsByAlias[alias] = append(f.teamsByAlias[alias], litellm.TeamListEntry{TeamID: id, TeamAlias: alias})
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

var _ litellm.Client = (*accessGroupFakeImpl)(nil)
