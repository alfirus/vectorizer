import os
import httpx
class Vectorizer:
    def __init__(self, base_url=None, api_key=None, workspace_id=None):
        self.base_url=(base_url or os.getenv("VECTORIZER_URL") or "http://localhost:8091").rstrip("/")
        self.api_key=api_key or os.getenv("VECTORIZER_API_KEY")
        self.workspace_id=workspace_id or os.getenv("VECTORIZER_WORKSPACE_ID")
        self._headers={"Content-Type":"application/json"}
        if self.api_key: self._headers["X-API-Key"]=self.api_key
    def _req(self, path, **kw):
        kw.setdefault("headers", self._headers)
        with httpx.Client() as c:
            r=c.request("GET" if "method" not in kw else kw.pop("method"), self.base_url+path, **kw)
            r.raise_for_status()
            return r.json() if r.text else None
    def workspace(self, id=None): return id or self.workspace_id or "default"
    def search(self, query, workspace_id=None, session_id=None, n_results=5):
        where={}
        if workspace_id: where["workspace_id"]=workspace_id
        if session_id: where["session_id"]=session_id
        return self._req("/api/v1/messages/search", method="POST", json={"query":query,"n_results":n_results,"where":where})
    def add_messages(self, messages): return self._req("/api/v1/messages/batch", method="POST", json={"messages":messages})
    def health(self): return self._req("/api/v1/health")
