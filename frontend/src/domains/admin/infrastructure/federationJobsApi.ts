import client from '@/shared/api/client'
export interface Page<T> { items:T[]; total:number }
export type InstanceStatus='federating'|'subscribing'|'publishing'|'failing'|'idle'
export interface Instance {domain:string;name?:string;software?:string;version?:string;icon_url?:string;status?:InstanceStatus;accounts:number;suspended_accounts:number;following:number;followers:number;last_activity:string|null;first_seen?:string|null;metadata_fetched_at?:string|null;metadata_failed_at?:string|null}
export interface InstanceQuery {q?:string;status?:string;sort?:string}
export interface Job {parent_id?:string;batch_no?:number;can_retry?:boolean;requires_reconciliation?:boolean;lease_expires_at?:string|null;id:string;kind:string;state:string;event?:string;activity_id?:string;activity_type?:string;destination?:string;attempts:number;last_error:string;next_attempt_at:string|null;created_at:string}
export interface QueueCounts {active:number;waiting:number;delayed:number;failed:number;completed:number;cancelled:number}
export interface QueuePoint {at:string;processed:number;failed:number;active:number;waiting:number;delayed:number}
export interface QueueStat {kind:string;label:string;counts:QueueCounts;series:QueuePoint[];bulk_actions:boolean;hosts:boolean}
export interface ErroredHost {host:string;failed:number;delayed:number;total:number;last_error:string}
export interface Webhook {id:string;url:string;description:string;events:string[];enabled:boolean}
export interface HookDelivery {id:string;subscription_id:string;state:string;event:string;attempts:number;last_error:string;created_at:string}
const base='/api/v1/admin'
export const federationJobsApi={
 async instances(page:number,q:string|InstanceQuery,limit=30){const f=typeof q==='string'?{q}:q;const params:Record<string,string|number>={page,limit};for(const [k,v] of Object.entries(f))if(v)params[k]=v;return(await client.get<Page<Instance>>(`${base}/federation/instances`,{params})).data},
 async instanceActivity(domains:string[]){return(await client.get<{days:number;items:Record<string,number[]>}>(`${base}/federation/instances/activity`,{params:{domains:domains.join(',')}})).data},
 async jobs(page:number,kind:string,state:string,q=''){return(await client.get<Page<Job>>(`${base}/federation/jobs`,{params:{page,kind,state,limit:30,...(q?{q}:{})}})).data},
 async queueStats(){return(await client.get<{queues:QueueStat[];interval_seconds:number}>(`${base}/federation/queue-stats`)).data},
 async queueHosts(kind:string){return(await client.get<{items:ErroredHost[]}>(`${base}/federation/queue-hosts`,{params:{kind}})).data},
 async queueBulk(kind:string,action:'promote'|'clear',host=''){return(await client.post<{updated:number}>(`${base}/federation/queue-bulk/${encodeURIComponent(kind)}`,{action,confirm:action==='clear',...(host?{host}:{})})).data},
 async jobAction(job:Job,action:'retry'|'cancel'){return(await client.post<{updated:boolean;reconciled?:boolean}>(`${base}/federation/jobs/${job.kind}/${encodeURIComponent(job.id)}/action`,{action,confirm:action==='cancel'})).data},
 async hooks(page=1){return(await client.get<Page<Webhook>>(`${base}/webhooks`,{params:{page,limit:30}})).data},
 async createHook(value:{url:string;description:string;events:string[];enabled:boolean}){return(await client.post<{subscription:Webhook;secret:string}>(`${base}/webhooks`,value)).data},
 async updateHook(id:string,value:Partial<Pick<Webhook,'enabled'|'description'|'events'>>){await client.put(`${base}/webhooks/${encodeURIComponent(id)}`,value)},
 async hookAction(id:string,action:'rotate'|'test'){return(await client.post<{secret?:string}>(`${base}/webhooks/${encodeURIComponent(id)}/${action}`,{confirm:true})).data},
 async deliveries(page:number,subscription_id:string,state:string){return(await client.get<Page<HookDelivery>>(`${base}/webhook-deliveries`,{params:{page,subscription_id,state,limit:30}})).data},
 async deliveryAction(id:string,action:'retry'|'cancel'){await client.post(`${base}/webhook-deliveries/${encodeURIComponent(id)}/action`,{action,confirm:action==='cancel'})},
}
