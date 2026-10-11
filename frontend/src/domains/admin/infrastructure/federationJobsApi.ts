import client from '@/shared/api/client'
export interface Page<T> { items:T[]; total:number }
export interface Instance {domain:string;accounts:number;suspended_accounts:number;following:number;followers:number;last_activity:string|null}
export interface Job {parent_id?:string;batch_no?:number;can_retry?:boolean;requires_reconciliation?:boolean;lease_expires_at?:string|null;id:string;kind:string;state:string;event?:string;activity_id?:string;destination?:string;attempts:number;last_error:string;next_attempt_at:string|null;created_at:string}
export interface Webhook {id:string;url:string;description:string;events:string[];enabled:boolean}
export interface HookDelivery {id:string;subscription_id:string;state:string;event:string;attempts:number;last_error:string;created_at:string}
const base='/api/v1/admin'
export const federationJobsApi={
 async instances(page:number,q:string){return(await client.get<Page<Instance>>(`${base}/federation/instances`,{params:{page,q,limit:30}})).data},
 async jobs(page:number,kind:string,state:string){return(await client.get<Page<Job>>(`${base}/federation/jobs`,{params:{page,kind,state,limit:30}})).data},
 async jobAction(job:Job,action:'retry'|'cancel'){return(await client.post<{updated:boolean;reconciled?:boolean}>(`${base}/federation/jobs/${job.kind}/${encodeURIComponent(job.id)}/action`,{action,confirm:action==='cancel'})).data},
 async hooks(page=1){return(await client.get<Page<Webhook>>(`${base}/webhooks`,{params:{page,limit:30}})).data},
 async createHook(value:{url:string;description:string;events:string[];enabled:boolean}){return(await client.post<{subscription:Webhook;secret:string}>(`${base}/webhooks`,value)).data},
 async updateHook(id:string,value:Partial<Pick<Webhook,'enabled'|'description'|'events'>>){await client.put(`${base}/webhooks/${encodeURIComponent(id)}`,value)},
 async hookAction(id:string,action:'rotate'|'test'){return(await client.post<{secret?:string}>(`${base}/webhooks/${encodeURIComponent(id)}/${action}`,{confirm:true})).data},
 async deliveries(page:number,subscription_id:string,state:string){return(await client.get<Page<HookDelivery>>(`${base}/webhook-deliveries`,{params:{page,subscription_id,state,limit:30}})).data},
 async deliveryAction(id:string,action:'retry'|'cancel'){await client.post(`${base}/webhook-deliveries/${encodeURIComponent(id)}/action`,{action,confirm:action==='cancel'})},
}
