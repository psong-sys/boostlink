import sys,glob,os,re,struct;sys.path.insert(0,'.');sys.path.insert(0,'specs')
from fhm2 import FHM2,tex_list
from PIL import Image
from strings import S
SRC='/mnt/user-data/uploads/exv2/data/x64/dplcache_release'
os.makedirs('build',exist_ok=True)
def patch_strings(raw,d):
    n=0
    for jp,en in d.items():
        jb=jp.encode(); eb=en.encode(); assert len(eb)<=len(jb)
        pat=struct.pack('<I',len(jb))+jb+b'\0'
        for m in list(re.finditer(re.escape(pat),bytes(raw))):
            # keep the length field: the table is read sequentially, so pad with spaces to the same byte length
            p=m.start(); raw[p+4:p+4+len(jb)]=eb+b' '*(len(jb)-len(eb)); n+=1
    return n
for F in sys.argv[1:]:
    a=FHM2(open(f'{SRC}/{F}.fhm2d','rb').read()); raw=bytearray(a.raw); ts=tex_list(bytes(raw)); n=0
    for p in glob.glob(f'out/{F}_*.png'):
        i=int(re.search(r'_(\d+)\.png$',p).group(1)); t=ts[i]
        im=Image.open(p).convert('RGBA'); assert im.size==(t['w'],t['h'])
        raw[t['off']:t['off']+t['size']]=im.tobytes(); n+=1
    ns=patch_strings(raw,S.get(F,{}))
    p=0
    for b in a.blocks: L=len(b[3]); b[3]=bytes(raw[p:p+L]); p+=L
    out=a.build(); b2=FHM2(out); assert b2.raw==bytes(raw)
    open(f'build/{F}.fhm2d','wb').write(out); print(F,n,'textures',ns,'strings',len(out))
