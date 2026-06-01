// SPDX-License-Identifier: Apache-2.0

package route

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
)

// SplitFrontmatter splits a markdown document into its raw YAML frontmatter
// region and its body (D-24). It is a promoted, package-shared copy of codex's
// findFrontmatterFences / startsWithFrontmatterFence fence logic (CRLF-aware,
// byte-stable): the returned frontmatter is the bytes BETWEEN the opening
// "---" fence + newline and the closing "---" line (both fences excluded), and
// body is everything AFTER the closing fence + newline.
//
// found is true only when the input opens with a well-formed "---\n" (or
// "---\r\n") fence AND a closing "---" line is located. When found is false the
// input does not carry frontmatter: frontmatter is nil and body is the whole
// input unchanged (so callers can pass arbitrary content through unharmed).
//
// The Wave-2 opencode agent Transform (03-03) consumes this to lift the
// frontmatter, restructure tools[]→{name:true}, and re-emit via
// EncodeFrontmatterDoc.
func SplitFrontmatter(in []byte) (frontmatter []byte, body []byte, found bool) {
	if !startsWithFrontmatterFence(in) {
		return nil, in, false
	}
	openEnd, closeStart, closeEnd, ok := findFrontmatterFences(in)
	if !ok {
		return nil, in, false
	}
	return in[openEnd:closeStart], in[closeEnd:], true
}

// startsWithFrontmatterFence reports whether raw begins with a YAML
// frontmatter opening fence ("---\n" or "---\r\n"). Pure prefix check.
// (Promoted copy of codex's identical helper — D-24.)
func startsWithFrontmatterFence(raw []byte) bool {
	if len(raw) < 4 {
		return false
	}
	if raw[0] != '-' || raw[1] != '-' || raw[2] != '-' {
		return false
	}
	if raw[3] == '\n' {
		return true
	}
	if len(raw) >= 5 && raw[3] == '\r' && raw[4] == '\n' {
		return true
	}
	return false
}

// findFrontmatterFences locates the byte offsets of the opening and closing
// YAML frontmatter fences in raw. Returns openEnd (first byte of frontmatter
// content), closeStart (first byte of the closing fence), closeEnd (first byte
// of the body), and found. The caller MUST have verified
// startsWithFrontmatterFence(raw) first. (Promoted copy of codex's identical
// helper — D-24.)
func findFrontmatterFences(raw []byte) (openEnd, closeStart, closeEnd int, found bool) {
	switch raw[3] {
	case '\n':
		openEnd = 4
	case '\r':
		openEnd = 5
	}

	i := openEnd
	for i < len(raw) {
		lineStart := i
		for i < len(raw) && raw[i] != '\n' {
			i++
		}
		lineEnd := i
		lineContent := raw[lineStart:lineEnd]
		if len(lineContent) > 0 && lineContent[len(lineContent)-1] == '\r' {
			lineContent = lineContent[:len(lineContent)-1]
		}
		if bytes.Equal(lineContent, []byte("---")) {
			closeStart = lineStart
			closeEnd = i
			if closeEnd < len(raw) && raw[closeEnd] == '\n' {
				closeEnd++
			}
			return openEnd, closeStart, closeEnd, true
		}
		if i < len(raw) && raw[i] == '\n' {
			i++
		}
	}
	return 0, 0, 0, false
}

