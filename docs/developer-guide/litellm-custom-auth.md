# LiteLLM Custom Auth & Impersonation Configuration Guide

This guide details how to configure **LiteLLM** to support the secure, zero-leak key impersonation protocol used by the ACH **Forwarder**.

---

## 1. Overview & Architecture

### The Security Challenge
Under standard LiteLLM operations:
1. Accessing LiteLLM with the **Master Key** acts as a super-admin, bypassing all user-level constraints, budgets, and rate limits.
2. Direct client authentication requires LiteLLM **Virtual Key plaintext** (e.g., `sk-xxxx...`).
3. To adhere to strict security practices (**FIX01 §A.6**), **ACH never stores or supplies the plaintext of these LiteLLM virtual keys**. We only store the stable, hashed/opaque LiteLLM identifier (`token`).

### The Impersonation Solution
To secure the communication without storing plaintext, we use an **impersonation protocol**:
1. The **Forwarder** receives the request and validates the ACH-specific key (`pk_...` or `ek_...`).
2. It strips all client-supplied headers and attaches:
   - `X-Litellm-Api-Key` set to the **LiteLLM Master Key** (verifying forwarder trust).
   - `X-Litellm-Key-Id` set to the **Virtual Key ID (token)**.
3. **LiteLLM Custom Auth Hook** intercepts the request:
   - It verifies the `X-Litellm-Api-Key` matches the known Master Key.
   - It queries the LiteLLM database to fetch the metadata, limits, and team associations of the virtual key using the `X-Litellm-Key-Id`.
   - It returns the verified user context (`UserAPIKeyAuth`), forcing LiteLLM to run the request **fully impersonating that virtual key** (enforcing its specific budgets, routing, and rate limits).

```
┌───────────┐  /v1/chat/completions  ┌───────────┐  X-Litellm-Api-Key: <MasterKey>  ┌─────────────┐
│  Client   │───────────────────────▶│ Forwarder │─────────────────────────────────▶│   LiteLLM   │
│ (pk_/ek_) │   (Auth: Bearer ACH)   │  Service  │  X-Litellm-Key-Id: <TokenID>    │ (CustomAuth)│
└───────────┘                        └───────────┘                                  └──────┬──────┘
                                                                                           │ Impersonates
                                                                                           ▼
                                                                                    [Apply Limits,
                                                                                     Budgets, Teams]
```

---

## 2. Custom Auth Hook Implementation (`auth_user_map.py`)

This Python script is the custom authentication adapter deployed alongside LiteLLM. It overrides LiteLLM's standard authentication pipeline for requests originating from the trusted Forwarder.

```python
# SPDX-License-Identifier: Apache-2.0
import os
import json
from datetime import datetime, timezone
from fastapi import Request, HTTPException
from litellm.proxy._types import UserAPIKeyAuth

# LITELLM_MASTER_KEY is the standard environment variable injected by the Helm chart
MASTER_KEY = os.getenv("LITELLM_MASTER_KEY") or os.getenv("PROXY_MASTER_KEY")

def clean_bearer(val: str) -> str:
    if not val:
        return ""
    if val.startswith("Bearer "):
        return val[7:]
    if val.startswith("bearer "):
        return val[7:]
    return val

async def sso_key_swapper(request: Request, api_key: str) -> UserAPIKeyAuth:
    """
    Intercepts requests using the Master Key and swaps their context to the
    impersonated virtual key specified in the X-Litellm-Key-Id header.
    """
    # Import proxy_server inside the function to prevent cyclic import deadlock during module loading
    from litellm.proxy import proxy_server 

    # 1. Check if the incoming request is authenticated using our trusted Master Key (either as bearer or x-litellm-api-key)
    header_api_key = request.headers.get("x-litellm-api-key")
    clean_api_key = clean_bearer(api_key)
    clean_header_api_key = clean_bearer(header_api_key)

    if clean_api_key == MASTER_KEY or clean_header_api_key == MASTER_KEY:
        # 2. Extract the Virtual Key ID (token) supplied by our Forwarder
        litellm_key_id = request.headers.get("x-litellm-key-id")
        if not litellm_key_id:
            # If no key-id is provided, fall back to standard LiteLLM auth
            raise Exception("fallback")
            
        if not proxy_server.prisma_client or not proxy_server.prisma_client.db:
            raise Exception("fallback")
            
        # 3. Lookup the key record in the database by its stable opaque token ID
        target_key = await proxy_server.prisma_client.db.litellm_verificationtoken.find_unique(
            where={
                "token": litellm_key_id
            }
        )
        
        if not target_key:
            raise HTTPException(
                status_code=403, 
                detail=f"Access denied: No active LiteLLM key found for key ID '{litellm_key_id}'."
            )

        # 4. Enforce blocking/revocation checks
        if getattr(target_key, 'blocked', False) is True:
            raise HTTPException(
                status_code=403,
                detail="Access denied: The requested key is blocked."
            )

        # 5. Enforce expiration checks
        now_utc = datetime.now(timezone.utc)
        if target_key.expires is not None:
            key_expires = target_key.expires
            if key_expires.tzinfo is None:
                key_expires = key_expires.replace(tzinfo=timezone.utc)
            if now_utc >= key_expires:
                raise HTTPException(
                    status_code=403,
                    detail="Access denied: The requested key has expired."
                )

        # 6. Parse metadata payload
        parsed_metadata = {}
        meta = target_key.metadata
        if isinstance(meta, str):
            try:
                parsed_metadata = json.loads(meta)
            except json.JSONDecodeError:
                pass
        elif isinstance(meta, dict):
            parsed_metadata = meta

        # 7. Construct and return the verified UserAPIKeyAuth object
        # LiteLLM uses this object to apply rate limits, budgets, and access groups.
        return UserAPIKeyAuth(
            api_key=target_key.token,
            key_name=target_key.key_name,
            team_alias=parsed_metadata.get("team_alias", "platform"),
            token=target_key.token,          
            key_alias=target_key.key_alias,
            user_id=target_key.user_id,              
            team_id=target_key.team_id,
            models=target_key.models or [],
            max_budget=target_key.max_budget,
            spend=target_key.spend or 0.0,
            tpm_limit=target_key.tpm_limit,
            rpm_limit=target_key.rpm_limit,
            expires=target_key.expires,
            metadata=parsed_metadata,
            allowed_routes=target_key.allowed_routes
        )
        
    # If the API key does not match our Master Key, fall back to standard LiteLLM authentication
    raise Exception("fallback") 
```

