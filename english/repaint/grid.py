import sys
from PIL import Image,ImageDraw
im=Image.open(sys.argv[1]).convert('RGBA'); s=float(sys.argv[3]) if len(sys.argv)>3 else 1
bg=Image.new('RGBA',im.size,(255,0,255,255)); bg.alpha_composite(im); im=bg
if len(sys.argv)>4:
    x0,y0,x1,y1=map(int,sys.argv[4].split(',')); im=im.crop((x0,y0,x1,y1))
else: x0=y0=0
im=im.resize((int(im.width*s),int(im.height*s)),Image.NEAREST); d=ImageDraw.Draw(im)
step=int(sys.argv[5]) if len(sys.argv)>5 else 20
for gx in range((x0//step)*step,x0+int(im.width/s)+1,step):
    X=(gx-x0)*s; d.line([(X,0),(X,im.height)],fill=(0,255,255,90) if gx%100 else (255,255,0,200))
    if gx%100==0: d.text((X+2,2),str(gx),fill=(255,255,0,255))
for gy in range((y0//step)*step,y0+int(im.height/s)+1,step):
    Y=(gy-y0)*s; d.line([(0,Y),(im.width,Y)],fill=(0,255,255,90) if gy%100 else (255,255,0,200))
    if gy%100==0: d.text((2,Y+2),str(gy),fill=(255,255,0,255))
im.convert('RGB').save(sys.argv[2])
