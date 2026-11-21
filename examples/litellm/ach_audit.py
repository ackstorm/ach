# SPDX-License-Identifier: Apache-2.0
"""LiteLLM callback: one audit line on stdout per model, MCP tool and A2A agent call.

Install: docs/platform/connect-litellm.md. Fields: docs/reference/audit-events.md.
Self-check, in an image that has LiteLLM: python ach_audit.py
"""

import json
from datetime import datetime, timezone

from litellm.integrations.custom_logger import CustomLogger

_AGENT_PREFIX = "a2a_agent/"
_ENV_TEAM_PREFIX = "ach-env-"


def audit_line(payload):
    """Map a LiteLLM StandardLoggingPayload to an audit line, or None when the call is not audited.

    Only named fields are copied, so prompts, responses, tool arguments and
    error text never reach the line. Identity comes from the key LiteLLM
    authenticated, never from request tags, which a caller reaching LiteLLM
    directly can set.
    """
    call_type = payload.get("call_type") or ""
    if call_type == "list_mcp_tools":
        return None
    meta = payload.get("metadata") or {}
    tool = meta.get("mcp_tool_call_metadata") or {}
    model = payload.get("model") or ""
    team = meta.get("user_api_key_team_alias") or ""
    err = payload.get("error_information") or {}

    if call_type == "call_mcp_tool":
        action, kind, name = "litellm.tool.call", "mcp_server", tool.get("mcp_server_name")
    elif model.startswith(_AGENT_PREFIX):
        action, kind, name = "litellm.agent.call", "agent", model[len(_AGENT_PREFIX):]
    else:
        action, kind, name = "litellm.model.call", "model", payload.get("model_group") or model

    policy = [
        {"name": g.get("guardrail_name"), "mode": g.get("guardrail_mode"), "status": g.get("guardrail_status")}
        for g in payload.get("guardrail_information") or []
    ]
    if payload.get("status") == "success":
        outcome = "success"
    elif any(p["status"] == "guardrail_intervened" for p in policy):
        outcome = "guardrail_blocked"
    else:
        outcome = "failed"

    is_model = kind == "model"
    line = {
        "time": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        "level": "INFO",
        "msg": action,
        "audit": True,
        "action": action,
        "outcome": outcome,
        "actor": meta.get("user_api_key_user_id") or meta.get("user_api_key_user_email"),
        "key.id": meta.get("user_api_key_alias"),
        "environment": team[len(_ENV_TEAM_PREFIX):] if team.startswith(_ENV_TEAM_PREFIX) else None,
        "team": team,
        "target.kind": kind,
        "target.name": name,
        "mcp.tool": tool.get("name") if kind == "mcp_server" else None,
        "model.name": model if is_model else None,
        "model.provider": payload.get("custom_llm_provider") if is_model else None,
        "tokens.prompt": payload.get("prompt_tokens") or None,
        "tokens.completion": payload.get("completion_tokens") or None,
        "cost": payload.get("response_cost"),
        "policy": policy,
        "error.class": err.get("error_class"),
        "error.code": err.get("error_code"),
        "source.ip": (payload.get("requester_ip_address") or "").split(",")[0].strip(),
        "source.user_agent": payload.get("user_agent"),
        "trace_id": payload.get("trace_id"),
        "litellm.call_id": payload.get("litellm_call_id"),
        "litellm.call_type": call_type,
    }
    return {k: v for k, v in line.items() if v not in (None, "", [])}


def _emit(kwargs):
    payload = kwargs.get("standard_logging_object")
    line = audit_line(payload) if payload else None
    if line:
        print(json.dumps(line, default=str), flush=True)


class AchAudit(CustomLogger):
    async def async_log_success_event(self, kwargs, response_obj, start_time, end_time):
        _emit(kwargs)

    async def async_log_failure_event(self, kwargs, response_obj, start_time, end_time):
        _emit(kwargs)


proxy_handler_instance = AchAudit()


def _demo():
    secret = "CONTENT-MUST-NOT-LEAK"
    base = {
        "status": "success",
        "trace_id": "t1",
        "requester_ip_address": "10.0.4.17, 10.0.0.1",
        "litellm_call_id": "c1",
        "messages": [{"role": "user", "content": secret}],
        "response": {"choices": [{"message": {"content": secret}}]},
        "model_parameters": {"system": secret},
        "error_str": secret,
        "metadata": {
            "user_api_key_user_id": "ana@example.com",
            "user_api_key_alias": "ekid_7f3c2a91",
            "user_api_key_team_alias": "ach-env-finance",
            "mcp_tool_call_metadata": {
                "mcp_server_name": "gitlab",
                "name": "create_merge_request",
                "arguments": {"title": secret},
                "result": {"text": secret},
            },
        },
    }
    model = audit_line(
        {
            **base,
            "call_type": "acompletion",
            "model": "gemini-flash",
            "model_group": "fast",
            "custom_llm_provider": "vertex_ai",
            "prompt_tokens": 12,
            "completion_tokens": 3,
            "response_cost": 0.0004,
        }
    )
    tool = audit_line({**base, "call_type": "call_mcp_tool", "model": "MCP: create_merge_request", "response_cost": 0.0})
    agent = audit_line({**base, "call_type": "asend_message_streaming", "model": "a2a_agent/doctor"})
    blocked = audit_line(
        {
            **base,
            "call_type": "acompletion",
            "model": "gemini-flash",
            "status": "failure",
            "error_information": {"error_class": "GuardrailRaisedException", "error_code": "400", "error_message": secret, "traceback": secret},
            "guardrail_information": [
                {
                    "guardrail_name": "pii",
                    "guardrail_mode": "pre_call",
                    "guardrail_status": "guardrail_intervened",
                    "guardrail_request": secret,
                    "guardrail_response": secret,
                }
            ],
        }
    )
    failed = audit_line({**base, "call_type": "acompletion", "model": "gemini-flash", "status": "failure"})

    assert audit_line({**base, "call_type": "list_mcp_tools"}) is None
    for line in (model, tool, agent, blocked, failed):
        assert secret not in json.dumps(line), line
        assert line["audit"] is True and line["actor"] == "ana@example.com", line
        assert line["key.id"] == "ekid_7f3c2a91" and line["environment"] == "finance", line
        assert line["source.ip"] == "10.0.4.17", line
    assert (model["action"], model["target.name"], model["model.name"], model["tokens.prompt"]) == ("litellm.model.call", "fast", "gemini-flash", 12)
    assert (tool["action"], tool["target.kind"], tool["target.name"], tool["mcp.tool"]) == ("litellm.tool.call", "mcp_server", "gitlab", "create_merge_request")
    assert "model.name" not in tool and tool["cost"] == 0.0
    assert (agent["action"], agent["target.kind"], agent["target.name"]) == ("litellm.agent.call", "agent", "doctor")
    assert (blocked["outcome"], blocked["error.class"]) == ("guardrail_blocked", "GuardrailRaisedException")
    assert blocked["policy"] == [{"name": "pii", "mode": "pre_call", "status": "guardrail_intervened"}]
    assert failed["outcome"] == "failed"
    print("ok")


if __name__ == "__main__":
    _demo()
