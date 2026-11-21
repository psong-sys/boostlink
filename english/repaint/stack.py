import sys
from PIL import Image,ImageDraw
out=sys.argv[1]; s=float(sys.argv[2]); fs=sys.argv[3:]
ims=[Image.open(f).convert('RGBA') for f in fs]
W=max(i.width for i in ims); H=sum(i.height+14 for i in ims)
S=Image.new('RGBA',(W,H),(255,0,255,255)); d=ImageDraw.Draw(S); y=0
for f,i in zip(fs,ims):
    d.text((0,y),f,fill=(0,0,0,255)); y+=12; S.alpha_composite(i,(0,y))
    for gx in range(0,i.width,10): d.line([(gx,y),(gx,y+i.height)],fill=(0,255,255,60) if gx%50 else (255,255,0,160))
    for gy in range(0,i.height,10): d.line([(0,y+gy),(i.width,y+gy)],fill=(0,255,255,60) if gy%50 else (255,255,0,160))
    y+=i.height+2
S=S.resize((int(W*s),int(H*s)),Image.NEAREST); S.convert('RGB').save(out)
