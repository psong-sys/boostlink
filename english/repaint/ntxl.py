import struct,zlib
def decrypt(b):
    b=bytearray(b)
    assert b[:4]==bytes.fromhex('a3feadf6')
    b[0]=(b[0]-0x55)&255; b[1]=(b[1]+0x56)&255; b[2]=(b[2]+0xab)&255; b[3]=(b[3]+0x56)&255
    for i in range(4,16): b[i]^=0xff
    size=struct.unpack_from('<I',b,8)[0]; ecx=size
    for i in range(16,size):
        c=b[i]; r8=(ecx*0x15a4e35+1)&0xffffffff
        b[i]=(c-(r8>>16))&255; ecx=(r8+c)&0xffff
    return bytes(b)
def encrypt(p):
    p=bytearray(p); size=struct.unpack_from('<I',p,8)[0]; ecx=size
    for i in range(16,size):
        r8=(ecx*0x15a4e35+1)&0xffffffff; c=(p[i]+(r8>>16))&255; p[i]=c; ecx=(r8+c)&0xffff
    for i in range(4,16): p[i]^=0xff
    p[0]=(p[0]+0x55)&255; p[1]=(p[1]-0x56)&255; p[2]=(p[2]-0xab)&255; p[3]=(p[3]-0x56)&255
    return bytes(p)
