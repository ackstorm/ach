// SPDX-License-Identifier: LicenseRef-ACKstorm-EE

package classifier

import "os"

// FromEnv reads ACH_CLASSIFIER_TAXONOMY_FILE: unset is the built-in
// taxonomy. The classifier and platform-api read the same file.
func FromEnv() (Taxonomy, error) {
	p := os.Getenv("ACH_CLASSIFIER_TAXONOMY_FILE")
	if p == "" {
		return Builtin, nil
	}
	return Load(p)
}
