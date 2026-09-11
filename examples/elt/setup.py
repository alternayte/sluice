#!/usr/bin/env python3
"""Load the ELT example into a running Sluice and run the flow.

The script creates the namespace elt, uploads the files of namespace/, sets the
connection variables and the password secret, runs the flow elt and waits for the end.
It uses only the Python standard library.

Environment:
  SLUICE_URL                       default http://localhost:8080
  SLUICE_BOOTSTRAP_ADMIN_EMAIL     default admin@local.test
  SLUICE_BOOTSTRAP_ADMIN_PASSWORD  required
  PG_HOST, PG_PORT, PG_DATABASE, PG_USER, ELT_PG_PASSWORD
                                   default: the warehouse service of compose.yml
"""

import http.cookiejar
import json
import os
import pathlib
import sys
import time
import urllib.error
import urllib.request

BASE = os.environ.get("SLUICE_URL", "http://localhost:8080").rstrip("/")
NAMESPACE = "elt"
ROOT = pathlib.Path(__file__).resolve().parent / "namespace"


def request(opener, method, path, body=None, headers=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method, headers={"Content-Type": "application/json", **(headers or {})})
    try:
        with opener.open(req) as resp:
            text = resp.read().decode()
            return resp.status, json.loads(text) if text else None
    except urllib.error.HTTPError as err:
        return err.code, json.loads(err.read().decode() or "null")


def main() -> int:
    password = os.environ.get("SLUICE_BOOTSTRAP_ADMIN_PASSWORD")
    if not password:
        print("Set SLUICE_BOOTSTRAP_ADMIN_PASSWORD.", file=sys.stderr)
        return 2
    email = os.environ.get("SLUICE_BOOTSTRAP_ADMIN_EMAIL", "admin@local.test")
    session = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    status, _ = request(session, "POST", "/api/v1/auth/login", {"email": email, "password": password})
    if status != 200:
        print(f"Sign-in failed with status {status}.", file=sys.stderr)
        return 1
    status, token = request(session, "POST", "/api/v1/tokens", {"name": "elt-example-setup", "role": "admin"}, {"Origin": BASE})
    if status != 201:
        print(f"Token creation failed with status {status}: {token}", file=sys.stderr)
        return 1
    api = urllib.request.build_opener()
    auth = {"Authorization": "Bearer " + token["secret"]}

    status, body = request(api, "POST", "/api/v1/namespaces", {"name": NAMESPACE}, auth)
    if status not in (201, 409):
        print(f"Namespace creation failed with status {status}: {body}", file=sys.stderr)
        return 1
    changes = []
    for path in sorted(p for p in ROOT.rglob("*") if p.is_file()):
        rel = path.relative_to(ROOT).as_posix()
        changes.append({"op": "put", "path": rel, "content": path.read_text(), "executable": rel.endswith(".sh")})
    status, body = request(api, "POST", f"/api/v1/namespaces/{NAMESPACE}/changes", {"message": "Load the ELT example", "changes": changes}, auth)
    if status != 201:
        print(f"Upload failed with status {status}: {body}", file=sys.stderr)
        return 1

    variables = {
        "PG_HOST": os.environ.get("PG_HOST", "warehouse"),
        "PG_PORT": os.environ.get("PG_PORT", "5432"),
        "PG_DATABASE": os.environ.get("PG_DATABASE", "warehouse"),
        "PG_USER": os.environ.get("PG_USER", "elt"),
    }
    for key, value in variables.items():
        status, body = request(api, "PUT", f"/api/v1/namespaces/{NAMESPACE}/variables/{key}", {"value": value}, auth)
        if status != 200:
            print(f"Variable {key} failed with status {status}: {body}", file=sys.stderr)
            return 1
    secret = os.environ.get("ELT_PG_PASSWORD", "elt-password-1")
    status, body = request(api, "PUT", f"/api/v1/namespaces/{NAMESPACE}/secrets/ELT_PG_PASSWORD", {"value": secret}, auth)
    if status != 200:
        print(f"Secret failed with status {status}: {body}", file=sys.stderr)
        return 1

    status, execution = request(api, "POST", f"/api/v1/flows/{NAMESPACE}/elt/executions", {}, auth)
    if status != 201:
        print(f"Run failed with status {status}: {execution}", file=sys.stderr)
        return 1
    print(f"Started execution {execution['id']}: {BASE}/executions/{execution['id']}")
    print("The first run downloads dlt and SQLMesh. It takes some minutes.")
    while execution["state"] not in ("SUCCESS", "FAILED", "TIMED_OUT", "CANCELLED", "SKIPPED"):
        time.sleep(5)
        _, execution = request(api, "GET", f"/api/v1/executions/{execution['id']}", None, auth)
        print(f"  {execution['state']}")
    print(f"The execution ended {execution['state']}.")
    return 0 if execution["state"] == "SUCCESS" else 1


if __name__ == "__main__":
    sys.exit(main())
