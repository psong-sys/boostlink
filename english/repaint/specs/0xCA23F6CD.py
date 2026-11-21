# mode-select logos and labels (GAME MODE SELECT screen)
from PIL import Image
CLR=[0,0,0,0]
def ink(i):
    return Image.open(f'full/0xCA23F6CD_{i}.png').split()[3].getbbox()
W=dict(font='titleu',color=[255,255,255],glow=[0,0,0],glowr=3,fill=CLR,squeeze=True)
def plain(i,text,pad=4,**kw):
    x0,y0,x1,y1=ink(i); im=Image.open(f'full/0xCA23F6CD_{i}.png')
    return [dict(box=[0,0,im.width,im.height],tbox=[max(0,x0-pad),y0,min(im.width,x1+pad),y1],text=text,**{**W,**kw})]
SPEC={}
for i,t in {2:'TIME RENTAL',31:'SOLO SORTIE',33:'TEAM SORTIE',127:'TEAM SORTIE',37:'MIX MATCH',38:'EXVS FES (TEMP)',
            40:'STORE MATCH',42:'CUSTOM MATCH',44:'PRACTICE',48:'TRAINING',49:'LOCAL',122:'TUTORIAL',
            61:'VERSUS',76:'VERSUS MODE',65:'CPU BATTLE / TRAINING MODE',133:'ONLINE TAG',138:'PARTNER WANTED!',
            141:'STORE TEAM',
            51:'Play with the same picks as last time',52:'Re-pick starting from the unit',53:'Re-pick everything from the start'}.items():
    SPEC[i]=plain(i,t,**({'font':'bold'} if i in (51,52,53) else {}))
for i,a,b in [(100,'SOLO SORTIE','(Local)'),(101,'SOLO SORTIE','(Online)'),(103,'TEAM SORTIE','(Local)'),
              (104,'TEAM SORTIE','(Online)'),(106,'SOLO SORTIE','(Event)'),(107,'TEAM SORTIE','(Event)'),
              (108,'TEAM HOLD','(Event)')]:
    SPEC[i]=plain(i,a+'  '+b)
SPEC[35]=plain(35,'CLASS MATCH G',pad=6,font='title',grad=[[255,244,200],[205,140,35]],outline=[70,25,95],ow=3,glow=[20,0,40])
SPEC[46]=plain(46,'VS. ROUTE BATTLE',pad=6,font='title',grad=[[255,255,255],[140,140,150]],outline=[120,0,10],ow=3,glow=[30,0,0])
# 79: keep the beginner leaf icon on the left
x0,y0,x1,y1=ink(79)
SPEC[79]=[dict(box=[44,0,420,56],tbox=[46,y0,x1,y1],text='Tutorial is playable too!',**{**W,'font':'bold'})]
# 154/156: "この台は ... です。" ("This cabinet is ... ."), white and red versions
for i,c in ((154,[255,255,255]),(156,[255,40,40])):
    SPEC[i]=[dict(box=[0,0,324,32],tbox=[0,2,97,30],text='This is',font='bold',color=c,fill=CLR,align='r'),
             dict(box=[263,0,324,32],tbox=[263,2,300,30],text='.',font='bold',color=c,fill=CLR,align='l')]
DUPS={30:[45],40:[110],37:[112],42:[114],48:[118],44:[120],46:[116],35:[83],31:[125],156:[157,158]}
