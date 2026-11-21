T=[0,0,0,0]; W=[255,255,255]; BLK=[0,0,0]; Y=[255,255,0]; RED=[255,0,0]
def full(w,h,text,pad=1,**k):
    k.setdefault('fill',T); k.setdefault('font','bold'); k.setdefault('size',h)
    return dict(box=[0,0,w,h],tbox=[pad,pad,w-pad,h-pad],text=text,**k)
WS=dict(color=W,shadow=[2,2],shadowcolor=[0,0,0])
SPEC={
11:[full(108,24,'Cancel',color=BLK)],
15:[full(44,20,'Yes',color=BLK)],
16:[full(68,20,'No',color=BLK)],
42:[full(44,20,'Back',color=BLK)],
45:[full(198,22,'Back to Title',color=BLK)],
46:[full(90,22,'Continue',color=BLK)],
56:[full(366,40,'Checking player...',pad=3,**WS)],
62:[full(184,32,'Settings',pad=3,**WS)],
88:[full(42,26,'Time',pad=2,color=W)],
106:[full(104,16,'Player Navi',color=W)],
107:[full(76,14,'Battle Navi',color=W)],
149:[full(376,34,'Use this game data?',pad=3,**WS)],
163:[full(262,40,'Access it here.',pad=3,align='l',**WS)],
55:[dict(box=[0,0,624,66],fill=T,tbox=[2,1,622,32],text='Touch the same Banapassport card you were playing with.',font='bold',size=28,**WS),
    dict(box=[0,0,1,1],fill='none',tbox=[40,34,584,62],text='[Caution] The game is over when the count reaches zero.',font='bold',size=20,color=RED,outline=[119,0,0],ow=1)],
150:[dict(box=[0,0,484,96],fill=T,tbox=[40,4,444,44],text='URL sent',font='bold',size=36,**WS),
     dict(box=[0,0,1,1],fill='none',tbox=[2,54,482,92],text='The last 20 digits of the URL are your access code.',font='bold',size=26,color=Y,shadow=[2,2])],
152:[full(630,32,'Touch your Banapassport card or mobile phone.',pad=2,color=Y,shadow=[2,2])],
151:[dict(box=[12,6,312,44],fill='h',tbox=[16,8,330,42],text='Register Game Data',font='bold',size=30,align='l',color=W)],
158:[dict(box=[10,5,432,46],fill='h',tbox=[14,8,430,42],text='Register on Banapassport Site',font='bold',size=30,align='l',color=W)],
}
B=dict(font='bold',**WS)
RD=dict(font='bold',color=RED,outline=[100,0,0],ow=1)
SPEC[64]=[
 dict(box=[60,80,560,128],fill=T,text='New IC card / mobile phone',size=34,**B),
 dict(box=[222,224,576,268],fill=T,tbox=[222,226,574,266],text='Create game data for this game?',size=26,align='l',**B),
 dict(box=[90,352,480,410],fill=T,tbox=[60,356,516,406],text='[Caution] This card/phone can no longer be used\nas a destination for transferred game data.',size=19,spacing=3,**RD),
]
SPEC[159]=[
 dict(box=[0,0,646,216],fill=T,tbox=[200,1,480,30],text='(1) Touch your mobile phone!',size=26,**B),
 dict(box=[0,56,646,96],fill=T,tbox=[2,60,644,92],text='(2) Register on the Banapassport site with the access code.',size=26,**B),
 dict(box=[0,110,646,168],fill=T,tbox=[2,112,644,166],text='Registering gives you services such as data transfer if you lose\nyour Banapassport card. Registration is free.',size=21,spacing=4,align='l',**B),
 dict(box=[0,170,646,206],fill=T,tbox=[2,174,644,202],text='[Caution] A phone already registered to a game cannot be a data transfer target.',size=19,**RD),
]
SPEC[160]=[
 dict(box=[16,4,330,48],fill='h',tbox=[20,8,400,44],text='Register Game Data',size=34,align='l',**B),
 dict(box=[16,60,500,134],fill='h',tbox=[20,64,496,130],text='Register game data using\na new card or mobile phone.',size=28,spacing=6,align='l',**B),
 dict(box=[16,138,500,168],fill='h',tbox=[20,140,496,166],text='[Caution] Cannot be used for data transfer after registering.',size=18,align='l',**RD),
 dict(box=[16,224,440,268],fill='h',tbox=[20,228,440,264],text='Register on Banapassport Site',size=34,align='l',**B),
 dict(box=[16,278,500,378],fill='h',tbox=[20,282,496,374],text='Issues an "access code" to your phone\nand sends the "URL" for registering\non the Banapassport site.',size=26,spacing=5,align='l',**B),
]
