#!/usr/bin/env python3
"""Turn a `tmux capture-pane -e -p` capture into a terminal-window SVG.

usage: svg.py capture.ans out.svg [--title margin] [--extra extra.ans]

--extra appends a second capture below a divider, for the agent pane under the viewer.
"""
import argparse
import html
import re

FONT = 16
CW = 9.6
LH = 21
PAD = 16
BAR = 38
FG = "#c9d1d9"
BG = "#0d1117"

SGR = re.compile(r"\x1b\[([0-9;:]*)m")


def parse(text):
    """Yield rows of cells: (char, fg, bg, bold, italic, strike)."""
    rows = []
    fg, bg, bold, italic, strike = None, None, False, False, False
    for line in text.rstrip("\n").split("\n"):
        row = []
        pos = 0
        for m in SGR.finditer(line):
            for ch in line[pos:m.start()]:
                row.append((ch, fg, bg, bold, italic, strike))
            pos = m.end()
            codes = [c for c in re.split("[;:]", m.group(1)) if c != ""] or ["0"]
            i = 0
            while i < len(codes):
                c = int(codes[i])
                if c == 0:
                    fg, bg, bold, italic, strike = None, None, False, False, False
                elif c == 1:
                    bold = True
                elif c == 3:
                    italic = True
                elif c == 9:
                    strike = True
                elif c == 22:
                    bold = False
                elif c == 23:
                    italic = False
                elif c == 29:
                    strike = False
                elif c == 39:
                    fg = None
                elif c == 49:
                    bg = None
                elif c in (38, 48) and i + 4 < len(codes) and codes[i + 1] == "2":
                    col = "#%02x%02x%02x" % tuple(int(x) for x in codes[i + 2:i + 5])
                    if c == 38:
                        fg = col
                    else:
                        bg = col
                    i += 4
                i += 1
        for ch in line[pos:]:
            row.append((ch, fg, bg, bold, italic, strike))
        rows.append(row)
    return rows


def runs(row, key):
    out = []
    for i, cell in enumerate(row):
        k = key(cell)
        if out and out[-1][0] == k and out[-1][2] == i:
            out[-1][2] = i + 1
            out[-1][3] += cell[0]
        else:
            out.append([k, i, i + 1, cell[0]])
    return out


def draw(rows, y0, out):
    for r, row in enumerate(rows):
        y = y0 + r * LH
        for bg, a, b, _ in runs(row, lambda c: c[2]):
            if bg:
                out.append(f'<rect x="{PAD + a * CW:.1f}" y="{y}" width="{(b - a) * CW:.1f}" height="{LH}" fill="{bg}"/>')
        for (fg, bold, italic, strike), a, b, text in runs(row, lambda c: c[1:2] + c[3:]):
            if not text.strip():
                continue
            attrs = f' fill="{fg or FG}"'
            if bold:
                attrs += ' font-weight="bold"'
            if italic:
                attrs += ' font-style="italic"'
            if strike:
                attrs += ' text-decoration="line-through"'
            out.append(f'<text x="{PAD + a * CW:.1f}" y="{y + LH - 6}" textLength="{(b - a) * CW:.1f}" '
                       f'lengthAdjust="spacingAndGlyphs"{attrs}>{html.escape(text, quote=False)}</text>')


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("capture")
    ap.add_argument("out")
    ap.add_argument("--title", default="margin")
    ap.add_argument("--extra")
    args = ap.parse_args()

    rows = parse(open(args.capture, encoding="utf-8").read())
    extra = parse(open(args.extra, encoding="utf-8").read()) if args.extra else []
    cols = max(len(r) for r in rows + extra)
    height = len(rows) + (len(extra) + 1 if extra else 0)
    w = round(PAD * 2 + cols * CW)
    h = BAR + PAD + height * LH + PAD

    out = [
        f'<svg xmlns="http://www.w3.org/2000/svg" xml:space="preserve" width="{w}" height="{h}" viewBox="0 0 {w} {h}">',
        f'<style>text{{font-family:"JetBrains Mono","SF Mono",Menlo,Consolas,"DejaVu Sans Mono",monospace;font-size:{FONT}px;white-space:pre}}</style>',
        f'<rect width="{w}" height="{h}" rx="10" fill="{BG}" stroke="#30363d"/>',
        f'<rect width="{w}" height="{BAR}" rx="10" fill="#161b22"/><rect y="{BAR - 10}" width="{w}" height="10" fill="#161b22"/>',
        f'<line x1="0" y1="{BAR}" x2="{w}" y2="{BAR}" stroke="#30363d"/>',
    ]
    for i, c in enumerate(["#ff5f57", "#febc2e", "#28c840"]):
        out.append(f'<circle cx="{22 + i * 22}" cy="{BAR / 2}" r="6.5" fill="{c}"/>')
    out.append(f'<text x="{w / 2}" y="{BAR / 2 + 5}" text-anchor="middle" fill="#8b949e">{html.escape(args.title)}</text>')
    y0 = BAR + PAD
    draw(rows, y0, out)
    if extra:
        ydiv = y0 + len(rows) * LH + LH / 2
        out.append(f'<line x1="{PAD}" y1="{ydiv}" x2="{w - PAD}" y2="{ydiv}" stroke="#30363d"/>')
        draw(extra, y0 + (len(rows) + 1) * LH, out)
    out.append("</svg>")
    with open(args.out, "w", encoding="utf-8") as f:
        f.write("\n".join(out) + "\n")


if __name__ == "__main__":
    main()
