// opencode plugin: SSO (OAuth) login for the platform's model provider.
//
//   opencode plugin https://<ach>/platform/opencode-auth -g
//   opencode auth login -p ackstorm
//
// PROVIDER is the provider id in the served api.json, not branding; the user
// sees only "SSO (browser)".
//
// Nothing is configured here. The provider's API URL comes from opencode (the
// served api.json); the authorization server comes from that URL's RFC 9728
// document; endpoints and scope from there. Every request then carries a fresh
// access token in Authorization, which the platform gateway maps to the user's
// LiteLLM key.
//
// opencode stores the tokens (~/.local/share/opencode/auth.json) but never
// refreshes them: the `fetch` returned by `loader` does, per request, the way
// opencode's own Anthropic plugin does.
import { createServer } from "node:http"
import { mkdir, readFile, writeFile } from "node:fs/promises"
import { randomBytes, createHash } from "node:crypto"

const PROVIDER = "ackstorm"
const DATA = `${process.env.XDG_DATA_HOME ?? `${process.env.HOME}/.local/share`}/opencode`
const CLIENT_FILE = `${DATA}/ackstorm-client.json` // DCR result; opencode has no plugin KV
const b64 = (b) => Buffer.from(b).toString("base64url")

async function json(url, init) {
  const r = await fetch(url, { signal: AbortSignal.timeout(15_000), ...init })
  if (!r.ok) throw new Error(`${r.status} ${url}`)
  return r.json()
}

// Provider API URL (opencode) -> protected-resource document (RFC 9728) ->
// authorization-server metadata (RFC 8414). Once per process.
let discovered
function discover(client) {
  return (discovered ??= (async () => {
    const { data } = await client.config.providers()
    const provider = data?.providers?.find((p) => p.id === PROVIDER)
    const api = Object.values(provider?.models ?? {})[0]?.api?.url ?? provider?.options?.baseURL
    if (!api) throw new Error(`provider ${PROVIDER} has no API URL`)
    const u = new URL(api)
    const prm = await json(`${u.origin}/.well-known/oauth-protected-resource${u.pathname.replace(/\/$/, "")}`)
    const issuer = prm.authorization_servers[0]
    const as = await json(`${issuer}/.well-known/oauth-authorization-server`)
    if (as.issuer !== issuer) throw new Error(`issuer mismatch: ${as.issuer} != ${issuer}`) // RFC 8414 §3.3
    return { issuer, as, scope: (prm.scopes_supported ?? []).join(" ") }
  })().catch((e) => { discovered = undefined; throw e })) // a blip must not poison the process
}

// The saved DCR identity, or null. A refresh token belongs to the client that
// obtained it, so a refresh must never register a new one (login does).
async function savedClientId(issuer) {
  try {
    const saved = JSON.parse(await readFile(CLIENT_FILE, "utf8"))
    if (saved.issuer === issuer) return saved.client_id
  } catch {}
  return null
}

async function clientId({ issuer, as }) {
  const saved = await savedClientId(issuer)
  if (saved) return saved
  const { client_id } = await json(as.registration_endpoint, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      client_name: "opencode",
      // Loopback: the AS ignores the port (RFC 8252 §7.3), so one registration
      // serves whichever port the listener gets.
      redirect_uris: ["http://127.0.0.1/callback"],
      grant_types: ["authorization_code", "refresh_token"],
      response_types: ["code"],
      token_endpoint_auth_method: "none",
    }),
  })
  await mkdir(DATA, { recursive: true })
  await writeFile(CLIENT_FILE, JSON.stringify({ issuer, client_id }), { mode: 0o600 })
  return client_id
}

async function token({ as }, form) {
  const j = await json(as.token_endpoint, {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(form),
  })
  return { access: j.access_token, refresh: j.refresh_token, expires: Date.now() + j.expires_in * 1000 }
}

// Loopback listener on a random port; resolves the code once, then closes.
function listen(state) {
  let done
  const code = new Promise((res, rej) => (done = { res, rej }))
  const server = createServer((req, res) => {
    const u = new URL(req.url, "http://127.0.0.1")
    if (u.pathname !== "/callback") return res.writeHead(404).end()
    // A stray hit must not consume the listener: the real callback is still coming.
    if (u.searchParams.get("state") !== state) return res.writeHead(400).end("state mismatch")
    res.end("Authorisation received. You can close this tab.")
    server.close()
    const c = u.searchParams.get("code")
    c ? done.res(c) : done.rej(new Error(u.searchParams.get("error") ?? "no code"))
  })
  setTimeout(() => { server.close(); done.rej(new Error("login timed out")) }, 5 * 60_000).unref()
  code.catch(() => {}) // marks the rejection handled if the user abandons the login
  const port = new Promise((res, rej) => {
    server.once("error", rej)
    server.listen(0, "127.0.0.1", () => res(server.address().port))
  })
  return { port, code }
}

export async function SsoAuth({ client }) {
  let refreshing // ponytail: one in-flight refresh; a lost race spends a rotated refresh token
  return {
    auth: {
      provider: PROVIDER,
      async loader(getAuth) {
        return {
          apiKey: "", // falsy: openai-compatible adds no Authorization; fetch sets it per request
          async fetch(input, init) {
            let auth = await getAuth()
            if (auth?.type !== "oauth") return fetch(input, init)
            if (auth.expires < Date.now() + 60_000) {
              refreshing ??= (async () => {
                // Re-read: a caller that read stale auth just after the previous
                // refresh cleared would otherwise spend an already-rotated token.
                const cur = await getAuth()
                if (cur?.type === "oauth" && cur.expires >= Date.now() + 60_000) return cur
                const d = await discover(client)
                const client_id = await savedClientId(d.issuer)
                if (!client_id) throw new Error(`SSO client identity lost, run \`opencode auth login -p ${PROVIDER}\``)
                const t = await token(d, { grant_type: "refresh_token", refresh_token: cur.refresh, client_id })
                t.refresh ||= cur.refresh
                await client.auth.set({ path: { id: PROVIDER }, body: { type: "oauth", ...t } })
                return t
              })().finally(() => { refreshing = undefined })
              auth = await refreshing
            }
            const req = new Request(input, init) // normalises url/Request + any headers shape
            req.headers.set("authorization", `Bearer ${auth.access}`)
            return fetch(req)
          },
        }
      },
      methods: [
        {
          type: "oauth",
          label: "SSO (browser)",
          async authorize() {
            const d = await discover(client)
            const verifier = b64(randomBytes(32))
            const state = b64(randomBytes(16))
            const client_id = await clientId(d) // before the listener: a failure here must not leave a port waiting
            const { port, code } = listen(state)
            const redirect_uri = `http://127.0.0.1:${await port}/callback`
            const url = new URL(d.as.authorization_endpoint)
            url.search = new URLSearchParams({
              response_type: "code",
              client_id,
              redirect_uri,
              scope: d.scope,
              state,
              code_challenge: b64(createHash("sha256").update(verifier).digest()),
              code_challenge_method: "S256",
            })
            return {
              url: url.toString(),
              method: "auto",
              instructions: "Open the URL in your browser and sign in with your organization account.",
              async callback() {
                try {
                  const t = await token(d, { grant_type: "authorization_code", code: await code, redirect_uri, client_id, code_verifier: verifier })
                  return { type: "success", ...t }
                } catch {
                  return { type: "failed" }
                }
              },
            }
          },
        },
      ],
    },
  }
}
