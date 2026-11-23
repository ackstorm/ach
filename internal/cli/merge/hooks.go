// SPDX-License-Identifier: Apache-2.0

package merge

import "fmt"

// EnsureHookCommand adds a matcher group holding handler to doc.hooks[event]
// unless a handler with the same command is already there. Every other group
// and handler stays. A non-object or non-array value on the path is an error,
// never replaced.
func EnsureHookCommand(doc map[string]any, event string, handler map[string]any) (bool, error) {
	hooks, groups, err := hookGroups(doc, event)
	if err != nil {
		return false, err
	}
	command := handler["command"]
	for _, g := range groups {
		handlers, err := groupHandlers(g)
		if err != nil {
			return false, err
		}
		for _, h := range handlers {
			if m, ok := h.(map[string]any); ok && m["command"] == command {
				return false, nil
			}
		}
	}
	if hooks == nil {
		hooks = map[string]any{}
		doc["hooks"] = hooks
	}
	hooks[event] = append(groups, map[string]any{"hooks": []any{handler}})
	return true, nil
}

// RemoveHookCommand drops every handler running command from doc.hooks[event],
// then any group, event list and hooks object it left empty.
func RemoveHookCommand(doc map[string]any, event, command string) (bool, error) {
	hooks, groups, err := hookGroups(doc, event)
	if err != nil || hooks == nil {
		return false, err
	}
	changed := false
	kept := groups[:0:0]
	for _, g := range groups {
		handlers, err := groupHandlers(g)
		if err != nil {
			return false, err
		}
		rest := handlers[:0:0]
		for _, h := range handlers {
			if m, ok := h.(map[string]any); ok && m["command"] == command {
				changed = true
				continue
			}
			rest = append(rest, h)
		}
		if len(rest) == len(handlers) {
			kept = append(kept, g)
		} else if len(rest) > 0 {
			g.(map[string]any)["hooks"] = rest
			kept = append(kept, g)
		}
	}
	if !changed {
		return false, nil
	}
	switch {
	case len(kept) > 0:
		hooks[event] = kept
	case len(hooks) > 1:
		delete(hooks, event)
	default:
		delete(doc, "hooks")
	}
	return true, nil
}

func hookGroups(doc map[string]any, event string) (map[string]any, []any, error) {
	v, ok := doc["hooks"]
	if !ok || v == nil {
		return nil, nil, nil
	}
	hooks, ok := v.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf(`"hooks" is not an object`)
	}
	groups, err := arrayAt(hooks, event)
	if err != nil {
		return nil, nil, fmt.Errorf("hooks.%w", err)
	}
	return hooks, groups, nil
}

func groupHandlers(g any) ([]any, error) {
	m, ok := g.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("a hook group is not an object")
	}
	return arrayAt(m, "hooks")
}
