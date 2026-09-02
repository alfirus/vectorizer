#!/usr/bin/env python3
"""Vectorizer Health Check ΓÇö checks vectorizer, chromadb, dashboard, lm-studio.
Auto-heals by starting Docker containers if possible. Alerts via email + Telegram if still unhealthy."""

import datetime
import json
import os
import subprocess
import sys
import urllib.request
import urllib.error

# ΓöÇΓöÇ Config ΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇ
ENV_FILE = r"C:\Users\alfir\vectorizer\.env"
ALERT_EMAIL = "alfirus@gmail.com"
TELEGRAM_TOKEN = os.environ.get("TELEGRAM_BOT_TOKEN", "")
TELEGRAM_CHAT_ID = os.environ.get("TELEGRAM_CHAT_ID", "")

# ΓöÇΓöÇ Helpers ΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇ
def log(msg):
    print(f"[{datetime.datetime.now().strftime('%H:%M:%S')}] {msg}")

def read_env(path):
    """Read .env file into a dict."""
    env = {}
    try:
        with open(path, "r") as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                if "=" in line:
                    k, v = line.split("=", 1)
                    env[k.strip()] = v.strip().strip('"').strip("'")
    except FileNotFoundError:
        pass
    return env

def _lm_studio_token():
    env = read_env(ENV_FILE)
    return env.get("LM_STUDIO_API_KEY") or env.get("OAI_API_KEY") or env.get("LLM_API_KEY") or env.get("LLM_OAI_API_KEY") or ""

def _lm_studio_url():
    env = read_env(ENV_FILE)
    base = env.get("LM_STUDIO_URL") or env.get("LLM_STUDIO_URL") or env.get("OAI_COMPATIBLE_URL") or env.get("LLM_OAI_COMPATIBLE_URL") or "http://localhost:1234/v1"
    base = base.rstrip("/")
    if base.endswith("/v1"):
        return base + "/models"
    return base + "/v1/models"

def _services():
    return [
        ("vectorizer", "http://localhost:8092/", 200),
        ("chromadb",   "http://localhost:8100/api/v2/version", 200),
        ("dashboard",  "http://localhost:8094/", 200),
        ("lm-studio",  _lm_studio_url(), None),
    ]

SERVICES = _services()

SERVICE_ACCEPTABLE = {
    "vectorizer": [200, 404],
    "chromadb": [200],
    "dashboard": [200, 307],
    "lm-studio": [200, 401],
}

def check_service(name, url):
    """Check a service URL. Returns (status_code, error_msg_or_None)."""
    try:
        req = urllib.request.Request(url, method="GET")
        if name == "lm-studio":
            token = _lm_studio_token()
            if token:
                req.add_header("Authorization", f"Bearer {token}")
        with urllib.request.urlopen(req, timeout=5) as resp:
            code = resp.status
            return code, None
    except urllib.error.HTTPError as e:
        return e.code, str(e)
    except Exception as e:
        return None, str(e)

def is_healthy(name, code):
    """Check if a status code means the service is healthy."""
    acceptable = SERVICE_ACCEPTABLE.get(name, [200])
    return code in acceptable

def docker_ps():
    """Get running containers."""
    try:
        result = subprocess.run(
            ["docker", "ps", "--format", "{{.Names}}\t{{.Status}}"],
            capture_output=True, text=True, timeout=15
        )
        if result.returncode == 0:
            lines = [l for l in result.stdout.strip().split("\n") if l]
            return {parts[0]: parts[1] for parts in (l.split("\t", 1) for l in lines)}
    except Exception as e:
        log(f"docker ps failed: {e}")
    return {}

