// SPDX-License-Identifier: Apache-2.0

// Package synthetic enforces "synthetic mode": ACH_URL + ACH_KEY both set,
// so ach-cli runs without a config file (CI, containers). The Hub URL comes
// from ACH_URL and the credential from ACH_KEY (a raw ek-…), or from a raw
// --key ek-… that overrides it. Once active:
//
//   - login, logout, token and every profile command exit 1 (there is no
//     config file to read or write).
//   - keys create exits 1 unless --no-save (nowhere to save the key).
//   - --profile / ACH_PROFILE exit 1 (there is no profile to pick).
//   - --key <name> exits 1 (a saved name needs a profile to look it up in).
//
// ACH_URL alone is not synthetic mode: it only pre-fills the login URL.
package synthetic
