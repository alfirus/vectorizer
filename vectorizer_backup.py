#!/usr/bin/env python3
"""Vectorizer ChromaDB Backup - Lightweight version for slow Docker."""
import datetime, glob, os, subprocess, sys, tarfile, tempfile, urllib.request, json, gzip

BACKUP_DIR = r"C:\Users\alfir\SynologyDrive\ai\backups\vectorizer"
KEEP_DAYS = 7
os.makedirs(BACKUP_DIR, exist_ok=True)

log = lambda msg: print(f"[{datetime.datetime.now().strftime('%H:%M:%S')}] {msg}")
date_suffix = datetime.datetime.now().strftime("%Y-%m-%d")

# Remove stale 0-byte file from previous failed run
backup_path = os.path.join(BACKUP_DIR, f"chroma-{date_suffix}.tar.gz")
if os.path.exists(backup_path) and os.path.getsize(backup_path) == 0:
    log("Removing stale 0-byte backup")
    os.remove(backup_path)

success = False

# Method A: ChromaDB HTTP API (fastest, no docker needed)
try:
    log("Method A: ChromaDB HTTP API...")
    req = urllib.request.Request("http://localhost:8100/api/v2/collections", method="GET")
    with urllib.request.urlopen(req, timeout=15) as resp:
        data = json.loads(resp.read())
    
    collections = data.get("collections", [])
    log(f"  Found {len(collections)} collection(s)")
    
    backup_data = {"timestamp": datetime.datetime.now().isoformat(), "collections": []}
    for coll_name in collections:
        try:
            req2 = urllib.request.Request(
                f"http://localhost:8100/api/v2/collections/{coll_name}", method="GET"
            )
            with urllib.request.urlopen(req2, timeout=15) as resp2:
                backup_data["collections"].append(json.loads(resp2.read()))
        except Exception as e:
            log(f"  Error getting {coll_name}: {e}")
    
    meta_path = os.path.join(BACKUP_DIR, f"chroma-metadata-{date_suffix}.json.gz")
    with gzip.open(meta_path, "wt", encoding="utf-8") as f:
        json.dump(backup_data, f, indent=2)
    
    size = os.path.getsize(meta_path)
    log(f"  SUCCESS ΓÇö metadata backup ({size} bytes)")
    success = True
    
except Exception as e:
    log(f"  HTTP API failed: {e}")

# Method B: docker cp (lighter than exec tar)
if not success:
    try:
        log("Method B: docker cp...")
        with tempfile.TemporaryDirectory() as tmpdir:
            cmd = ["docker", "cp", "vectorizer-chromadb:/data/.", tmpdir]
            result = subprocess.run(cmd, capture_output=True, text=True, timeout=180)
            
            if result.returncode == 0 and os.listdir(tmpdir):
                with tarfile.open(backup_path, "w:gz") as tar:
                    for item in os.listdir(tmpdir):
                        full = os.path.join(tmpdir, item)
                        tar.add(full, arcname=item)
                
                size = os.path.getsize(backup_path)
                log(f"  SUCCESS ΓÇö docker cp backup ({size} bytes)")
                success = True
            else:
                log("  docker cp returned empty or failed")
    except Exception as e:
        log(f"  docker cp failed: {e}")

# Method C: docker exec with shorter timeout
if not success:
    try:
        log("Method C: docker exec tar...")
        cmd = "docker exec vectorizer-chromadb tar -czf /tmp/backup.tar.gz -C /data ."
        result = subprocess.run(cmd, shell=True, capture_output=True, text=True, timeout=180)
        
        if result.returncode == 0:
            # Download the file
            cmd2 = "docker cp vectorizer-chromadb:/tmp/backup.tar.gz " + backup_path
            result2 = subprocess.run(cmd2, shell=True, capture_output=True, text=True, timeout=60)
            
            if result2.returncode == 0 and os.path.exists(backup_path):
                size = os.path.getsize(backup_path)
                log(f"  SUCCESS ΓÇö docker exec backup ({size} bytes)")
                success = True
    except Exception as e:
        log(f"  docker exec failed: {e}")

# Prune old backups
log("\nPruning backups older than {} days...".format(KEEP_DAYS))
cutoff = datetime.datetime.now() - datetime.timedelta(days=KEEP_DAYS)
pruned = 0
for f in glob.glob(os.path.join(BACKUP_DIR, "chroma-*.tar.gz")) + \
         glob.glob(os.path.join(BACKUP_DIR, "chroma-metadata-*.json.gz")):
    try:
        basename = os.path.basename(f)
        # Extract date from filename
        if "-metadata-" in basename:
            date_str = basename.replace("chroma-metadata-", "").replace(".json.gz", "")
        else:
            date_str = basename.replace("chroma-", "").replace(".tar.gz", "")
        
        file_date = datetime.datetime.strptime(date_str, "%Y-%m-%d")
        if file_date < cutoff:
            os.remove(f)
            log(f"  PRUNED: {basename}")
            pruned += 1
    except Exception as e:
        pass

if pruned > 0:
    log(f"Pruned {pruned} old backup(s)")
else:
    log("No backups to prune")

# Final status
log("=" * 60)
if success:
    log("BACKUP SUCCESS")
    sys.exit(0)
else:
    log("BACKUP ERROR ΓÇö All methods failed")
    print("\nBACKUP ERROR: All backup methods failed", file=sys.stderr)
    sys.exit(1)