def docker_compose_up():
    """Try to start containers via docker-compose."""
    compose_paths = [
        r"C:\Users\alfir\vectorizer\docker-compose.yml",
        r"C:\Users\alfir\vectorizer\docker-compose.yaml",
    ]
    for cp in compose_paths:
        if os.path.exists(cp):
            log(f"Found docker-compose at {cp}, starting...")
            try:
                result = subprocess.run(
                    ["docker", "compose", "-f", cp, "up", "-d"],
                    capture_output=True, text=True, timeout=120,
                    cwd=os.path.dirname(cp)
                )
                if result.returncode != 0:
                    result = subprocess.run(
                        ["docker-compose", "-f", cp, "up", "-d"],
                        capture_output=True, text=True, timeout=120,
                        cwd=os.path.dirname(cp)
                    )
                log(f"  stdout: {result.stdout.strip()}")
                if result.stderr.strip():
                    log(f"  stderr: {result.stderr.strip()[:500]}")
                return result.returncode == 0
            except Exception as e:
                log(f"docker-compose up failed: {e}")

    # Try docker run for each image we know about
    log("No compose file found, trying individual containers...")
    
    # ChromaDB container
    try:
        result = subprocess.run(
            ["docker", "run", "-d", "--name", "vectorizer-chromadb",
             "-p", "8100:8000",
             "-v", "vectorizer_chroma_data:/chroma/chroma",
             "chromadb/chroma:1.0.0"],
            capture_output=True, text=True, timeout=30
        )
        if result.returncode == 0 or "already in use" in (result.stderr or ""):
            log("  Started chromadb container")
        else:
            log(f"  ChromaDB start failed: {result.stderr[:200]}")
    except Exception as e:
        log(f"ChromaDB start error: {e}")

    # Vectorizer container (internal port 8091, mapped to host 8092)
    try:
        cmd = ["docker", "run", "-d", "--name", "vectorizer-vectorizer",
               "-p", "8092:8091", "-p", "50051:50051",
               "vectorizer-vectorizer:latest"]
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        if result.returncode == 0 or "already in use" in (result.stderr or ""):
            log("  Started vectorizer container")
        else:
            log(f"  Vectorizer start failed: {result.stderr[:200]}")
    except Exception as e:
        log(f"Vectorizer start error: {e}")

    # Dashboard container (internal port 8092, mapped to host 8094)
    try:
        result = subprocess.run(
            ["docker", "run", "-d", "--name", "vectorizer-dashboard",
             "-p", "8094:8092",
             "vectorizer-dashboard:latest"],
            capture_output=True, text=True, timeout=30
        )
        if result.returncode == 0 or "already in use" in (result.stderr or ""):
            log("  Started dashboard container")
        else:
            log(f"  Dashboard start failed: {result.stderr[:200]}")
    except Exception as e:
        log(f"Dashboard start error: {e}")

    return True

def send_email(subject, body):
    """Send email alert using SMTP."""
    env = read_env(ENV_FILE)
    smtp_host = env.get("SMTP_HOST", "smtp.gmail.com")
    smtp_port = int(env.get("SMTP_PORT", "587"))
    smtp_user = env.get("SMTP_USER", "")
    smtp_pass = env.get("SMTP_PASS", "")
    
    if not smtp_user or not smtp_pass:
        log(f"EMAIL SKIP: SMTP_USER/PASS not set in .env -> would send to {ALERT_EMAIL} subj {subject}")
        return False
    
    from email.mime.text import MIMEText
    from email.mime.multipart import MIMEMultipart
    import smtplib
    
    msg = MIMEMultipart()
    msg["From"] = smtp_user
    msg["To"] = ALERT_EMAIL
    msg["Subject"] = f"≡ƒÜ¿ Vectorizer Health Alert: {subject}"
    msg.attach(MIMEText(body, "plain"))
    
    try:
        with smtplib.SMTP(smtp_host, smtp_port) as server:
            server.starttls()
            server.login(smtp_user, smtp_pass)
            server.sendmail(smtp_user, ALERT_EMAIL, msg.as_string())
        log(f"EMAIL SENT to {ALERT_EMAIL}")
        return True
    except Exception as e:
        log(f"EMAIL FAILED: {e}")
        return False

