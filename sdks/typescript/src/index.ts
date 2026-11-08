export type VectorizerOpts = { baseUrl?: string; apiKey?: string; workspaceId?: string; fetch?: typeof fetch };
export type Msg = { workspace_id: string; session_id: string; role: string; content: string; scope?: string; peer_id?: string; metadata?: Record<string, unknown> };
export class Vectorizer {
  private baseUrl: string; private apiKey?: string; workspaceId?: string; private f: typeof fetch;
  constructor(opts: VectorizerOpts = {}) {
    this.baseUrl = (opts.baseUrl ?? process.env.VECTORIZER_URL ?? "http://localhost:8091").replace(/\/$/,"");
    this.apiKey = opts.apiKey ?? process.env.VECTORIZER_API_KEY;
    this.workspaceId = opts.workspaceId; this.f = opts.fetch ?? fetch;
  }
  private h(): Record<string,string> { const h: Record<string,string> = {"Content-Type":"application/json"}; if(this.apiKey) h["X-API-Key"]=this.apiKey; return h; }
  private async req<T>(path: string, init?: RequestInit): Promise<T> {
    const r = await this.f(`${this.baseUrl}${path}`, { ...init, headers:{...this.h(),...(init?.headers as Record<string,string>??{})}});
    const t=await r.text(); const j=t?JSON.parse(t):null; if(!r.ok) throw new Error(`${r.status} ${(j as {error?:string})?.error ?? t}`); return j as T;
  }
  private q(params: Record<string, string|number|undefined>): string {
    const s = Object.entries(params).filter(([,v])=>v!==undefined).map(([k,v])=>`${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`).join("&");
    return s ? `?${s}` : "";
  }
  workspace(id?: string) { return id ?? this.workspaceId ?? "default"; }
  session(id: string){ return new Session(this, id); }
  // -- health / ops --
  async health(){ return this.req("/api/v1/health");}
  async metrics(): Promise<string> {
    const r = await this.f(`${this.baseUrl}/api/v1/metrics`, { headers: this.h() });
    const t = await r.text(); if(!r.ok) throw new Error(`${r.status} ${t}`); return t;
  }
  async usageDaily(days=30){ return this.req(`/api/v1/usage/daily?days=${days}`);}
  // -- messages --
  async addMessage(m: Msg){ return this.req("/api/v1/messages",{method:"POST",body:JSON.stringify(m)});}
  async addMessages(msgs: Msg[]){ return this.req("/api/v1/messages/batch",{method:"POST",body:JSON.stringify({messages:msgs})});}
  async getMessage(id: string){ return this.req(`/api/v1/messages/${encodeURIComponent(id)}`);}
  async listMessages(opts: {workspace_id?:string;session_id?:string;limit?:number;offset?:number}={}){ return this.req("/api/v1/messages"+this.q({workspace_id:opts.workspace_id,session_id:opts.session_id,limit:opts.limit??50,offset:opts.offset??0}));}
  async updateMessage(id: string, content: string){ return this.req(`/api/v1/messages/${encodeURIComponent(id)}`,{method:"PUT",body:JSON.stringify({content})});}
  async deleteMessage(id: string){ return this.req(`/api/v1/messages/${encodeURIComponent(id)}`,{method:"DELETE"});}
  async search(query:string, opts: {workspace_id?:string;session_id?:string;n_results?:number;role?:string;scope?:string}={}){ const where:Record<string,string>={}; if(opts.workspace_id) where.workspace_id=opts.workspace_id; if(opts.session_id) where.session_id=opts.session_id; return this.req<{results:unknown[]}>("/api/v1/messages/search",{method:"POST",body:JSON.stringify({query,n_results:opts.n_results??5,where,role:opts.role,scope:opts.scope})});}
  async searchAll(query: string, n_results=5){ return this.req("/api/v1/messages/search/all",{method:"POST",body:JSON.stringify({query,n_results})});}
  async analytics(workspace_id?: string){ return this.req("/api/v1/messages/analytics"+this.q({workspace_id}));}
  async grep(pattern: string, opts:{workspace_id?:string;session_id?:string}={}){ return this.req("/api/v1/messages/grep"+this.q({pattern,...opts}));}
  async temporal(workspace_id: string, session_id: string, opts:{before?:string;after?:string;limit?:number}={}){ return this.req("/api/v1/messages/temporal"+this.q({workspace_id,session_id,...opts}));}
  async pendingUpdates(){ return this.req("/api/v1/messages/pending");}
  async context(query:string, opts:{workspace_id?:string;tokens?:number}={}){ const r=await this.search(query,{workspace_id:opts.workspace_id??this.workspaceId, n_results:5}); return r;}
  // -- sessions --
  async createSession(workspace_id: string, session_id: string, opts:{title?:string;peer_ids?:string[];scope?:string}={}){ return this.req("/api/v1/sessions",{method:"POST",body:JSON.stringify({workspace_id,session_id,...opts})});}
  async listSessions(workspace_id: string){ return this.req("/api/v1/sessions"+this.q({workspace_id}));}
  async sessionContext(workspace_id: string, session_id: string, tokens=10000){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/sessions/${encodeURIComponent(session_id)}/context?tokens=${tokens}`);}
  // -- workspaces --
  async listWorkspaces(){ return this.req("/api/v1/workspaces");}
  async createWorkspace(id: string, extra: Record<string,unknown>={}){ return this.req("/api/v1/workspaces",{method:"POST",body:JSON.stringify({id,...extra})});}
  async getWorkspace(id: string){ return this.req(`/api/v1/workspaces/${encodeURIComponent(id)}`);}
  async updateWorkspace(id: string, metadata: Record<string,unknown>){ return this.req(`/api/v1/workspaces/${encodeURIComponent(id)}`,{method:"PUT",body:JSON.stringify({metadata})});}
  async deleteWorkspace(id: string){ return this.req(`/api/v1/workspaces/${encodeURIComponent(id)}`,{method:"DELETE"});}
  async workspaceHealth(id: string){ return this.req(`/api/v1/workspaces/${encodeURIComponent(id)}/health`);}
  async workspaceStats(id: string){ return this.req(`/api/v1/workspaces/${encodeURIComponent(id)}/stats`);}
  async workspaceSearch(id: string, query: string, n_results=5){ return this.req(`/api/v1/workspaces/${encodeURIComponent(id)}/search`,{method:"POST",body:JSON.stringify({query,n_results})});}
  async dream(id: string){ return this.req(`/api/v1/workspaces/${encodeURIComponent(id)}/dream`,{method:"POST"});}
  // -- chat (agentic) + peers --
  async chat(workspace_id: string, query: string, opts:{observer?:string;observed?:string;session_id?:string;reasoning_level?:string}={}){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/chat`,{method:"POST",body:JSON.stringify({query,reasoning_level:opts.reasoning_level??"low",...opts})});}
  async peerChat(workspace_id: string, peer_id: string, query: string, extra: Record<string,unknown>={}){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/peers/${encodeURIComponent(peer_id)}/chat`,{method:"POST",body:JSON.stringify({query,...extra})});}
  async createPeer(workspace_id: string, fields: Record<string,unknown>){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/peers`,{method:"POST",body:JSON.stringify(fields)});}
  async listPeers(workspace_id: string){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/peers`);}
  async updatePeer(workspace_id: string, peer_id: string, fields: Record<string,unknown>){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/peers/${encodeURIComponent(peer_id)}`,{method:"PUT",body:JSON.stringify(fields)});}
  async peerSessions(workspace_id: string, peer_id: string){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/peers/${encodeURIComponent(peer_id)}/sessions`);}
  async setPeerCard(workspace_id: string, peer_id: string, card: unknown){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/peers/${encodeURIComponent(peer_id)}/card`,{method:"PUT",body:JSON.stringify(card)});}
  async getPeerCard(workspace_id: string, peer_id: string){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/peers/${encodeURIComponent(peer_id)}/card`);}
  // -- conclusions / reasoning graph --
  async createConclusion(workspace_id: string, content: string, opts:{peer_id?:string;metadata?:Record<string,unknown>}={}){ return this.req("/api/v1/conclusions",{method:"POST",body:JSON.stringify({workspace_id,content,...opts})});}
  async batchConclusions(conclusions: {workspace_id:string;peer_id?:string;content:string}[]){ return this.req("/api/v1/conclusions/batch",{method:"POST",body:JSON.stringify({conclusions})});}
  async queryConclusions(workspace_id: string, query: string, n_results=5){ return this.req("/api/v1/conclusions/query",{method:"POST",body:JSON.stringify({workspace_id,query,n_results})});}
  async listConclusions(workspace_id: string, opts:{peer_id?:string;limit?:number;offset?:number}={}){ return this.req("/api/v1/conclusions"+this.q({workspace_id,peer_id:opts.peer_id,limit:opts.limit??25,offset:opts.offset??0}));}
  async deleteConclusion(id: string, workspace_id: string){ return this.req(`/api/v1/conclusions/${encodeURIComponent(id)}`+this.q({workspace_id}),{method:"DELETE"});}
  async representation(workspace_id: string, opts:{peer_id?:string;session_id?:string;max_conclusions?:number}={}){ return this.req("/api/v1/representations"+this.q({workspace_id,peer_id:opts.peer_id,session_id:opts.session_id,max_conclusions:opts.max_conclusions??25}));}
  async trace(workspace_id: string, id: string, direction="forward"){ return this.req("/api/v1/conclusions/trace"+this.q({workspace_id,id,direction}));}
  async staleConclusions(workspace_id: string){ return this.req("/api/v1/conclusions/stale"+this.q({workspace_id}));}
  async conclusionsBrief(workspace_id: string){ return this.req("/api/v1/conclusions/brief"+this.q({workspace_id}));}
  // -- code index --
  async codeIndex(path: string, opts:{workspace_id?:string;session_id?:string}={}){ return this.req("/api/v1/code/index",{method:"POST",body:JSON.stringify({path,...opts})});}
  async codeSymbols(workspace_id: string, query?: string){ return this.req("/api/v1/code/symbols"+this.q({workspace_id,query}));}
  async codeCallers(workspace_id: string, symbol: string){ return this.req("/api/v1/code/callers"+this.q({workspace_id,symbol}));}
  // -- scopes --
  async createScope(workspace_id: string, name: string, sessions: string[]=[]){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/scopes`,{method:"POST",body:JSON.stringify({name,sessions})});}
  async listScopes(workspace_id: string){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/scopes`);}
  async getScope(workspace_id: string, scope_id: string){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/scopes/${encodeURIComponent(scope_id)}`);}
  async scopeAddSessions(workspace_id: string, scope_id: string, sessions: string[]){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/scopes/${encodeURIComponent(scope_id)}/sessions`,{method:"POST",body:JSON.stringify({sessions})});}
  async scopeRemoveSession(workspace_id: string, scope_id: string, session_id: string){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/scopes/${encodeURIComponent(scope_id)}/sessions/${encodeURIComponent(session_id)}`,{method:"DELETE"});}
  async scopeSessions(workspace_id: string, scope_id: string){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/scopes/${encodeURIComponent(scope_id)}/sessions`);}
  async scopeStatus(workspace_id: string, scope_id: string){ return this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/scopes/${encodeURIComponent(scope_id)}/status`);}
  // -- keys --
  async createKey(workspace_id?: string, name?: string){ const body:{workspace_id?:string;name?:string}={}; if(workspace_id) body.workspace_id=workspace_id; if(name) body.name=name; return workspace_id ? this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/keys`,{method:"POST",body:JSON.stringify(body)}) : this.req("/api/v1/keys",{method:"POST",body:JSON.stringify(body)});}
  async listKeys(workspace_id?: string){ return workspace_id ? this.req(`/api/v1/workspaces/${encodeURIComponent(workspace_id)}/keys`) : this.req("/api/v1/keys");}
  // -- admin / webhooks / brain --
  async getEmbedding(){ return this.req("/api/v1/admin/embedding");}
  async setEmbedding(opts:{model?:string;base_url?:string}){ return this.req("/api/v1/admin/embedding",{method:"POST",body:JSON.stringify(opts)});}
  async registerWebhook(workspace_id: string, url: string, events?: string[]){ return this.req("/api/v1/webhooks",{method:"POST",body:JSON.stringify({workspace_id,url,events})});}
  async listWebhooks(workspace_id?: string){ return this.req("/api/v1/webhooks"+this.q({workspace_id}));}
  async brainSummarize(text: string, max_chars=400){ return this.req("/api/v1/brain/summarize",{method:"POST",body:JSON.stringify({text,max_chars})});}
  async brainAsk(question: string, context?: string){ return this.req("/api/v1/brain/ask",{method:"POST",body:JSON.stringify({question,context})});}
}
class Session {
  constructor(private v: Vectorizer, public id:string){}
  async addMessages(msgs:{role:string;content:string}[], workspaceId?:string){ const wid=workspaceId??this.v.workspaceId??"default"; return this.v.addMessages(msgs.map(m=>({workspace_id:wid,session_id:this.id,role:m.role,content:m.content})));}
  async context(opts:{tokens?:number}={}){ return this.v.context("",{workspace_id: this.v.workspaceId});}
}
export default Vectorizer;
