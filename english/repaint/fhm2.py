"""fhm2d (EXVS2 dplcache) unpack/repack.
Layout: 0x30 header (magic = ~'FHM2', u64 file size @0x10, u64 TOC raw/deflated size @0x18/@0x20),
raw-deflate TOC, then data blocks (64 KB raw each; raw-deflate or stored, per a bitmask in the TOC entry)."""
import zlib,struct,re
MAGIC=b'\xb9\xb7\xb2\xcd'
def _u32(b,p): return struct.unpack_from('<I',b,p)[0]
def _u64(b,p): return struct.unpack_from('<Q',b,p)[0]
def parse_entries(toc):
    """find data entries: rawsize,0,u32 1,u32 nblocks,u64 offset,u32 id,mask,u64 sizes"""
    ents=[]; p=0
    while p+41<=len(toc):
        raw=_u64(toc,p)
        if 0<raw<(1<<34) and _u64(toc,p+8)==0 and _u32(toc,p+16)==1:
            nb=_u32(toc,p+20); n=max(nb,1)
            if n==(raw+0xffff)//0x10000 or (nb==0 and raw<=0x10000):
                off=_u64(toc,p+24); mlen=(nb+7)//8; mp=p+36
                mask=int.from_bytes(toc[mp:mp+mlen],'little'); sp=mp+mlen
                if sp+8*n<=len(toc) and mask < (1<<n):
                    sz=[_u64(toc,sp+8*k) for k in range(n)]
                    ok=all((s>0 if mask>>k&1 else s==0) for k,s in enumerate(sz))
                    if ok:
                        ents.append(dict(pos=p,raw=raw,off=off,n=n,mask=mask,sizes=sz,szpos=sp)); p=sp+8*n; continue
        p+=1
    return ents
class FHM2:
    def __init__(self,d):
        assert d[:4]==MAGIC,'bad magic'
        self.hdr=bytearray(d[:0x30]); fs,tr,tc=struct.unpack_from('<QQQ',self.hdr,0x10)
        assert fs==len(d),'size mismatch'
        o=zlib.decompressobj(-15); self.toc=bytearray(o.decompress(d[0x30:0x30+tc])); assert len(self.toc)==tr
        self.ents=parse_entries(self.toc)
        pos=0x30+tc; self.blocks=[]   # list of (entry_idx, block_idx, compressed?, rawbytes)
        for ei,e in enumerate(self.ents):
            left=e['raw']
            for k in range(e['n']):
                rl=min(0x10000,left); left-=rl
                if e['mask']>>k&1:
                    cs=e['sizes'][k]; b=zlib.decompressobj(-15).decompress(d[pos:pos+cs]); pos+=cs
                else:
                    b=d[pos:pos+rl]; pos+=rl
                assert len(b)==rl,(ei,k,len(b),rl)
                self.blocks.append([ei,k,bool(e['mask']>>k&1),b])
        assert pos==len(d),('trailing',pos,len(d))
    def entry_raw(self,ei): return b''.join(b[3] for b in self.blocks if b[0]==ei)
    def set_entry_raw(self,ei,raw):
        assert len(raw)==self.ents[ei]['raw']; p=0
        for b in self.blocks:
            if b[0]==ei: b[3]=raw[p:p+len(b[3])]; p+=len(b[3])
    @property
    def raw(self): return b''.join(b[3] for b in self.blocks)
    def build(self,level=9):
        toc=bytearray(self.toc); body=[]
        for ei,k,comp,b in self.blocks:
            if comp:
                c=zlib.compressobj(level,zlib.DEFLATED,-15); cb=c.compress(b)+c.flush()
                struct.pack_into('<Q',toc,self.ents[ei]['szpos']+8*k,len(cb)); body.append(cb)
            else: body.append(b)
        c=zlib.compressobj(level,zlib.DEFLATED,-15); tc=c.compress(bytes(toc))+c.flush()
        out=b''.join([tc]+body); hdr=bytearray(self.hdr)
        struct.pack_into('<QQQ',hdr,0x10,0x30+len(out),len(toc),len(tc))
        return bytes(hdr)+out
def tex_list(raw):
    out=[]
    for m in re.finditer(b'46XT',raw):
        i=m.start(); nm=raw[i+4:i+0x44].split(b'\0')[0].decode('latin1')
        if i+0x68>len(raw): continue
        w,h=struct.unpack_from('<II',raw,i+0x44); psz=_u32(raw,i+0x64)
        if psz!=w*h*4 or i-0x40-psz<0: continue
        out.append(dict(name=nm,w=w,h=h,off=i-0x40-psz,size=psz))
    return out
def to_image(raw,t):
    from PIL import Image
    return Image.frombytes('RGBA',(t['w'],t['h']),raw[t['off']:t['off']+t['size']])
