#!/usr/bin/env python3
"""Exercise idle EOF against an explicit isolated gateway; never run recovery.

The Go qualification fixture separately observes gateway-owned Wait after this
harness proves terminal retirement. A standalone run proves only frontend and
adapter-worker Wait, not gateway Wait.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import selectors
import shutil
import signal
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request


WORKER_OBSERVER = r'''
import json,os,runpy,subprocess,sys
original = subprocess.Popen
class ObservedPopen(original):
    def wait(self, *args, **kwargs):
        try:
            code = super().wait(*args, **kwargs)
        except subprocess.TimeoutExpired:
            with open(os.environ["QUALIFICATION_WORKER_WAITS"], "a") as out:
                out.write(json.dumps({"pid": self.pid, "state": "wait_unknown"}) + "\n")
            raise
        with open(os.environ["QUALIFICATION_WORKER_WAITS"], "a") as out:
            out.write(json.dumps({"pid": self.pid, "state": "waited", "code": code}) + "\n")
        return code
subprocess.Popen = ObservedPopen
adapter = sys.argv[1]
sys.path.insert(0, os.path.dirname(adapter))
sys.argv = sys.argv[1:]
namespace = runpy.run_path(adapter)
original_request = namespace["Gateway"].request
def observed_request(self, method, path, *args, **kwargs):
    response = original_request(self, method, path, *args, **kwargs)
    status, body, session = response
    event = None
    if method == "POST" and path.endswith("/resolve") and status == 200 and isinstance(body, dict) and body.get("terminal") is True:
        event = body
    elif method == "DELETE" and path.startswith("/api/v0/servers/"):
        event = {"method": method, "path": path, "status": status}
    if event is not None:
        with open(os.environ["QUALIFICATION_GATEWAY_RECEIPTS"], "a") as out:
            out.write(json.dumps(event) + "\n")
    return response
namespace["Gateway"].request = observed_request
namespace["main"]()
'''


def read_events(path):
    return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []


def request(url, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url + path, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=10) as response:
        return json.load(response)


def rpc(child, identifier, method, params):
    child.stdin.write(json.dumps({"jsonrpc": "2.0", "id": identifier, "method": method, "params": params}).encode() + b"\n")
    child.stdin.flush()
    with selectors.DefaultSelector() as selector:
        selector.register(child.stdout, selectors.EVENT_READ)
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            if not selector.select(max(0, deadline - time.monotonic())):
                break
            line = child.stdout.readline()
            if not line:
                raise RuntimeError("frontend exited before response")
            response = json.loads(line)
            if response.get("id") == identifier:
                if "error" in response:
                    raise RuntimeError(str(response["error"]))
                return response["result"]
    raise TimeoutError("frontend response deadline")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gateway-url", required=True)
    parser.add_argument("--adapter-script", type=Path, required=True)
    parser.add_argument("--python", type=Path, required=True)
    parser.add_argument("--receipt", type=Path, required=True)
    parser.add_argument("--scratch-parent", type=Path, required=True)
    args = parser.parse_args()
    def interrupted(signum, frame):
        raise RuntimeError(f"qualification interrupted by signal {signum}")
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    url = urllib.parse.urlsplit(args.gateway_url)
    if url.scheme != "http" or url.hostname != "127.0.0.1" or not url.port or url.path not in ("", "/"):
        parser.error("gateway must be an explicit dynamic loopback HTTP URL")
    for path in (args.adapter_script, args.python, args.receipt, args.scratch_parent):
        if not path.is_absolute():
            parser.error("all paths must be absolute")
    args.adapter_script = args.adapter_script.resolve(strict=True)
    args.python = args.python.resolve(strict=True)
    args.scratch_parent = args.scratch_parent.resolve(strict=True)
    root = Path(tempfile.mkdtemp(prefix="owned-eof-", dir=args.scratch_parent))
    if args.receipt.is_relative_to(root):
        parser.error("receipt must be outside disposable scratch")
    receipt = {"schema": "mcpjungle-eof-qualification/v1", "scratch": str(root), "gateway_wait": "not_observed_by_python", "frontend_wait": "not_started", "terminal_before_gateway_shutdown": False}
    child = None
    success = False
    events = root / "backend-events.jsonl"
    waits = root / "worker-waits.jsonl"
    gateway_receipts = root / "gateway-receipts.jsonl"
    stderr_path = root / "frontend.stderr"
    try:
        for name in ("home", "tmp", "cwd", "state", "backend-scratch"):
            (root / name).mkdir(mode=0o700)
        backend = Path(__file__).resolve().with_name("synthetic_backend.py")
        config = {"gateway_url": args.gateway_url, "gateway_registration_contract": 1,
                  "startup_budget_seconds": 60, "call_deadline_seconds": 30,
                  "state_root": str(root / "state"), "temp_root": str(root / "backend-scratch"),
                  "backend_launcher": str(args.adapter_script.with_name("backend_launcher.py")),
                  "services": {"next-devtools": {"command": str(args.python), "args": ["-B", str(backend)],
                      "env": {"QUALIFICATION_BACKEND_EVENTS": str(events)}, "session_mode": "stateful"}}}
        config_path = root / "config.json"
        config_path.write_text(json.dumps(config))
        env = {"HOME": str(root / "home"), "TMPDIR": str(root / "tmp"), "PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "PYTHONDONTWRITEBYTECODE": "1", "QUALIFICATION_WORKER_WAITS": str(waits), "QUALIFICATION_GATEWAY_RECEIPTS": str(gateway_receipts)}
        with stderr_path.open("wb") as stderr:
            child = subprocess.Popen([str(args.python), "-B", "-c", WORKER_OBSERVER, str(args.adapter_script), "--config", str(config_path), "--service", "next-devtools"], cwd=root / "cwd", env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=stderr)
            receipt["frontend_wait"] = "pending"
            rpc(child, 1, "initialize", {"protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "owned-eof", "version": "1"}})
            child.stdin.write(b'{"jsonrpc":"2.0","method":"notifications/initialized"}\n')
            child.stdin.flush()
            tools = rpc(child, 2, "tools/list", {})
            if not any(tool["name"] == "echo" for tool in tools["tools"]):
                raise RuntimeError("real backend tool missing")
            result = rpc(child, 3, "tools/call", {"name": "echo", "arguments": {}})
            if result.get("content", [{}])[0].get("text") != "owned-backend-called":
                raise RuntimeError("real backend call missing")
            journals = list((root / "state" / "registrations").glob("*.json"))
            if len(journals) != 1:
                raise RuntimeError("expected one active registration journal")
            registration = json.loads(journals[0].read_text())["registration"]
            receipt["registration_name"] = registration["name"]
            receipt["backend_called_before_eof"] = any(e["event"] == "called" for e in read_events(events))
            receipts_before_eof = len(read_events(gateway_receipts))
            child.stdin.close()
            try:
                code = child.wait(timeout=45)
            except subprocess.TimeoutExpired:
                receipt["frontend_wait"] = "wait_unknown"
                raise
            receipt["frontend_wait"] = "waited"
            receipt["frontend_exit_code"] = code
            if code != 0:
                raise RuntimeError("frontend failed")
            receipt["journals_remaining"] = len(list((root / "state" / "registrations").glob("*.json")))
            receipt["scratch_remaining"] = len(list((root / "backend-scratch").iterdir()))
            if receipt["journals_remaining"] or receipt["scratch_remaining"]:
                raise RuntimeError("idle EOF left cleanup pending; recovery not attempted")
            terminals = read_events(gateway_receipts)[receipts_before_eof:]
            terminal = next((item for item in terminals if item.get("name") == registration["name"] and item.get("terminal") is True and item.get("outcome") in ("present", "absent")), None)
            receipt["terminal_receipt"] = terminal
            receipt["terminal_receipt_source"] = "adapter_owned_resolve_after_frontend_eof"
            if terminal is None:
                raise RuntimeError("adapter did not receive its terminal receipt after EOF")
            if terminal["outcome"] == "present":
                deletion = next((item for item in terminals[terminals.index(terminal) + 1:] if item.get("method") == "DELETE" and item.get("path") == "/api/v0/servers/" + registration["name"] and item.get("status") == 204), None)
                receipt["adapter_deregister_response"] = deletion
                if deletion is None:
                    raise RuntimeError("adapter did not complete terminal deregistration")
            servers = request(args.gateway_url, "/api/v0/servers")
            status = request(args.gateway_url, "/api/v0/registration-status")
            if not isinstance(servers, list) or any(item.get("name") == registration["name"] for item in servers):
                raise RuntimeError("registration remains after adapter EOF cleanup")
            if status.get("complete") is not True or status.get("available") is not True or status.get("cleanup_pending_names"):
                raise RuntimeError("gateway cleanup status remains pending or unknown")
            receipt["read_only_cleanup_status"] = status
            worker_events = read_events(waits)
            if not worker_events or any(e["state"] != "waited" or e.get("code") != 0 for e in worker_events):
                raise RuntimeError("adapter-owned worker Wait unproved")
            receipt["terminal_before_gateway_shutdown"] = True
            success = True
    except Exception as exc:
        receipt["error"] = str(exc)
    finally:
        if child is not None:
            if child.stdin is not None:
                child.stdin.close()
            if child.poll() is None:
                try:
                    child.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    child.terminate()
                    try:
                        child.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        child.kill()
                        try:
                            child.wait(timeout=3)
                        except subprocess.TimeoutExpired:
                            receipt["frontend_cleanup_wait"] = "wait_unknown"
            if child.returncode is not None and receipt["frontend_wait"] != "waited":
                receipt["frontend_cleanup_wait"] = "waited"
                receipt["frontend_cleanup_exit_code"] = child.wait(timeout=0)
            if child.stdout is not None:
                child.stdout.close()
            if child.stdin is not None:
                child.stdin.close()
        receipt["backend_events"] = read_events(events)
        receipt["worker_waits"] = read_events(waits)
        receipt["adapter_terminal_receipts"] = read_events(gateway_receipts)
        receipt["frontend_stderr"] = stderr_path.read_text() if stderr_path.exists() else ""
        sources = [args.adapter_script, Path(__file__).resolve(), Path(__file__).resolve().with_name("synthetic_backend.py")]
        sources += [args.adapter_script.with_name(name) for name in ("backend_launcher.py", "registration_state.py", "attribution.py", "darwin_process.py")]
        receipt["inputs"] = {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in sources if p.is_file()}
        receipt["passed"] = success
        receipt["scratch_retained"] = not success
        if success:
            try:
                shutil.rmtree(root)
                receipt["scratch_removed"] = not root.exists()
            except OSError as exc:
                receipt["cleanup_error"] = str(exc)
                receipt["passed"] = success = False
        args.receipt.parent.mkdir(parents=True, exist_ok=True)
        args.receipt.write_text(json.dumps(receipt, indent=2) + "\n")
    return 0 if success else 1


if __name__ == "__main__":
    raise SystemExit(main())
