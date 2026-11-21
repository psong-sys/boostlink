T=[0,0,0,0]
UPC=dict(font='title',grad=[[255,236,140],[235,95,30]],outline=[45,20,10],ow=3,shadow=[3,3],fill=T)
DNC=dict(font='title',grad=[[215,230,255],[95,115,225]],outline=[15,25,70],ow=3,shadow=[3,3],fill=T)
GRC=dict(font='title',grad=[[235,255,140],[100,200,30]],outline=[20,45,10],ow=3,shadow=[3,3],fill=T)
def full(w,h,text,st,pad=6,size=None): return [dict(box=[0,0,w,h],tbox=[pad,pad,w-pad,h-pad],text=text,size=size or h,**st)]
SPEC={
141:full(96,36,'PROMO\nNEAR',dict(font='boldi',color=[255,255,255],outline=[0,120,230],ow=2,fill=T,spacing=0),pad=2),
142:full(96,36,'DEMOTE\nRISK',dict(font='boldi',color=[255,255,255],outline=[220,0,40],ow=2,fill=T,spacing=0),pad=2),
154:full(484,64,'PLAYER LEVEL UP!',UPC),
168:full(512,68,'PLAYER BADGE UP!',UPC),
170:full(512,68,'PLAYER BADGE DOWN...',DNC),
171:full(284,68,'CLASS UP!',UPC),
172:full(328,68,'GRADE UP!',UPC),
175:full(284,68,'CLASS DOWN...',DNC),
176:full(328,68,'GRADE DOWN...',DNC),
177:full(568,68,'GROUP TAG LEVEL UP!',GRC),
181:full(344,64,'NAVI BOND UP!',GRC),
197:full(344,64,'MS MASTERY UP!',GRC),
198:full(420,68,'RANDOM MASTERY UP!',GRC),
155:{'streak':154},169:{'streak':168},173:{'streak':171},
}
SPEC[146]=[dict(box=[30,14,182,41],text='LEVEL UP SOON!',font='bold',size=19,color=[225,250,150])]
SPEC[147]=[dict(box=[30,14,182,41],text='BADGE UP SOON!',font='bold',size=19,color=[255,235,160])]
