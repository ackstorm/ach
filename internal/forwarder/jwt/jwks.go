// SPDX-License-Identifier: Apache-2.0

package jwt

import (
	"encoding/json"
	"net/http"
)

// jwksDoc is the wire shape of the JWK Set per RFC 7517 §5 — a JSON
// object with a single "keys" array. We render this from the Signer's
// JWKS() result, normalizing a nil slice to an empty array so
// downstream consumers (backend JWKS clients) never see {"keys":null}.
type jwksDoc struct {
	Keys []JWK `json:"keys"`
}

// JWKSHandler returns an http.HandlerFunc that publishes the signer's
// current + optional next slot as a JWK Set per FWD-08 / Hub §9.2.
//
// Response invariants:
//
//   - Content-Type: application/jwk-set+json (RFC 7517 §8.5.1; backends
//     match this to pin "Ed25519 JWS verification keys")
//   - Cache-Control: public, max-age=3600 (Hub §9.2; the 1-hour cache
//     TTL is what forces the ≥24h rotation overlap of FWD-09 — backends
//     can hold a stale JWKS view for up to an hour after a rotation
//     step, so ≥24h overlap is ≥24× the cache TTL).
//   - Status: 200 (the route is anonymous and ALWAYS-on; an empty signer
//     emits {"keys":[]} with 200 OK rather than 503, so backends polling
//     during forwarder startup don't see transient failures that poison
//     their cache).
//   - Body: a JWK Set JSON document. The "keys" array contains 0..2
//     JWKs (current + optional next; current first when present).
//
// The handler is anonymous: backends fetch server-to-server and never
// authenticate to this route. Authentication middleware (Authn) MUST
// be wired OUTSIDE the path that contains /.well-known/jwks.json (the
// Plan 04-08 server.go registers this route OUTSIDE the Authn group
// per D-02).
func JWKSHandler(signer Signer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := jwksDoc{Keys: signer.JWKS()}
		if out.Keys == nil {
			out.Keys = []JWK{} // render as "keys":[] rather than "keys":null
		}
		w.Header().Set("Content-Type", "application/jwk-set+json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(out)
	}
}
