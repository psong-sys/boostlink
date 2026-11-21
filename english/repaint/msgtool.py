"""Rebuild the EXVS2 message table (0x1212B83E.fhm2d entry 0, encrypted NTXL).
English text is appended to the string pool; records are repointed. Keys untouched."""
import struct, zlib, sys, json
from ntxl import decrypt, encrypt
P = 0xbe20
NREC = 2023

def x25(d):
    c = 0xffff
    for x in d:
        c ^= x
        for _ in range(8):
            c = (c >> 1) ^ 0x8408 if c & 1 else c >> 1
    return c ^ 0xffff

def a4(n): return (n + 3) & ~3

def parse_ntxl(b):
    recs = []
    for i in range(NREC):
        _, x, to, ko, _ = struct.unpack_from('<HHIII', b, 0x40 + 16 * i)
        h, n = struct.unpack_from('<HH', b, P + to)
        t = b[P + to + 4:P + to + 4 + 2 * n].decode('utf-16le')
        k = b[P + ko:b.index(b'\0', P + ko)].decode()
        recs.append(dict(i=i, key=k, text=t, to=to, ko=ko, n=n))
    return recs

def content_end(b, recs):
    e = 0
    for r in recs:
        e = max(e, a4(r['to'] + 4 + 2 * r['n'] + 2), a4(r['ko'] + len(r['key']) + 1))
    return e

def rebuild_ntxl(b, tr):
    """b: decrypted ntxl; tr: {key: english}"""
    recs = parse_ntxl(b)
    end = content_end(b, recs)
    old_field = struct.unpack_from('<I', b, P)[0]
    body = bytearray(b[:P + end])
    newoff = {}
    for r in recs:
        en = tr.get(r['key'])
        if en is None or en == r['text']:
            continue
        if en not in newoff:
            off = len(body) - P
            u = en.encode('utf-16le')
            body += struct.pack('<HH', x25(u), len(en)) + u + b'\0\0'
            body += b'\0' * (a4(len(body)) - len(body))
            newoff[en] = off
        struct.pack_into('<I', body, 0x40 + 16 * r['i'] + 4, newoff[en])
    nend = len(body) - P
    struct.pack_into('<I', body, P, old_field + (nend - end))
    body += b'\xff' * ((-len(body)) % 16) + b'\xff' * 16
    struct.pack_into('<I', body, 8, len(body))
    return bytes(body)

# ---- fhm2d container (0x1212B83E special TOC) ----
def unpack_entry0(d):
    s, tr_, tc = struct.unpack_from('<QQQ', d, 0x10)
    toc = zlib.decompress(d[0x30:0x30 + tc], -15)
    raw, = struct.unpack_from('<Q', toc, 136)
    c0, = struct.unpack_from('<Q', toc, 173)
    base = 0x30 + tc
    blk = zlib.decompress(d[base:base + c0], -15)
    tail = d[base + c0:base + c0 + raw - 0x10000]
    return toc, blk + tail, base

def repack(d, newraw):
    toc, oldraw, base = unpack_entry0(d)
    toc = bytearray(toc)
    oraw, = struct.unpack_from('<Q', toc, 136)
    oc0, = struct.unpack_from('<Q', toc, 173)
    obody = oc0 + oraw - 0x10000
    assert len(newraw) % 16 == 0 and 0x10000 < len(newraw) <= 0x80000
    c = zlib.compressobj(9, zlib.DEFLATED, -15)
    nb0 = c.compress(newraw[:0x10000]) + c.flush()
    nbody = nb0 + newraw[0x10000:]
    dv = len(newraw) - oraw          # virtual stream delta
    db = len(nbody) - obody          # body delta
    struct.pack_into('<Q', toc, 136, len(newraw))
    struct.pack_into('<Q', toc, 173, len(nb0))
    for o in (52, 84, 96, 108, 120):
        v, = struct.unpack_from('<I', toc, o); struct.pack_into('<I', toc, o, v + dv)
    for rp in (189, 242, 303, 356):
        v, = struct.unpack_from('<Q', toc, rp + 24); struct.pack_into('<Q', toc, rp + 24, v + db)
    c = zlib.compressobj(9, zlib.DEFLATED, -15)
    ntoc = c.compress(bytes(toc)) + c.flush()
    rest = d[base + obody:]
    hdr = bytearray(d[:0x30])
    out = bytes(hdr) + ntoc + nbody + rest
    hdr = bytearray(out[:0x30])
    struct.pack_into('<QQQ', hdr, 0x10, len(out), len(toc), len(ntoc))
    return bytes(hdr) + out[0x30:]

def check(d):
    """re-parse all 5 entries and verify sizes add up"""
    s, tr_, tc = struct.unpack_from('<QQQ', d, 0x10)
    assert s == len(d)
    toc = zlib.decompress(d[0x30:0x30 + tc], -15)
    base = 0x30 + tc; ends = []
    for rp in (136, 189, 242, 303, 356):
        raw, _, _, nb, off = struct.unpack_from('<QQIIQ', toc, rp)
        mask = toc[rp + 36]
        sizes = list(struct.unpack_from('<%dQ' % nb, toc, rp + 37))
        p = base + off; data = b''; j = 0
        while len(data) < raw:
            if mask >> j & 1:
                sz = sizes.pop(0); blk = zlib.decompress(d[p:p + sz], -15)
                assert len(blk) == min(0x10000, raw - len(data))
            else:
                sz = min(0x10000, raw - len(data)); blk = d[p:p + sz]
            data += blk; p += sz; j += 1
        assert len(data) == raw and not sizes, (rp, len(data), raw)
        tl = 0
        ends.append(p + tl)
    assert ends[-1] == len(d), (ends, len(d))
    return toc

if __name__ == '__main__':
    src, trjson, out = sys.argv[1:4]
    d = open(src, 'rb').read()
    check(d)
    _, raw, _ = unpack_entry0(d)
    assert encrypt(decrypt(raw)) == raw
    tr = json.load(open(trjson, encoding='utf-8'))
    nt = rebuild_ntxl(decrypt(raw), tr)
    nd = repack(d, encrypt(nt))
    check(nd)
    _, raw2, _ = unpack_entry0(nd)
    recs = parse_ntxl(decrypt(raw2))
    got = sum(1 for r in recs if r['key'] in tr and r['text'] == tr[r['key']])
    print('records translated', got, 'of', len(tr), 'size', len(d), '->', len(nd))
    open(out, 'wb').write(nd)
