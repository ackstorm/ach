// SPDX-License-Identifier: Apache-2.0

package route

import (
	"bytes"
	"runtime"
	"strings"

	"github.com/ackstorm/ach/internal/cli/adapter"
)

// pluginBaseName drops the "@<marketplace>" suffix a plugin directory carries
// (bro@bro → bro, git@ackstorm-defaults → git).
func pluginBaseName(plugin string) string {
	if i := strings.IndexByte(plugin, '@'); i >= 0 {
		return plugin[:i]
	}
	return plugin
}

// applyPrefix namespaces one matched file of a plugin tree (a no-op without a
// plugin or a Prefix); dest is its already-remapped destination. It returns the new destination, the (possibly
// renamed) content, and for a user-only SKILL.md on a rule with
// UserOnlyCommandDir the extra command write.
func applyPrefix(rule Rule, plugin, dest string, content []byte) (string, []byte, *adapter.FileWrite) {
	if plugin == "" || rule.Prefix == PrefixNone {
		return dest, content, nil
	}
	anchor := globAnchor(rule.ToGlob)
	suffix := strings.TrimPrefix(strings.TrimPrefix(dest, anchor), "/")
	if suffix == "" || suffix == dest {
		return dest, content, nil
	}
	first, tail, _ := strings.Cut(suffix, "/")

	switch rule.Prefix {
	case PrefixSkill:
		// Only files INSIDE a skill directory: skills/<s>/<file>.
		if tail == "" {
			return dest, content, nil
		}
		newDest := anchor + "/" + plugin + rule.Sep + first + "/" + tail
		nameSep := rule.Sep
		if rule.NameSep != "" {
			nameSep = rule.NameSep
		}
		name := plugin + nameSep + first
		if tail != "SKILL.md" {
			return newDest, content, nil
		}
		var extra *adapter.FileWrite
		if rule.UserOnlyCommandDir != "" && userOnly(content) {
			extra = &adapter.FileWrite{
				Path:    rule.UserOnlyCommandDir + "/" + plugin + rule.Sep + first + ".md",
				Content: skillAsCommand(content),
				Merge:   adapter.MergeReplace,
			}
		}
		return newDest, renameFrontmatterName(content, name), extra

	case PrefixCommand:
		if rule.Sep == "/" {
			return anchor + "/" + plugin + "/" + suffix, content, nil
		}
		return anchor + "/" + plugin + rule.Sep + suffix, content, nil
	}
	return dest, content, nil
}

// frontmatterLines returns the frontmatter split into lines plus the line
// terminator in use, or ok=false when the document has none.
func frontmatterLines(in []byte) (fm []byte, body []byte, nl string, ok bool) {
	fm, body, ok = SplitFrontmatter(in)
	nl = "\n"
	if bytes.HasPrefix(in, []byte("---\r\n")) {
		nl = "\r\n"
	}
	return fm, body, nl, ok
}

// topLevelKey reports the key of a top-level `key: value` YAML line.
func topLevelKey(line string) string {
	if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
		return ""
	}
	k, _, found := strings.Cut(line, ":")
	if !found {
		return ""
	}
	return strings.TrimSpace(k)
}

func scalarValue(line string) string {
	_, v, _ := strings.Cut(line, ":")
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		v = v[1 : len(v)-1]
	}
	return v
}

// renameFrontmatterName sets the frontmatter `name:` to name, replacing only
// that line so every other byte (comments, order, quoting) is untouched; it
// adds the line, or a whole frontmatter block, when absent.
func renameFrontmatterName(in []byte, name string) []byte {
	fm, body, nl, ok := frontmatterLines(in)
	if !ok {
		return append([]byte("---\n"+"name: "+name+"\n---\n"), in...)
	}
	lines := strings.Split(strings.TrimSuffix(string(fm), nl), nl)
	replaced := false
	for i, l := range lines {
		if topLevelKey(l) == "name" {
			lines[i] = "name: " + name
			replaced = true
			break
		}
	}
	if !replaced {
		lines = append([]string{"name: " + name}, lines...)
	}
	return []byte("---" + nl + strings.Join(lines, nl) + nl + "---" + nl + string(body))
}

// userOnly reports whether a SKILL.md opts out of model invocation.
func userOnly(in []byte) bool {
	fm, _, nl, ok := frontmatterLines(in)
	if !ok {
		return false
	}
	for _, l := range strings.Split(string(fm), nl) {
		if topLevelKey(l) == "disable-model-invocation" {
			return strings.EqualFold(scalarValue(l), "true")
		}
	}
	return false
}

// skillAsCommand turns a SKILL.md into a command file: its description (when
// it is a one-line scalar) as the only frontmatter key, and the skill body as
// the prompt.
func skillAsCommand(in []byte) []byte {
	fm, body, nl, _ := frontmatterLines(in)
	for _, l := range strings.Split(string(fm), nl) {
		if topLevelKey(l) != "description" {
			continue
		}
		if d := scalarValue(l); d != "" && d != ">-" && d != ">" && d != "|" && d != "|-" {
			return []byte("---" + nl + "description: " + d + nl + "---" + nl + string(body))
		}
	}
	return body
}

// SkillDirSep is the separator for a Claude Code skill directory: ":" gives
// the native-plugin form /<plugin>:<skill>, but Windows forbids ":" in a file
// name, so there the directory takes "-" (the frontmatter name still carries
// ":" — Claude Code registers both, measured on 2.1.310).
var SkillDirSep = skillDirSep(runtime.GOOS)

func skillDirSep(goos string) string {
	if goos == "windows" {
		return "-"
	}
	return ":"
}
