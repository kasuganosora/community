import { expect,it,vi } from 'vitest'
const post=vi.hoisted(()=>vi.fn().mockResolvedValue({data:{id:'1'}}))
vi.mock('@/shared/api/client',()=>({default:{post}}))
import { communityAssetsApi } from '../communityAssetsApi'
it('overrides the JSON client default when uploading real image bytes',async()=>{const data=new FormData();data.append('file',new File(['png'],'frame.png',{type:'image/png'}));await communityAssetsApi.create('avatar_decorations',data);expect(post).toHaveBeenCalledWith('/api/v1/admin/avatar_decorations',data,{headers:{'Content-Type':'multipart/form-data'}})})
