import sys,glob,os,hashlib
SRC=os.environ.get('EXVS_DATA','data/x64/dplcache_release')  # the game's dplcache_release folder
sys.path.insert(0,'.')
from fhm2 import FHM2,tex_list,to_image
from PIL import Image,ImageDraw
def sheet(path,out,maxw=1600,scale=0.5):
    a=FHM2(open(path,'rb').read()); raw=a.raw
    ims=[];seen=set()
    for i,t in enumerate(tex_list(raw)):
        if t['w']<40 and t['h']<40: continue
        h=hashlib.md5(raw[t['off']:t['off']+t['size']]).hexdigest()
        if h in seen: continue
        seen.add(h)
        im=to_image(raw,t)
        if im.getextrema()[3][1]==0: continue
        s=scale if max(t['w'],t['h'])>300 else 0.75
        im=im.resize((max(1,int(t['w']*s)),max(1,int(t['h']*s))))
        ims.append((i,im))
    if not ims: return 0
    x=y=rowh=0; pos=[]
    for i,im in ims:
        if x+im.width>maxw: x=0;y+=rowh+18;rowh=0
        pos.append((x,y)); x+=im.width+8; rowh=max(rowh,im.height)
    H=y+rowh+18
    S=Image.new('RGBA',(maxw,H),(40,60,70,255)); d=ImageDraw.Draw(S)
    for (i,im),(x,y) in zip(ims,pos):
        S.alpha_composite(im,(x,y+14)); d.text((x,y),str(i),fill=(255,255,0,255))
    S.convert('RGB').save(out,quality=80); return len(ims)
for p in sorted(glob.glob(f'{SRC}/*.fhm2d')):
    n=os.path.basename(p)[:-6]
    if os.path.exists(f'sheets/{n}.jpg'): continue
    try: c=sheet(p,f'sheets/{n}.jpg')
    except Exception as e: c=repr(e)[:80]
    print(n,c)
