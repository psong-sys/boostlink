import json,sys,re
src=json.load(open(sys.argv[1],encoding='utf-8')); out=json.load(open(sys.argv[2],encoding='utf-8'))
ALLOW=set('※○●★☆←→↑↓【】「」·…×')
code=re.compile(r'\x10(\[[^\]]*\]|c[0-9A-Fa-f]{6}|[A-Za-z][0-9A-Za-z]?)')
fmt=re.compile(r'%[-0-9.]*z?[dsfuxXc]')
bad=0
for s in src:
    k=s['key']; ja=s['ja']
    if k not in out: print('MISSING',k); bad+=1; continue
    en=out[k]
    if not isinstance(en,str): print('NOTSTR',k); bad+=1; continue
    if sorted(code.findall(ja))!=sorted(code.findall(en)): print('CODES',k,code.findall(ja),code.findall(en)); bad+=1
    if sorted(fmt.findall(ja))!=sorted(fmt.findall(en)): print('FMT',k); bad+=1
    odd=[c for c in en if not (32<=ord(c)<127 or c in '\n\x10' or c in ALLOW)]
    if odd: print('CHARS',k,''.join(sorted(set(odd)))); bad+=1
    if en.count('\n')>ja.count('\n')+1: print('LINES',k); bad+=1
    jl=ja.split('\n'); el=en.split('\n')
    mx=max(len(l) for l in jl)
    for l in el:
        if len(code.sub('',l))>max(2*mx+4,12): print('LONG',k,repr(l)); bad+=1; break
extra=set(out)-{s['key'] for s in src}
if extra: print('EXTRA keys',len(extra))
print('OK' if not bad else f'{bad} problems')
