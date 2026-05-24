#!/usr/bin/env python3
"""
Pull bill attachments from Gmail (freegly@gmail.com) and POST them to
the family-finances import endpoint.

Usage:
    python import_from_gmail.py [--query "..."] [--server URL] [--keep]

Default query: looks for Alipay/WeChat bill attachments in last 24h.
"""

from __future__ import annotations

import argparse
import base64
import os
import re
import sys
import tempfile
from pathlib import Path

# Reuse the same OAuth+proxy bootstrap that google_api.py uses.
SKILL_DIR = Path(os.path.expanduser("~/.hermes/skills/productivity/google-workspace/scripts"))
sys.path.insert(0, str(SKILL_DIR))

import _hermes_home  # noqa: F401  -- installs httplib2 proxy patch
from googleapiclient.discovery import build  # noqa: E402
from google.oauth2.credentials import Credentials  # noqa: E402
import requests  # noqa: E402


TOKEN_PATH = Path(os.path.expanduser("~/.hermes/google_token.json"))


def get_gmail_service():
    creds = Credentials.from_authorized_user_file(str(TOKEN_PATH))
    return build("gmail", "v1", credentials=creds, cache_discovery=False)


def find_attachments(service, query: str):
    """Yield (msg_id, filename, attachment_id, mimeType) tuples."""
    resp = service.users().messages().list(userId="me", q=query, maxResults=50).execute()
    for m in resp.get("messages", []):
        msg = service.users().messages().get(userId="me", id=m["id"], format="full").execute()
        for part in iter_parts(msg.get("payload", {})):
            filename = part.get("filename") or ""
            body = part.get("body", {})
            att_id = body.get("attachmentId")
            if filename and att_id:
                yield m["id"], filename, att_id, part.get("mimeType", "")


def iter_parts(payload):
    """Recursively walk MIME parts."""
    yield payload
    for part in payload.get("parts", []) or []:
        yield from iter_parts(part)


def download_attachment(service, msg_id: str, att_id: str) -> bytes:
    att = service.users().messages().attachments().get(
        userId="me", messageId=msg_id, id=att_id
    ).execute()
    data = att["data"]
    # Gmail API returns URL-safe base64
    return base64.urlsafe_b64decode(data)


def classify(filename: str) -> str | None:
    """Return 'alipay' / 'wepay' / None based on filename heuristics."""
    name = filename.lower()
    if "alipay" in name or "支付宝" in filename:
        return "alipay"
    if "wepay" in name or "微信" in filename or "wechat" in name:
        return "wepay"
    return None


def normalize_filename(filename: str, kind: str) -> str:
    """Ensure the filename starts with alipay-/wepay- so the server detects source."""
    if classify(filename) is not None:
        return filename  # already prefixed
    base = re.sub(r"[^\w\.\-]+", "_", filename)
    return f"{kind}-import-{base}"


def upload(server: str, path: Path, original_filename: str, kind: str, account: str) -> dict:
    """POST file + source + account to /imports as multipart/form-data."""
    source = "alipay" if kind == "alipay" else "wechat"
    with path.open("rb") as fh:
        files = {"file": (original_filename, fh)}
        data = {"source": source, "account": account}
        r = requests.post(f"{server.rstrip('/')}/imports", files=files, data=data, timeout=120, allow_redirects=False)
    return {
        "status": r.status_code,
        "location": r.headers.get("Location", ""),
        "body": r.text[:2000],
        "source": source,
        "account": account,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument(
        "--query",
        default='has:attachment newer_than:1d (subject:(账单 OR bill OR 微信 OR 支付宝 OR alipay OR wechat) OR filename:(csv OR xlsx OR zip))',
        help="Gmail search query",
    )
    ap.add_argument("--server", default="http://127.0.0.1:8787")
    ap.add_argument("--account", choices=["husband", "wife"], required=False,
                    help="Account owner. Required unless --dry-run.")
    ap.add_argument("--keep", action="store_true", help="Keep downloaded files in CWD instead of tempdir")
    ap.add_argument("--dry-run", action="store_true", help="List attachments, do not upload")
    args = ap.parse_args()

    svc = get_gmail_service()
    print(f"[info] query: {args.query}", file=sys.stderr)
    found = list(find_attachments(svc, args.query))
    print(f"[info] {len(found)} attachment(s) found", file=sys.stderr)

    if not found:
        print("No attachments. Exit.")
        return 0

    out_dir = Path.cwd() if args.keep else Path(tempfile.mkdtemp(prefix="gmail-bills-"))
    print(f"[info] download dir: {out_dir}", file=sys.stderr)

    results = []
    for msg_id, filename, att_id, mime in found:
        kind = classify(filename)
        print(f"  - {filename} ({mime}) → kind={kind}", file=sys.stderr)
        if args.dry_run:
            continue
        data = download_attachment(svc, msg_id, att_id)
        local = out_dir / filename
        local.write_bytes(data)
        if kind is None:
            results.append({"file": filename, "skipped": "unknown source (filename does not contain alipay/wepay/支付宝/微信)"})
            continue
        if not args.account:
            results.append({"file": filename, "skipped": "missing --account husband|wife"})
            continue
        result = upload(args.server, local, filename, kind, args.account)
        result["file"] = filename
        result["kind"] = kind
        results.append(result)

    import json
    print(json.dumps(results, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
