import sys,importlib.util;sys.path.insert(0,'.')
from PIL import Image,ImageFilter
import repaint
F=sys.argv[1]; only=[int(x) for x in sys.argv[2:]]
spec=importlib.util.spec_from_file_location('s',f'specs/{F}.py'); m=importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
def streak(src,size):
    s=Image.open(f'out/{F}_{src}.png').convert('RGBA'); bb=s.getbbox(); s=s.crop(bb)
    W,H=size; s=s.resize((int(W*0.8),min(H-8,s.height)),Image.LANCZOS)
    small=s.resize((max(1,s.width//12),s.height),Image.BILINEAR).resize(s.size,Image.BILINEAR)
    small=small.filter(ImageFilter.GaussianBlur(2))
    a=small.split()[3].point(lambda v:int(v*0.8)); small.putalpha(a)
    o=Image.new('RGBA',size,(0,0,0,0)); o.alpha_composite(small,((W-s.width)//2,(H-s.height)//2)); return o
items=sorted(m.SPEC.items(),key=lambda kv: isinstance(kv[1],dict))
for i,ops in items:
    if only and i not in only: continue
    im=Image.open(f'full/{F}_{i}.png')
    o=streak(ops['streak'],im.size) if isinstance(ops,dict) else repaint.apply(im,ops)
    o.save(f'out/{F}_{i}.png')
    bg=Image.new('RGBA',o.size,(40,60,70,255)); bg.alpha_composite(o); bg.convert('RGB').save(f'out/{F}_{i}_v.jpg',quality=90)
