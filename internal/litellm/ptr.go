// SPDX-License-Identifier: Apache-2.0

package litellm

// BoolPtr returns a pointer to b. Used for optional tri-state request
// fields (nil = omit/keep server default, &false / &true = explicit).
func BoolPtr(b bool) *bool { return &b }
