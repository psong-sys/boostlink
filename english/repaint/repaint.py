"""Repaint Japanese text in RGBA UI textures with English.

spec: list of ops. Each op is a dict:
  box   [x0,y0,x1,y1]   area to clear (exclusive x1,y1)
  fill  'h' (row-wise interpolation between left/right edges, default),
        'v' (column-wise between top/bottom), 'none', or [r,g,b,a]
  text  English text ('' = clear only). '\n' for lines.
  tbox  optional box for text placement (defaults to box)
  size  max font px; shrinks to fit
  font  key in FONTS
  color [r,g,b] ; outline [r,g,b] ; ow outline width ; shadow [dx,dy]
  align 'c' | 'l' | 'r' ; valign 'c' | 't'
  spacing line spacing px
"""
import os
from PIL import Image, ImageDraw, ImageFont, ImageFilter

HERE = os.path.dirname(os.path.abspath(__file__))
FD = os.path.join(HERE, 'fonts')
FONTS = {
    'title': (os.path.join(FD, 'Exo2-Italic.ttf'), b'Black Italic'),
    'titleu': (os.path.join(FD, 'Exo2.ttf'), b'Black'),
    'bold': (os.path.join(FD, 'Exo2.ttf'), b'Bold'),
    'semi': (os.path.join(FD, 'Exo2.ttf'), b'SemiBold'),
    'boldi': (os.path.join(FD, 'Exo2-Italic.ttf'), b'ExtraBold Italic'),
    'sans': ('/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf', None),
}
_fc = {}


def font(key, size):
    k = (key, size)
    if k not in _fc:
        path, var = FONTS[key]
        f = ImageFont.truetype(path, size)
        if var:
            f.set_variation_by_name(var)
        _fc[k] = f
    return _fc[k]


def fill_box(im, box, mode):
    x0, y0, x1, y1 = box
    px = im.load()
    W, H = im.size
    if mode == 'none':
        return
    if isinstance(mode, (list, tuple)):
        c = tuple(mode) + ((255,) if len(mode) == 3 else ())
        for y in range(y0, y1):
            for x in range(x0, x1):
                px[x, y] = c
        return
    if mode == 'h':
        for y in range(y0, y1):
            a = px[max(x0 - 1, 0), y]
            b = px[min(x1, W - 1), y]
            n = x1 - x0 + 1
            for x in range(x0, x1):
                t = (x - x0 + 1) / n
                px[x, y] = tuple(round(a[i] * (1 - t) + b[i] * t) for i in range(4))
    elif mode == 'v':
        for x in range(x0, x1):
            a = px[x, max(y0 - 1, 0)]
            b = px[x, min(y1, H - 1)]
            n = y1 - y0 + 1
            for y in range(y0, y1):
                t = (y - y0 + 1) / n
                px[x, y] = tuple(round(a[i] * (1 - t) + b[i] * t) for i in range(4))


def _measure(f, lines, spacing, ow):
    ws, hs = [], []
    for ln in lines:
        bb = f.getbbox(ln or ' ')
        ws.append(bb[2] - bb[0] + 2 * ow)
        hs.append(f.size)
    return max(ws), sum(hs) + spacing * (len(lines) - 1) + 2 * ow


def draw_text(im, op):
    text = op.get('text', '')
    if not text:
        return
    x0, y0, x1, y1 = op.get('tbox', op['box'])
    bw, bh = x1 - x0, y1 - y0
    lines = text.split('\n')
    ow = op.get('ow', 0)
    sp = op.get('spacing', 2)
    size = op.get('size', bh)
    fk = op.get('font', 'bold')
    hscale = 1.0
    while size > 6:
        f = font(fk, size)
        w, h = _measure(f, lines, sp, ow)
        if w <= bw and h <= bh:
            break
        if h <= bh and op.get('squeeze', True) and w <= bw / 0.78:
            hscale = bw / w  # condense horizontally instead of shrinking
            break
        size -= 1
    f = font(fk, size)
    w, h = _measure(f, lines, sp, ow)
    # render on its own layer (at full width), then scale horizontally if needed
    pad = ow + 4 + abs(op.get('shadow', [0, 0])[0]) + abs(op.get('shadow', [0, 0])[1])
    L = Image.new('RGBA', (int(w) + 2 * pad, int(h) + 2 * pad), (0, 0, 0, 0))
    d = ImageDraw.Draw(L)
    M = Image.new('L', L.size, 0)
    dm = ImageDraw.Draw(M)
    color = tuple(op.get('color', [255, 255, 255])) + (255,)
    oc = op.get('outline')
    al = op.get('align', 'c')
    y = pad + ow
    for ln in lines:
        bb = f.getbbox(ln or ' ')
        lw = bb[2] - bb[0]
        if al == 'c':
            x = pad + (w - lw) / 2 - bb[0]
        elif al == 'l':
            x = pad + ow - bb[0]
        else:
            x = pad + w - ow - lw - bb[0]
        asc = f.getmetrics()[0]
        ty = y - (f.getbbox('Ag')[1]) * 0
        if op.get('shadow'):
            sx, sy = op['shadow']
            sc = tuple(op.get('shadowcolor', [0, 0, 0])) + (200,)
            d.text((x + sx, ty + sy), ln, font=f, fill=sc,
                   stroke_width=ow, stroke_fill=sc)
        if oc:
            d.text((x, ty), ln, font=f, fill=color, stroke_width=ow,
                   stroke_fill=tuple(oc) + (255,))
        else:
            d.text((x, ty), ln, font=f, fill=color)
        dm.text((x, ty), ln, font=f, fill=255)
        y += f.size + sp
    if op.get('grad'):
        c0, c1 = op['grad']
        bb = M.getbbox() or (0, 0, 1, 1)
        G = Image.new('RGBA', L.size)
        gp = G.load()
        for yy in range(L.height):
            t = min(1, max(0, (yy - bb[1]) / max(1, bb[3] - bb[1] - 1)))
            c = tuple(round(c0[i] * (1 - t) + c1[i] * t) for i in range(3)) + (255,)
            for xx in range(L.width):
                gp[xx, yy] = c
        L.paste(G, (0, 0), M)
    if op.get('glow'):
        g = L.split()[3].filter(ImageFilter.GaussianBlur(op.get('glowr', 4)))
        gl = Image.new('RGBA', L.size, tuple(op['glow']) + (0,))
        gl.putalpha(g.point(lambda v: min(255, v * 2)))
        gl.alpha_composite(L)
        L = gl
    if hscale < 1:
        L = L.resize((max(1, int(L.width * hscale)), L.height), Image.LANCZOS)
    if op.get('rotate'):
        L = L.rotate(op['rotate'], resample=Image.BICUBIC, expand=True)
    # trim to the actual ink so vertical centering is accurate
    bb = L.getbbox() or (0, 0, L.width, L.height)
    L = L.crop(bb)
    lw, lh = L.size
    if al == 'c':
        px = x0 + (bw - lw) // 2
    elif al == 'l':
        px = x0
    else:
        px = x1 - lw
    va = op.get('valign', 'c')
    py = y0 + (bh - lh) // 2 if va == 'c' else y0
    im.alpha_composite(L, (int(px), int(py)))


def apply(im, spec):
    im = im.convert('RGBA').copy()
    for op in spec:
        if 'box' in op:
            fill_box(im, op['box'], op.get('fill', 'h'))
    for op in spec:
        draw_text(im, op)
    return im
