#!/usr/bin/env python3
"""Dependency-free MCP fixture; stdin EOF exits this exact backend process."""
import json
import os
import sys
from pathlib import Path


def event(kind):
    with Path(os.environ["QUALIFICATION_BACKEND_EVENTS"]).open("a") as output:
        output.write(json.dumps({"event": kind, "pid": os.getpid()}) + "\n")


def main():
    event("started")
    for line in sys.stdin:
        request = json.loads(line)
        if "id" not in request:
            continue
        method = request.get("method")
        if method == "initialize":
            result = {"protocolVersion": "2025-03-26", "serverInfo": {"name": "owned-fixture", "version": "1"}, "capabilities": {"tools": {}}}
        elif method == "tools/list":
            result = {"tools": [{"name": "echo", "description": "Qualification echo", "inputSchema": {"type": "object"}}]}
        elif method == "tools/call":
            event("called")
            result = {"content": [{"type": "text", "text": "owned-backend-called"}]}
        elif method == "prompts/list":
            result = {"prompts": []}
        elif method == "resources/list":
            result = {"resources": []}
        elif method == "resources/templates/list":
            result = {"resourceTemplates": []}
        else:
            result = {}
        print(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}), flush=True)
    event("stdin_eof")


if __name__ == "__main__":
    main()