---

## 3. Kubernetes Deployment Configuration

The Python script must be mounted into the LiteLLM Pod and registered in its runtime environment variables.

### A. Creating the ConfigMap
Package the custom auth script inside a ConfigMap in the namespace where LiteLLM is running (e.g., `litellm-system`):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: ackstorm-litellm-extras
  namespace: litellm-system
data:
  auth_user_map.py: |
    # [Insert code from Section 2 here]
```

To create/update this in a running cluster via CLI:
```bash
kubectl -n litellm-system create configmap ackstorm-litellm-extras \
  --from-file=auth_user_map.py=path/to/auth_user_map.py \
  -o yaml --dry-run=client | kubectl apply -f -
```

### B. Helm Chart Values Integration (`values.yaml`)
To mount the script and register it as LiteLLM's custom authenticator, update the `berriai/litellm-helm` configuration in your Helm values file:

```yaml
# 1. Define the volumes to fetch the custom auth module from the ConfigMap
volumes:
  - name: ackstorm-litellm-auth
    configMap:
      name: ackstorm-litellm-extras
      items:
        - key: auth_user_map.py
          path: auth_user_map.py
      optional: false

# 2. Mount the script into /app (the container working directory) so Python can import it
volumeMounts:
  - name: ackstorm-litellm-auth
    mountPath: /app/auth_user_map.py
    subPath: auth_user_map.py
    readOnly: true

# 3. Set the environment variable to activate custom auth
envVars:
  # Instructs LiteLLM to use the `sso_key_swapper` function in the `auth_user_map` module
  LITELLM_CUSTOM_AUTH: "auth_user_map.sso_key_swapper"
```

---

## 4. Troubleshooting & Verification

### A. Verify Module Loading on Startup
When the LiteLLM pod restarts, check the container logs to ensure that the custom auth script was mounted and loaded without errors:

```bash
kubectl -n litellm-system logs deploy/litellm -c litellm | grep -E "auth|custom"
```

### B. Troubleshooting Common Issues

| Symptom | Probable Cause | Resolution |
|---------|----------------|------------|
| `ModuleNotFoundError: No module named 'auth_user_map'` | The custom script was mounted in the wrong directory inside the pod. | Ensure `mountPath` in `volumeMounts` is `/app/auth_user_map.py` (matching the container's working directory). |
| `HTTP 403 Forbidden` on requests | The virtual key has expired, is blocked, or the forwarder sent a nonexistent ID. | Check the `litellm_verificationtoken` database table for the `token` matching the `X-Litellm-Key-Id` header to verify status. |
| `fallback` exception in logs | The incoming request's API key didn't match the master key, or the `X-Litellm-Key-Id` header was missing. | Verify that the forwarder is correctly configured with the exact same Master Key as LiteLLM, and check that `X-Litellm-Key-Id` is present in outbound forwarder requests. |