def send_telegram(message):
    """Send Telegram bot message."""
    if not TELEGRAM_TOKEN or not TELEGRAM_CHAT_ID:
        log("TELEGRAM SKIP: TOKEN/CHAT_ID not set in env")
        return False
    
    url = f"https://api.telegram.org/bot{TELEGRAM_TOKEN}/sendMessage"
    payload = {
        "chat_id": TELEGRAM_CHAT_ID,
        "text": message,
        "parse_mode": "HTML"
    }
    
    try:
        data = json.dumps(payload).encode()
        req = urllib.request.Request(url, data=data, method="POST")
        req.add_header("Content-Type", "application/json")
        with urllib.request.urlopen(req, timeout=10) as resp:
            log(f"TELEGRAM SENT to {TELEGRAM_CHAT_ID}")
            return True
    except Exception as e:
        log(f"TELEGRAM FAILED: {e}")
        return False

# ΓöÇΓöÇ Main ΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇΓöÇ
def main():
    log("=" * 60)
    log("Vectorizer Health Check")
    log("=" * 60)
    
    # Step 1: Check current status
    results = {}
    for name, url, expected in SERVICES:
        code, err = check_service(name, url)
        healthy = is_healthy(name, code) if code else False
        results[name] = {"code": code, "error": err, "healthy": healthy}
        status_str = f"Γ£à {code}" if healthy else f"Γ¥î {code or 'down'}{f' ({err})' if err else ''}"
        log(f"  {name}: {status_str}")
    
    # Step 2: Check Docker containers
    running = docker_ps()
    log(f"\nRunning containers: {list(running.keys()) if running else '(none)'}")
    
    # Step 3: Auto-heal ΓÇö try to start containers
    unhealthy = [name for name, r in results.items() if not r["healthy"]]
    if unhealthy:
        log(f"\nUnhealthy services: {unhealthy}")
        log("Attempting auto-heal...")
        docker_compose_up()
        
        # Wait a moment for containers to start
        log("Waiting 15s for containers to initialize...")
        import time
        time.sleep(15)
    
    # Step 4: Re-check after heal attempt
    log("\nRe-checking after auto-heal:")
    post_results = {}
    for name, url, expected in SERVICES:
        code, err = check_service(name, url)
        healthy = is_healthy(name, code) if code else False
        post_results[name] = {"code": code, "error": err, "healthy": healthy}
        status_str = f"Γ£à {code}" if healthy else f"Γ¥î {code or 'down'}{f' ({err})' if err else ''}"
        log(f"  {name}: {status_str}")
    
    # Step 5: Determine final health
    still_unhealthy = [name for name, r in post_results.items() if not r["healthy"]]
    
    summary_parts = []
    for name, url, expected in SERVICES:
        code = post_results[name]["code"]
        healthy = post_results[name]["healthy"]
        err = post_results[name].get("error", "")
        if healthy:
            summary_parts.append(f"{name}:Γ£à")
        else:
            summary_parts.append(f"{name}:{code or 'down'}! ({err})"[:40])
    
    health_summary = " | ".join(summary_parts)
    
    # Step 6: Alert if still unhealthy
    if still_unhealthy:
        log(f"\n≡ƒÜ¿ STILL UNHEALTHY: {still_unhealthy}")
        
        alert_body = f"""Vectorizer Health Check ΓÇö {datetime.datetime.now().strftime('%Y-%m-%d %H:%M')}

Services after auto-heal attempt:
{chr(10).join(f"  {'Γ£à' if post_results[n]['healthy'] else 'Γ¥î'} {n}: {post_results[n]['code'] or 'down'} ΓÇö {post_results[n].get('error', 'N/A')}" for n, _, expected in SERVICES)}

Still unhealthy: {', '.join(still_unhealthy)}

Please check Docker and Vectorizer manually."""
        
        alert_msg = f"≡ƒÜ¿ Vectorizer health: {health_summary} -> {', '.join(still_unhealthy)} still down"
        
        send_email(health_summary, alert_body)
        send_telegram(alert_msg)
    else:
        log(f"\nΓ£à All services healthy!")
    
    # Print final summary for cron output
    print(f"\nHEALTH {health_summary}")
    if still_unhealthy:
        print(f"≡ƒÜ¿ Vectorizer health: {health_summary} -> {', '.join(still_unhealthy)} still down")
    else:
        print("Γ£à All services healthy!")
    
    return 0 if not still_unhealthy else 1

if __name__ == "__main__":
    sys.exit(main())
