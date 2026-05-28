# SPDX-License-Identifier: Apache-2.0

import os
import json
from datetime import datetime, timezone
from fastapi import Request, HTTPException
from litellm.proxy._types import UserAPIKeyAuth

# LITELLM_MASTER_KEY is the standard env var used inside the container
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
    # Import proxy_server inside the function to prevent cyclic import deadlock during module loading
    from litellm.proxy import proxy_server 

    # 1. Check if the incoming request uses our trusted Master Key (either as bearer or x-litellm-api-key)
    header_api_key = request.headers.get("x-litellm-api-key")
    clean_api_key = clean_bearer(api_key)
    clean_header_api_key = clean_bearer(header_api_key)

    if clean_api_key == MASTER_KEY or clean_header_api_key == MASTER_KEY:
        # 2. Extract the Virtual Key ID (token) sent by our forwarder
        litellm_key_id = request.headers.get("x-litellm-key-id")
        if not litellm_key_id:
            # If no key-id is provided, fall back to default LiteLLM auth
            raise Exception("fallback")
            
        if not proxy_server.prisma_client or not proxy_server.prisma_client.db:
            raise Exception("fallback")
            
        # 3. Direct lookup by token ID (extremely fast indexed query)
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

        # 6. Parse metadata
        parsed_metadata = {}
        meta = target_key.metadata
        if isinstance(meta, str):
            try:
                parsed_metadata = json.loads(meta)
            except json.JSONDecodeError:
                pass
        elif isinstance(meta, dict):
            parsed_metadata = meta

        # 7. Return the validated key's parameters to allow LiteLLM to impersonate it
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
        
    # Go to litellm default auth if api_key != MASTER_KEY
    raise Exception("fallback") 
