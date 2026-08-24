export type VectorizerOpts = { baseUrl?: string; apiKey?: string; workspaceId?: string; fetch?: typeof fetch };
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
  workspace(id?: string) { return id ?? this.workspaceId ?? "default"; }
  session(id: string){ return new Session(this, id); }
  async addMessages(msgs: {workspace_id:string;session_id:string;role:string;content:string}[]){ return this.req("/api/v1/messages/batch",{method:"POST",body:JSON.stringify({messages:msgs})});}
  async search(query:string, opts: {workspace_id?:string;session_id?:string;n_results?:number}={}){ const where:Record<string,string>={}; if(opts.workspace_id) where.workspace_id=opts.workspace_id; if(opts.session_id) where.session_id=opts.session_id; return this.req<{results:unknown[]}>("/api/v1/messages/search",{method:"POST",body:JSON.stringify({query,n_results:opts.n_results??5,where})});}
  async context(query:string, opts:{workspace_id?:string;tokens?:number}={}){ const r=await this.search(query,{workspace_id:opts.workspace_id??this.workspaceId, n_results:5}); return r;}
  async health(){ return this.req("/api/v1/health");}
}
class Session {
  constructor(private v: Vectorizer, public id:string){}
  async addMessages(msgs:{role:string;content:string}[], workspaceId?:string){ const wid=workspaceId??this.v.workspaceId??"default"; return this.v.addMessages(msgs.map(m=>({workspace_id:wid,session_id:this.id,role:m.role,content:m.content})));}
  async context(opts:{tokens?:number}={}){ return this.v.context("",{workspace_id: this.v.workspaceId});}
}
export default Vectorizer;
