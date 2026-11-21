import struct
def parse(p, full=False):
    n=struct.unpack_from('<I',p,0x30)[0]; P=struct.unpack_from('<I',p,0x1c)[0]; out={}
    for i in range(n-1):
        _,x,to,ko,_=struct.unpack_from('<HHIII',p,0x40+16*i)
        if to==0xffffffff: continue
        h,c=struct.unpack_from('<HH',p,P+to); t=p[P+to+4:P+to+4+2*c].decode('utf-16le','replace')
        k=p[P+ko:p.index(b'\0',P+ko)].decode(); out[k]=t
    return out
