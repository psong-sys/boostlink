import sys;sys.path.insert(0,'.')
from fhm2 import *
F=sys.argv[1]; idx=[int(x) for x in sys.argv[2:]]
a=FHM2(open(f'/mnt/user-data/uploads/exv2/data/x64/dplcache_release/{F}.fhm2d','rb').read())
ts=tex_list(a.raw)
for i,t in enumerate(ts):
  if not idx or i in idx:
    to_image(a.raw,t).save(f'full/{F}_{i}.png'); print(i,t['name'],t['w'],t['h'])
