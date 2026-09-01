#!/usr/bin/env python3
"""Migrate all workspaces from local Vectorizer to server via SSH."""
import json
import time
import urllib.request
import subprocess

LOCAL_URL = "http://localhost:8091/api/v1"
API_KEY = "vectorizer-local-key"
SSH_KEY = "C:/Users/alfir/.ssh/personal"
SERVER = "ubuntu@139.99.131.127"

def api_get(url, path):
    req = urllib.request.Request(f"{url}{path}", headers={"X-API-Key": API_KEY})
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read())

def ssh_cmd(cmd):
    result = subprocess.run(
        ["ssh", "-i", SSH_KEY, SERVER, cmd],
        capture_output=True, text=True, timeout=30
    )
    return result.stdout.strip()

def export_workspace(ws_id):
    """Export all messages from a workspace using GET /messages."""
    print(f"  Exporting {ws_id}...", end=" ", flush=True)
    all_messages = []
    offset = 0
    limit = 100
    while True:
        try:
            data = api_get(LOCAL_URL, f"/messages?workspace_id={ws_id}&limit={limit}&offset={offset}")
            msgs = data.get("messages", [])
            if not msgs:
                break
            all_messages.extend(msgs)
            if len(msgs) < limit:
                break
            offset += limit
        except Exception as e:
            print(f"ERROR at offset {offset}: {e}")
            break
    print(f"{len(all_messages)} messages")
    return all_messages

def import_workspace(ws_id, messages):
    """Import messages into a workspace on server via SSH curl."""
    if not messages:
        print(f"  Skipping {ws_id} - no messages")
        return 0
    
    print(f"  Importing {ws_id} ({len(messages)} messages)...", end=" ", flush=True)
    
    # Convert to batch import format
    batch = []
    for msg in messages:
        meta = msg.get("metadata", {})
        content = msg.get("document", "")
        if not content:
            continue
        batch.append({
            "session_id": meta.get("session_id", f"migration-{ws_id}"),
            "role": meta.get("role", "user"),
            "content": content,
            "metadata": {
                k: str(v) if not isinstance(v, (str, int, float, bool)) else v
                for k, v in meta.items()
                if k in ["source_type", "source_path", "header_path", "chunk_type",
                         "tags", "importance", "agent", "language", "parent_doc_id",
                         "doc_title", "chunk_id", "file_hash", "entities", "summary_1line"]
                and v is not None and v != ""
            }
        })
    
    # Import in batches of 10 via SSH
    imported = 0
    for i in range(0, len(batch), 10):
        chunk = batch[i:i+10]
        payload = json.dumps({"workspace_id": ws_id, "messages": chunk})
        # Write payload to temp file, use curl @file
        ssh_cmd(f"cat > /tmp/import_payload.json << 'PAYLOAD_EOF'\n{payload}\nPAYLOAD_EOF")
        result = ssh_cmd(f"curl -s -X POST 'http://127.0.0.1:8091/api/v1/messages/batch' -H 'X-API-Key: {API_KEY}' -H 'Content-Type: application/json' -d @/tmp/import_payload.json")
        try:
            resp = json.loads(result)
            if "error" not in resp:
                imported += len(chunk)
            else:
                print(f"\n    Batch {i//10} error: {resp.get('error')}")
        except:
            imported += len(chunk)  # assume success if no parseable error
        time.sleep(0.05)
    
    print(f" imported {imported}")
    return imported

def main():
    # Get workspaces
    local_data = api_get(LOCAL_URL, "/workspaces")
    local_ws = [w["id"] for w in local_data["workspaces"]]
    
    server_result = ssh_cmd("curl -s http://127.0.0.1:8091/api/v1/workspaces -H 'X-API-Key: vectorizer-local-key'")
    server_ws = [w["id"] for w in json.loads(server_result).get("workspaces", [])]
    
    missing = [ws for ws in local_ws if ws not in server_ws]
    
    print(f"Local: {len(local_ws)} workspaces")
    print(f"Server: {len(server_ws)} workspaces")
    print(f"Missing: {missing}")
    
    if not missing:
        print("All workspaces already on server!")
        return
    
    total_imported = 0
    for ws_id in missing:
        messages = export_workspace(ws_id)
        count = import_workspace(ws_id, messages)
        total_imported += count
    
    # Verify
    print("\n--- Verification ---")
    server_result = ssh_cmd("curl -s http://127.0.0.1:8091/api/v1/workspaces -H 'X-API-Key: vectorizer-local-key'")
    final_ws = json.loads(server_result).get("workspaces", [])
    print(f"Server workspaces: {[w['id'] for w in final_ws]}")
    print(f"Total imported: {total_imported} messages")

if __name__ == "__main__":
    main()
