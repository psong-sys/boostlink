"""Generic repack of 'special TOC' NTXL fhm2d containers (0x1212B83E, 0x2FBC5CED, ...)."""
import struct, zlib
def records(toc):
    cnt, = struct.unpack_from('<I', toc, 64)
    p = 84 + 12 * (cnt - 1) + 4; out = []
    for _ in range(cnt):
        raw, z, one, nb, off = struct.unpack_from('<QQIIQ', toc, p)
        eid, = struct.unpack_from('<I', toc, p + 32); mask = toc[p + 36]
        sizes = list(struct.unpack_from('<%dQ' % nb, toc, p + 37))
        out.append(dict(p=p, raw=raw, nb=nb, off=off, id=eid, mask=mask, sizes=sizes))
        p += 37 + 8 * nb + 8
    return out
def load(d):
    s, tr, tc = struct.unpack_from('<QQQ', d, 0x10)
    toc = zlib.decompress(d[0x30:0x30 + tc], -15); base = 0x30 + tc
    ents = []
    for r in records(toc):
        p = base + r['off']; data = b''; j = 0; sz = list(r['sizes'])
        while len(data) < r['raw']:
            if r['mask'] >> j & 1:
                n = sz.pop(0); blk = zlib.decompress(d[p:p + n], -15)
            else:
                n = min(0x10000, r['raw'] - len(data)); blk = d[p:p + n]
            assert len(blk) == min(0x10000, r['raw'] - len(data))
            data += blk; p += n; j += 1
        assert not sz
        r['data'] = data; r['blen'] = p - (base + r['off']); ents.append(r)
    return toc, base, ents
def body(data, mask):
    out = b''; sizes = []; j = 0
    for i in range(0, len(data), 0x10000):
        blk = data[i:i + 0x10000]
        if mask >> j & 1:
            c = zlib.compressobj(9, zlib.DEFLATED, -15); b = c.compress(blk) + c.flush(); sizes.append(len(b))
        else: b = blk
        out += b; j += 1
    return out, sizes
def repack(d, eid, newraw):
    toc, base, ents = load(d); toc = bytearray(toc)
    e = [x for x in ents if x['id'] == eid][0]
    assert len(newraw) % 16 == 0 and len(newraw) > 0x10000 * (e['mask'].bit_length() - 1)
    nb_, sizes = body(newraw, e['mask']); assert len(sizes) == e['nb']
    dv = len(newraw) - e['raw']; db = len(nb_) - e['blen']
    cnt, = struct.unpack_from('<I', toc, 64)
    myv = 0 if eid == 0 else struct.unpack_from('<I', toc, 84 + 12 * (eid - 1))[0]
    for k in range(cnt - 1):
        o = 84 + 12 * k; v, = struct.unpack_from('<I', toc, o)
        if v > myv: struct.pack_into('<I', toc, o, v + dv)
    v, = struct.unpack_from('<I', toc, 52); struct.pack_into('<I', toc, 52, v + dv)
    struct.pack_into('<Q', toc, e['p'], len(newraw))
    struct.pack_into('<%dQ' % e['nb'], toc, e['p'] + 37, *sizes)
    for x in ents:
        if x['off'] > e['off']: struct.pack_into('<Q', toc, x['p'] + 24, x['off'] + db)
    c = zlib.compressobj(9, zlib.DEFLATED, -15); ntoc = c.compress(bytes(toc)) + c.flush()
    bd = d[base:]
    nbody = bd[:e['off']] + nb_ + bd[e['off'] + e['blen']:]
    hdr = bytearray(d[:0x30])
    struct.pack_into('<QQQ', hdr, 0x10, 0x30 + len(ntoc) + len(nbody), len(toc), len(ntoc))
    return bytes(hdr) + ntoc + nbody
