import struct, sys, json
sys.path.insert(0, '/tmp/claude-0/-home-user/c2208f31-42f0-573b-b6cf-14a0fcad2a81/scratchpad')
from ntxl import decrypt, encrypt
from ntxlpack import load, repack
from ntxlparse import parse
from msgtool import x25, a4
def rebuild(b, tr):
    n = struct.unpack_from('<I', b, 0x30)[0]; P = struct.unpack_from('<I', b, 0x1c)[0]
    recs = []; end = 0
    for i in range(n - 1):
        _, x, to, ko, _ = struct.unpack_from('<HHIII', b, 0x40 + 16 * i)
        if to == 0xffffffff: continue
        h, c = struct.unpack_from('<HH', b, P + to)
        k = b[P + ko:b.index(b'\0', P + ko)].decode()
        t = b[P + to + 4:P + to + 4 + 2 * c].decode('utf-16le')
        recs.append((i, k, t)); end = max(end, a4(P + to + 4 + 2 * c + 2), a4(P + ko + len(k) + 1))
    old_field = struct.unpack_from('<I', b, P)[0]
    body = bytearray(b[:end]); newoff = {}
    for i, k, t in recs:
        en = tr.get(k)
        if en is None or en == t: continue
        if en not in newoff:
            off = len(body) - P; u = en.encode('utf-16le')
            body += struct.pack('<HH', x25(u), len(en)) + u + b'\0\0'
            body += b'\0' * (a4(len(body)) - len(body)); newoff[en] = off
        struct.pack_into('<I', body, 0x40 + 16 * i + 4, newoff[en])
    struct.pack_into('<I', body, P, old_field + (len(body) - end))
    body += b'\xff' * ((-len(body)) % 16) + b'\xff' * 16
    struct.pack_into('<I', body, 8, len(body))
    return bytes(body)
def apply(d, tr, lang=1):
    toc, base, ents = load(d)
    e = [x for x in ents if decrypt(x['data'])[6] == lang][0]
    nt = rebuild(decrypt(e['data']), tr)
    nd = repack(d, e['id'], encrypt(nt))
    _, _, ents2 = load(nd)
    e2 = [x for x in ents2 if x['id'] == e['id']][0]
    got = parse(decrypt(e2['data']))
    ok = sum(1 for k, v in tr.items() if got.get(k) == v)
    for x, y in zip(ents, ents2):
        if x['id'] != e['id']: assert x['data'] == y['data']
    return nd, ok
if __name__ == '__main__':
    src, out = sys.argv[1:3]
    d = open(src, 'rb').read()
    toc, base, ents = load(d)
    assert apply(d, {})[0] == d
    if len(sys.argv) > 3:
        tr = json.load(open(sys.argv[3], encoding='utf-8'))
    else:   # official English entry (lang 2)
        en = [x for x in ents if decrypt(x['data'])[6] == 2][0]
        tr = parse(decrypt(en['data']))
    nd, ok = apply(d, tr)
    print('applied', ok, 'of', len(tr), len(d), '->', len(nd))
    open(out, 'wb').write(nd)
