"""Vectorizer Python SDK — full REST surface client.

Covers: health/metrics, messages (CRUD, batch, search, upload, grep,
temporal, analytics), sessions, workspaces, chat (agentic + peers),
conclusions/representations, code index/symbols/callers, scopes, keys,
admin embedding hot-swap, webhooks, and brain endpoints.

Backwards compatible: ``search``, ``add_messages`` and ``health`` keep
their original signatures.
"""
import os

import httpx


class Vectorizer:
    def __init__(self, base_url=None, api_key=None, workspace_id=None, timeout=30.0):
        self.base_url = (base_url or os.getenv("VECTORIZER_URL") or "http://localhost:8091").rstrip("/")
        self.api_key = api_key or os.getenv("VECTORIZER_API_KEY")
        self.workspace_id = workspace_id or os.getenv("VECTORIZER_WORKSPACE_ID")
        self.timeout = timeout
        self._headers = {"Content-Type": "application/json"}
        if self.api_key:
            self._headers["X-API-Key"] = self.api_key

    # -- core ----------------------------------------------------------
    def _req(self, path, method="GET", **kw):
        kw.setdefault("headers", self._headers)
        kw.setdefault("timeout", self.timeout)
        with httpx.Client() as c:
            r = c.request(method, self.base_url + path, **kw)
            r.raise_for_status()
            return r.json() if r.text else None

    def workspace(self, id=None):
        return id or self.workspace_id or "default"

    # -- health / ops --------------------------------------------------
    def health(self):
        return self._req("/api/v1/health")

    def metrics(self):
        with httpx.Client() as c:
            r = c.get(self.base_url + "/api/v1/metrics",
                      headers=self._headers, timeout=self.timeout)
            r.raise_for_status()
            return r.text

    def usage_daily(self, days=30):
        return self._req("/api/v1/usage/daily?days=%d" % days)

    # -- messages ------------------------------------------------------
    def add_message(self, workspace_id, session_id, role, content, **extra):
        return self._req("/api/v1/messages", method="POST", json={
            "workspace_id": workspace_id, "session_id": session_id,
            "role": role, "content": content, **extra})

    def add_messages(self, messages):
        return self._req("/api/v1/messages/batch", method="POST",
                         json={"messages": messages})

    def get_message(self, message_id):
        return self._req("/api/v1/messages/%s" % message_id)

    def list_messages(self, workspace_id=None, session_id=None, limit=50, offset=0):
        params = {"limit": limit, "offset": offset}
        if workspace_id:
            params["workspace_id"] = workspace_id
        if session_id:
            params["session_id"] = session_id
        return self._req("/api/v1/messages", params=params)

    def update_message(self, message_id, content):
        return self._req("/api/v1/messages/%s" % message_id,
                         method="PUT", json={"content": content})

    def delete_message(self, message_id):
        return self._req("/api/v1/messages/%s" % message_id, method="DELETE")

    def search(self, query, workspace_id=None, session_id=None, n_results=5,
               role=None, scope=None):
        where = {}
        if workspace_id:
            where["workspace_id"] = workspace_id
        if session_id:
            where["session_id"] = session_id
        body = {"query": query, "n_results": n_results, "where": where}
        if role:
            body["role"] = role
        if scope:
            body["scope"] = scope
        return self._req("/api/v1/messages/search", method="POST", json=body)

    def search_all(self, query, n_results=5):
        return self._req("/api/v1/messages/search/all", method="POST",
                         json={"query": query, "n_results": n_results})

    def analytics(self, workspace_id=None):
        params = {"workspace_id": workspace_id} if workspace_id else None
        return self._req("/api/v1/messages/analytics", params=params)

    def grep(self, pattern, workspace_id=None, session_id=None):
        params = {"pattern": pattern}
        if workspace_id:
            params["workspace_id"] = workspace_id
        if session_id:
            params["session_id"] = session_id
        return self._req("/api/v1/messages/grep", params=params)

    def temporal(self, workspace_id, session_id, before=None, after=None, limit=50):
        params = {"workspace_id": workspace_id, "session_id": session_id,
                  "limit": limit}
        if before:
            params["before"] = before
        if after:
            params["after"] = after
        return self._req("/api/v1/messages/temporal", params=params)

    def pending_updates(self):
        return self._req("/api/v1/messages/pending")

    # -- sessions ------------------------------------------------------
    def create_session(self, workspace_id, session_id, title=None,
                       peer_ids=None, scope=None):
        body = {"workspace_id": workspace_id, "session_id": session_id}
        if title:
            body["title"] = title
        if peer_ids:
            body["peer_ids"] = peer_ids
        if scope:
            body["scope"] = scope
        return self._req("/api/v1/sessions", method="POST", json=body)

    def list_sessions(self, workspace_id):
        return self._req("/api/v1/sessions", params={"workspace_id": workspace_id})

    def session_context(self, workspace_id, session_id, tokens=10000):
        return self._req(
            "/api/v1/workspaces/%s/sessions/%s/context?tokens=%d"
            % (workspace_id, session_id, tokens))

    # -- workspaces ----------------------------------------------------
    def list_workspaces(self):
        return self._req("/api/v1/workspaces")

    def create_workspace(self, workspace_id, **extra):
        return self._req("/api/v1/workspaces", method="POST",
                         json={"id": workspace_id, **extra})

    def get_workspace(self, workspace_id):
        return self._req("/api/v1/workspaces/%s" % workspace_id)

    def update_workspace(self, workspace_id, metadata):
        return self._req("/api/v1/workspaces/%s" % workspace_id,
                         method="PUT", json={"metadata": metadata})

    def delete_workspace(self, workspace_id):
        return self._req("/api/v1/workspaces/%s" % workspace_id, method="DELETE")

    def workspace_health(self, workspace_id):
        return self._req("/api/v1/workspaces/%s/health" % workspace_id)

    def workspace_stats(self, workspace_id):
        return self._req("/api/v1/workspaces/%s/stats" % workspace_id)

    def workspace_search(self, workspace_id, query, n_results=5):
        return self._req("/api/v1/workspaces/%s/search" % workspace_id,
                         method="POST",
                         json={"query": query, "n_results": n_results})

    def dream(self, workspace_id):
        return self._req("/api/v1/workspaces/%s/dream" % workspace_id,
                         method="POST")

    # -- chat (agentic) + peers ----------------------------------------
    def chat(self, workspace_id, query, observer=None, observed=None,
             session_id=None, reasoning_level="low"):
        body = {"query": query, "reasoning_level": reasoning_level}
        if observer:
            body["observer"] = observer
        if observed:
            body["observed"] = observed
        if session_id:
            body["session_id"] = session_id
        return self._req("/api/v1/workspaces/%s/chat" % workspace_id,
                         method="POST", json=body)

    def peer_chat(self, workspace_id, peer_id, query, **kw):
        return self._req(
            "/api/v1/workspaces/%s/peers/%s/chat" % (workspace_id, peer_id),
            method="POST", json={"query": query, **kw})

    def create_peer(self, workspace_id, **fields):
        return self._req("/api/v1/workspaces/%s/peers" % workspace_id,
                         method="POST", json=fields)

    def list_peers(self, workspace_id):
        return self._req("/api/v1/workspaces/%s/peers" % workspace_id)

    def update_peer(self, workspace_id, peer_id, **fields):
        return self._req(
            "/api/v1/workspaces/%s/peers/%s" % (workspace_id, peer_id),
            method="PUT", json=fields)

    def peer_sessions(self, workspace_id, peer_id):
        return self._req(
            "/api/v1/workspaces/%s/peers/%s/sessions" % (workspace_id, peer_id))

    def set_peer_card(self, workspace_id, peer_id, card):
        return self._req(
            "/api/v1/workspaces/%s/peers/%s/card" % (workspace_id, peer_id),
            method="PUT", json=card if isinstance(card, dict) else {"card": card})

    def get_peer_card(self, workspace_id, peer_id):
        return self._req(
            "/api/v1/workspaces/%s/peers/%s/card" % (workspace_id, peer_id))

    # -- conclusions / reasoning graph ---------------------------------
    def create_conclusion(self, workspace_id, content, peer_id=None, metadata=None):
        body = {"workspace_id": workspace_id, "content": content}
        if peer_id:
            body["peer_id"] = peer_id
        if metadata:
            body["metadata"] = metadata
        return self._req("/api/v1/conclusions", method="POST", json=body)

    def batch_conclusions(self, conclusions):
        return self._req("/api/v1/conclusions/batch", method="POST",
                         json={"conclusions": conclusions})

    def query_conclusions(self, workspace_id, query, n_results=5):
        return self._req("/api/v1/conclusions/query", method="POST", json={
            "workspace_id": workspace_id, "query": query,
            "n_results": n_results})

    def list_conclusions(self, workspace_id, peer_id=None, limit=25, offset=0):
        params = {"workspace_id": workspace_id, "limit": limit, "offset": offset}
        if peer_id:
            params["peer_id"] = peer_id
        return self._req("/api/v1/conclusions", params=params)

    def delete_conclusion(self, conclusion_id, workspace_id):
        return self._req("/api/v1/conclusions/%s?workspace_id=%s"
                         % (conclusion_id, workspace_id), method="DELETE")

    def representation(self, workspace_id, peer_id=None, session_id=None,
                       max_conclusions=25):
        params = {"workspace_id": workspace_id,
                  "max_conclusions": max_conclusions}
        if peer_id:
            params["peer_id"] = peer_id
        if session_id:
            params["session_id"] = session_id
        return self._req("/api/v1/representations", params=params)

    def trace(self, workspace_id, conclusion_id, direction="forward"):
        return self._req("/api/v1/conclusions/trace", params={
            "workspace_id": workspace_id, "id": conclusion_id,
            "direction": direction})

    def stale_conclusions(self, workspace_id):
        return self._req("/api/v1/conclusions/stale",
                         params={"workspace_id": workspace_id})

    def conclusions_brief(self, workspace_id):
        return self._req("/api/v1/conclusions/brief",
                         params={"workspace_id": workspace_id})

    # -- code index ----------------------------------------------------
    def code_index(self, path, workspace_id=None, session_id=None):
        body = {"path": path}
        if workspace_id:
            body["workspace_id"] = workspace_id
        if session_id:
            body["session_id"] = session_id
        return self._req("/api/v1/code/index", method="POST", json=body)

    def code_symbols(self, workspace_id, query=None):
        params = {"workspace_id": workspace_id}
        if query:
            params["query"] = query
        return self._req("/api/v1/code/symbols", params=params)

    def code_callers(self, workspace_id, symbol):
        return self._req("/api/v1/code/callers",
                         params={"workspace_id": workspace_id, "symbol": symbol})

    # -- scopes --------------------------------------------------------
    def create_scope(self, workspace_id, name, sessions=None):
        return self._req("/api/v1/workspaces/%s/scopes" % workspace_id,
                         method="POST",
                         json={"name": name, "sessions": sessions or []})

    def list_scopes(self, workspace_id):
        return self._req("/api/v1/workspaces/%s/scopes" % workspace_id)

    def get_scope(self, workspace_id, scope_id):
        return self._req("/api/v1/workspaces/%s/scopes/%s"
                         % (workspace_id, scope_id))

    def scope_add_sessions(self, workspace_id, scope_id, sessions):
        return self._req(
            "/api/v1/workspaces/%s/scopes/%s/sessions" % (workspace_id, scope_id),
            method="POST", json={"sessions": sessions})

    def scope_remove_session(self, workspace_id, scope_id, session_id):
        return self._req(
            "/api/v1/workspaces/%s/scopes/%s/sessions/%s"
            % (workspace_id, scope_id, session_id), method="DELETE")

    def scope_sessions(self, workspace_id, scope_id):
        return self._req(
            "/api/v1/workspaces/%s/scopes/%s/sessions" % (workspace_id, scope_id))

    def scope_status(self, workspace_id, scope_id):
        return self._req(
            "/api/v1/workspaces/%s/scopes/%s/status" % (workspace_id, scope_id))

    # -- keys ----------------------------------------------------------
    def create_key(self, workspace_id=None, name=None):
        body = {}
        if workspace_id:
            body["workspace_id"] = workspace_id
        if name:
            body["name"] = name
        if workspace_id:
            return self._req(
                "/api/v1/workspaces/%s/keys" % workspace_id,
                method="POST", json=body)
        return self._req("/api/v1/keys", method="POST", json=body)

    def list_keys(self, workspace_id=None):
        if workspace_id:
            return self._req("/api/v1/workspaces/%s/keys" % workspace_id)
        params = {"workspace_id": workspace_id} if workspace_id else None
        return self._req("/api/v1/keys", params=params)

    # -- admin / webhooks / brain --------------------------------------
    def get_embedding(self):
        return self._req("/api/v1/admin/embedding")

    def set_embedding(self, model=None, base_url=None):
        body = {}
        if model:
            body["model"] = model
        if base_url:
            body["base_url"] = base_url
        return self._req("/api/v1/admin/embedding", method="POST", json=body)

    def register_webhook(self, workspace_id, url, events=None):
        body = {"workspace_id": workspace_id, "url": url}
        if events:
            body["events"] = events
        return self._req("/api/v1/webhooks", method="POST", json=body)

    def list_webhooks(self, workspace_id=None):
        params = {"workspace_id": workspace_id} if workspace_id else None
        return self._req("/api/v1/webhooks", params=params)

    def brain_summarize(self, text, max_chars=400):
        return self._req("/api/v1/brain/summarize", method="POST",
                         json={"text": text, "max_chars": max_chars})

    def brain_ask(self, question, context=None):
        body = {"question": question}
        if context:
            body["context"] = context
        return self._req("/api/v1/brain/ask", method="POST", json=body)
