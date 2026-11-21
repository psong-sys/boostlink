W=[255,255,255]
BTN=dict(font='titleu',color=W,outline=[25,25,35],ow=3,shadow=[3,3])
DESC=dict(font='semi',color=W,align='l',size=19,spacing=3,fill='h')
CRED=dict(font='bold',color=W,outline=[150,10,30],ow=2,align='l',size=17)
SPEC={
14:[
 dict(box=[28,24,864,96],text='CPU BATTLE / TRAINING',size=62,**BTN),
 dict(box=[130,146,538,212],text='VS. ROUTE BATTLE',size=50,**{**BTN,'font':'title','color':[225,228,240]}),
 dict(box=[566,140,852,216],text='Choose a route and conquer\nthe map in CPU battles.\n(Single player only)',**DESC),
 dict(box=[78,236,520,262],text='Credit : Ends on a stage clear or a loss.',**CRED),
 dict(box=[78,264,665,291],text='Credits : Ends on a stage clear or 3 losses.    *Rewards even on a loss',**CRED),
 dict(box=[176,334,538,402],text='PRACTICE',size=54,**BTN),
 dict(box=[566,336,852,396],text='Practice with mock\nbattles against the CPU.',**DESC),
 dict(box=[78,424,470,450],text='Credit : Ends after 6 scenes or a loss.',**CRED),
 dict(box=[78,452,470,479],text='Credits : Ends after 6 scenes or 3 losses.',**CRED),
 dict(box=[176,522,538,592],text='TRAINING',size=54,**BTN),
 dict(box=[566,536,852,566],text='Training-only mode.',**DESC),
 dict(box=[78,611,380,637],text='Credit : 5 minutes of play',**CRED),
 dict(box=[78,639,380,666],text='Credits : 12 minutes of play',**CRED),
],
}
FOOT=dict(font='bold',color=W,outline=[120,10,30],ow=2,align='l',size=17)
ICON=dict(font='bold',color=W,size=12,fill='h')
SPEC[25]=[
 dict(box=[26,27,98,43],text='ONLINE',**ICON),
 dict(box=[120,27,192,43],text='ONLINE',**ICON),
 dict(box=[236,24,872,98],text='ONLINE BATTLE MODE',size=60,**BTN),
 dict(box=[195,128,700,178],text='Get matched online for 2-on-2 battles.\nSolo sortie for 1 player, team sortie for 2.',font='bold',color=W,size=18,spacing=4),
 dict(box=[192,182,712,206],tbox=[195,183,705,202],text='*Except Store Match, a card registered with VS. Mobile is required.',font='bold',color=[235,25,25],size=15),
 dict(box=[12,195,180,227],text='Many match types!',font='boldi',color=[255,205,40],outline=[90,40,0],ow=2,size=18,fill='h',rotate=6),
 dict(box=[100,238,460,302],text='CLASS MATCH G',size=48,**{**BTN,'font':'title','color':[255,225,120],'outline':[60,30,0]}),
 dict(box=[488,240,850,292],text='Matches you with players of a\nsimilar grade in the same class.',**DESC),
 dict(box=[100,334,460,398],text='MIX MATCH',size=50,**BTN),
 dict(box=[488,344,850,376],text='Matches with no conditions.',**DESC),
 dict(box=[100,428,460,494],text='CUSTOM MATCH',size=50,**BTN),
 dict(box=[488,430,850,480],text='Matches in a room registered\nvia the Live Monitor or mobile.',**DESC),
 dict(box=[100,522,460,588],text='STORE MATCH',size=50,**BTN),
 dict(box=[488,534,850,564],text='Matches with players in this store.',**DESC),
 dict(box=[48,627,420,654],text='Credit : Up to 10 wins in a row, ends on a loss.',**FOOT),
 dict(box=[48,655,720,682],text='Credits : 3 battles guaranteed, extras for wins.    *A CPU loss counts as 1 battle',**FOOT),
]
SPEC[26]=[
 dict(box=[26,27,98,43],text='STORE',**ICON),
 dict(box=[120,27,192,43],text='STORE',**ICON),
 dict(box=[236,24,872,98],text='IN-STORE BATTLE MODE',size=60,**BTN),
 dict(box=[150,318,760,348],text='Get matched in this store for 2-on-2 battles.',font='bold',color=W,size=21),
 dict(box=[130,376,770,408],text='Solo sortie for 1 player, team sortie for 2 players.',font='bold',color=W,size=21),
 dict(box=[48,627,420,654],text='Credit : Up to 10 wins in a row, ends on a loss.',**FOOT),
 dict(box=[48,655,720,682],text='Credits : 3 battles guaranteed, extras for wins.    *A CPU loss counts as 1 battle',**FOOT),
]
T=[0,0,0,0]
SM=dict(font='bold',color=W,fill=T,shadow=[1,1],outline=[40,40,40],ow=1,align='l')
SPEC[1]=[dict(box=[178,0,756,40],text='GAME MODES AVAILABLE NOW',font='titleu',color=W,fill=T,size=34,shadow=[2,2],align='l')]
SPEC[2]=[
 dict(box=[47,0,165,27],text='Play fee',size=17,**SM),
 dict(box=[117,38,246,67],text='CREDIT /',size=18,**SM),
 dict(box=[277,38,406,67],text='CREDITS /',size=18,**SM),
 dict(box=[433,38,545,66],text='CREDITS',size=18,**SM),
 dict(box=[402,63,560,88],text='(30 min unlimited play)',size=15,**{**SM,'align':'c'}),
]
SPEC[13]=[
 dict(box=[47,0,165,27],text='Play fee',size=17,**SM),
 dict(box=[117,38,246,67],text='CREDIT /',size=18,**SM),
 dict(box=[277,38,406,67],text='CREDITS',size=18,**SM),
]