// EncodeFrontmatterDoc re-emits a markdown document from a frontmatter map +
// body as `---\n<sorted-key YAML>\n---\n<body>` (D-24, reactivating the
// deferred Phase-2 D-05). The encoder is DETERMINISTIC: keys are sorted
// lexicographically and scalar/sequence/map values are formatted stably, so
// repeated calls on identical input are byte-identical (mirroring the
// CanonicalJSON / CanonicalTOML determinism contract in encode.go) — the
// VER-03 idempotence precondition.
//
// The opencode agent Transform needs only a minimal value grammar, so the
// supported value types are deliberately narrow:
//
//   - scalars: string, bool, and the integer kinds (int / int64 / float64
//     with an integral value — JSON/YAML decode numbers as float64);
//   - string slices ([]string and []any whose elements are all strings),
//     emitted as a YAML block sequence;
//   - string→bool maps (map[string]bool and map[string]any whose values are
//     all bool), emitted as a sorted nested mapping — this is the
//     tools[]→{name:true} shape opencode produces.
//
// Any other value type returns an error rather than emitting nondeterministic
// or lossy output. All string emission goes through strconv.Quote so a value
// containing YAML metacharacters cannot break out of its field (T-03-03).
func EncodeFrontmatterDoc(frontmatter map[string]any, body []byte) ([]byte, error) {
	keys := make([]string, 0, len(frontmatter))
	for k := range frontmatter {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("---\n")
	for _, k := range keys {
		if err := encodeFrontmatterEntry(&buf, k, frontmatter[k]); err != nil {
			return nil, fmt.Errorf("route: encode frontmatter key %q: %w", k, err)
		}
	}
	buf.WriteString("---\n")
	buf.Write(body)
	return buf.Bytes(), nil
}

// encodeFrontmatterEntry emits one top-level `key: value` (or `key:` + nested
// block) for the supported value grammar.
func encodeFrontmatterEntry(buf *bytes.Buffer, key string, val any) error {
	switch v := val.(type) {
	case string:
		buf.WriteString(key + ": " + yamlScalarString(v) + "\n")
		return nil
	case bool:
		buf.WriteString(key + ": " + strconv.FormatBool(v) + "\n")
		return nil
	case int:
		buf.WriteString(key + ": " + strconv.Itoa(v) + "\n")
		return nil
	case int64:
		buf.WriteString(key + ": " + strconv.FormatInt(v, 10) + "\n")
		return nil
	case float64:
		if v == float64(int64(v)) {
			buf.WriteString(key + ": " + strconv.FormatInt(int64(v), 10) + "\n")
			return nil
		}
		return fmt.Errorf("non-integral float value %v unsupported", v)
	case []string:
		return encodeStringSlice(buf, key, v)
	case []any:
		strs := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return fmt.Errorf("sequence element %v (%T) is not a string", e, e)
			}
			strs = append(strs, s)
		}
		return encodeStringSlice(buf, key, strs)
	case map[string]bool:
		return encodeStringBoolMap(buf, key, v)
	case map[string]any:
		conv := make(map[string]bool, len(v))
		for mk, mv := range v {
			b, ok := mv.(bool)
			if !ok {
				return fmt.Errorf("map value for %q (%T) is not a bool", mk, mv)
			}
			conv[mk] = b
		}
		return encodeStringBoolMap(buf, key, conv)
	default:
		return fmt.Errorf("unsupported value type %T", val)
	}
}

// encodeStringSlice emits a YAML block sequence under key, preserving the given
// element order (the caller is responsible for any ordering it wants stable).
func encodeStringSlice(buf *bytes.Buffer, key string, vals []string) error {
	buf.WriteString(key + ":\n")
	for _, e := range vals {
		buf.WriteString("  - " + yamlScalarString(e) + "\n")
	}
	return nil
}

// encodeStringBoolMap emits a nested mapping under key with the keys sorted
// lexicographically (deterministic — the tools[]→{name:true} shape).
func encodeStringBoolMap(buf *bytes.Buffer, key string, m map[string]bool) error {
	buf.WriteString(key + ":\n")
	subKeys := make([]string, 0, len(m))
	for sk := range m {
		subKeys = append(subKeys, sk)
	}
	sort.Strings(subKeys)
	for _, sk := range subKeys {
		buf.WriteString("  " + yamlScalarString(sk) + ": " + strconv.FormatBool(m[sk]) + "\n")
	}
	return nil
}

// yamlScalarString renders a string as a stable, safe YAML scalar. It always
// double-quotes via strconv.Quote so a value carrying YAML metacharacters
// (`:`, `#`, `-`, leading/trailing space, etc.) cannot escape its field
// (T-03-03) and the emission is byte-stable across runs.
func yamlScalarString(s string) string {
	return strconv.Quote(s)
}
