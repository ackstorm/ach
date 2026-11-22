// SPDX-License-Identifier: Apache-2.0

package merge

import "fmt"

// EnsureArrayEntry adds entry to the top-level array doc[key] when absent,
// keeping every other element. A non-array value is an error, never replaced.
func EnsureArrayEntry(doc map[string]any, key, entry string) (bool, error) {
	arr, err := arrayAt(doc, key)
	if err != nil {
		return false, err
	}
	for _, v := range arr {
		if v == entry {
			return false, nil
		}
	}
	doc[key] = append(arr, entry)
	return true, nil
}

// RemoveArrayEntry drops entry from doc[key] and deletes the key when the
// array becomes empty.
func RemoveArrayEntry(doc map[string]any, key, entry string) (bool, error) {
	arr, err := arrayAt(doc, key)
	if err != nil {
		return false, err
	}
	out := arr[:0:0]
	for _, v := range arr {
		if v != entry {
			out = append(out, v)
		}
	}
	if len(out) == len(arr) {
		return false, nil
	}
	if len(out) == 0 {
		delete(doc, key)
	} else {
		doc[key] = out
	}
	return true, nil
}

func arrayAt(doc map[string]any, key string) ([]any, error) {
	v, ok := doc[key]
	if !ok || v == nil {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%q is not an array", key)
	}
	return arr, nil
}
