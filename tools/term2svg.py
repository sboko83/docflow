#!/usr/bin/env python3
"""Render a raw terminal recording (`script -q`) as an SVG terminal window.

Emulates the subset docflow's TTY output uses: SGR colours (16, 256 and
truecolor), carriage return, erase-line, backspace and tabs. Other escape
sequences are dropped.

usage: term2svg.py RAW OUT.svg [--cols N] [--title T]
                   [--from REGEX] [--to REGEX] [--skip REGEX] [--prompt CMD]
--from picks the first matching line, --to ends before the first match after
it, --skip drops matching lines, --prompt prepends a `$ CMD` line. Standard library only.
"""
import argparse
import html
import re
import sys

BG = "#16191d"
FG = "#d4d7dc"
PALETTE = [
    "#2b2f36", "#f2686a", "#4fc66b", "#d6b84a", "#5a8ff0", "#c678dd", "#4ab7c8", "#d4d7dc",
    "#6b717c", "#ff7b7d", "#6fdc89", "#ecd067", "#7aa7ff", "#d98ef0", "#68d1e0", "#ffffff",
]
# Background colours are darker so light text on a badge stays readable.
BG_PALETTE = [
    "#2b2f36", "#b8383b", "#2e8b47", "#9c7f1e", "#2f5fb3", "#8a3fa8", "#227f8f", "#c9cdd3",
    "#4a4f58", "#cf4a4d", "#3aa258", "#b8972a", "#3f73cc", "#a050c2", "#2c97a8", "#e6e8eb",
]


def xterm256(n):
    if n < 16:
        return PALETTE[n]
    if n < 232:
        n -= 16
        steps = [0, 95, 135, 175, 215, 255]
        return "#%02x%02x%02x" % (steps[n // 36], steps[n // 6 % 6], steps[n % 6])
    v = 8 + (n - 232) * 10
    return "#%02x%02x%02x" % (v, v, v)


class Style:
    __slots__ = ("fg", "bg", "bold", "dim")

    def __init__(self, fg=None, bg=None, bold=False, dim=False):
        self.fg, self.bg, self.bold, self.dim = fg, bg, bold, dim

    def key(self):
        return (self.fg, self.bg, self.bold, self.dim)

    def copy(self):
        return Style(self.fg, self.bg, self.bold, self.dim)


def apply_sgr(style, params):
    codes = [int(p) if p else 0 for p in params.split(";")] if params else [0]
    i = 0
    while i < len(codes):
        c = codes[i]
        if c == 0:
            style = Style()
        elif c == 1:
            style.bold = True
        elif c == 2:
            style.dim = True
        elif c == 22:
            style.bold = style.dim = False
        elif 30 <= c <= 37:
            style.fg = PALETTE[c - 30]
        elif 90 <= c <= 97:
            style.fg = PALETTE[c - 90 + 8]
        elif 40 <= c <= 47:
            style.bg = BG_PALETTE[c - 40]
        elif 100 <= c <= 107:
            style.bg = BG_PALETTE[c - 100 + 8]
        elif c == 39:
            style.fg = None
        elif c == 49:
            style.bg = None
        elif c in (38, 48) and i + 1 < len(codes):
            if codes[i + 1] == 5 and i + 2 < len(codes):
                colour = xterm256(codes[i + 2])
                i += 2
            elif codes[i + 1] == 2 and i + 4 < len(codes):
                colour = "#%02x%02x%02x" % tuple(codes[i + 2:i + 5])
                i += 4
            else:
                colour = None
            if c == 38:
                style.fg = colour
            else:
                style.bg = colour
        i += 1
    return style


ESC_RE = re.compile(r"\x1b(?:\[([0-9;?]*)([@-~])|\][^\x07\x1b]*(?:\x07|\x1b\\)|.)")


def emulate(data, cols):
    lines = [[]]
    row = col = 0
    style = Style()
    pos = 0
    while pos < len(data):
        ch = data[pos]
        if ch == "\x1b":
            m = ESC_RE.match(data, pos)
            if not m:
                pos += 1
                continue
            pos = m.end()
            if m.group(2) == "m":
                style = apply_sgr(style.copy(), m.group(1))
            elif m.group(2) == "K":
                del lines[row][col:]
            continue
        pos += 1
        if ch == "\n":
            row += 1
            if row == len(lines):
                lines.append([])
        elif ch == "\r":
            col = 0
        elif ch == "\b":
            col = max(0, col - 1)
        elif ch == "\t":
            col = (col // 8 + 1) * 8
        elif ch < " " or ch == "\x7f":
            continue
        else:
            if col >= cols:
                row, col = row + 1, 0
                if row == len(lines):
                    lines.append([])
            line = lines[row]
            while len(line) < col:
                line.append((" ", Style()))
            cell = (ch, style.copy())
            if col < len(line):
                line[col] = cell
            else:
                line.append(cell)
            col += 1
    return lines


def plain(line):
    return "".join(c for c, _ in line).rstrip()


def select(lines, start, end, skip):
    first = 0
    if start:
        first = next((i for i, l in enumerate(lines) if re.search(start, plain(l))), None)
        if first is None:
            sys.exit("term2svg: --from %r not found" % start)
    last = len(lines)
    if end:
        last = next((i for i in range(first + 1, len(lines)) if re.search(end, plain(lines[i]))), None)
        if last is None:
            sys.exit("term2svg: --to %r not found" % end)
    picked = [l for l in lines[first:last] if not (skip and re.search(skip, plain(l)))]
    while picked and not plain(picked[0]):
        picked.pop(0)
    while picked and not plain(picked[-1]):
        picked.pop()
    return picked


def runs(line):
    out = []
    for i, (ch, st) in enumerate(line):
        if out and out[-1][2].key() == st.key():
            out[-1][1] += ch
        else:
            out.append([i, ch, st])
    return out


def render(lines, cols, title):
    size, cw, lh = 14, 8.43, 18
    pad, top = 18, 44
    width = round(pad * 2 + cols * cw)
    height = round(top + len(lines) * lh + pad)
    o = [
        '<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">'
        % (width, height, width, height),
        "<style>text{font-family:'SF Mono',Menlo,Consolas,'DejaVu Sans Mono','Liberation Mono',monospace;"
        "font-size:%dpx;white-space:pre}.b{font-weight:700}.d{opacity:.55}</style>" % size,
        '<rect width="100%%" height="100%%" rx="10" fill="%s"/>' % BG,
        '<rect x="0.5" y="0.5" width="%d" height="%d" rx="10" fill="none" stroke="#2c3036"/>'
        % (width - 1, height - 1),
    ]
    for i, colour in enumerate(("#ff5f57", "#febc2e", "#28c840")):
        o.append('<circle cx="%d" cy="20" r="6" fill="%s"/>' % (22 + i * 20, colour))
    if title:
        o.append('<text x="%d" y="25" text-anchor="middle" fill="#7d838d">%s</text>'
                 % (width // 2, html.escape(title)))
    for n, line in enumerate(lines):
        y = top + n * lh
        for col, text, st in runs(line):
            if st.bg:
                o.append('<rect x="%.1f" y="%d" width="%.1f" height="%d" fill="%s"/>'
                         % (pad + col * cw, y, len(text) * cw, lh, st.bg))
        for col, text, st in runs(line):
            if not text.strip():
                continue
            cls = " ".join(c for c, on in (("b", st.bold), ("d", st.dim)) if on)
            o.append('<text x="%.1f" y="%d" fill="%s"%s textLength="%.1f" lengthAdjust="spacingAndGlyphs">%s</text>'
                     % (pad + col * cw, y + 13, st.fg or FG, ' class="%s"' % cls if cls else "",
                        len(text) * cw, html.escape(text)))
    o.append("</svg>")
    return "\n".join(o) + "\n"


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("raw")
    ap.add_argument("out")
    ap.add_argument("--cols", type=int, default=100)
    ap.add_argument("--title", default="")
    ap.add_argument("--from", dest="start")
    ap.add_argument("--to", dest="end")
    ap.add_argument("--skip")
    ap.add_argument("--prompt", help="command shown as a shell prompt line above the selection")
    a = ap.parse_args()
    with open(a.raw, encoding="utf-8", errors="replace", newline="") as f:
        lines = emulate(f.read(), a.cols)
    picked = select(lines, a.start, a.end, a.skip)
    if a.prompt:
        green = Style(fg=PALETTE[2], bold=True)
        picked = [[("$", green), (" ", Style())] + [(c, Style()) for c in a.prompt]] + picked
    if not picked:
        sys.exit("term2svg: nothing selected")
    with open(a.out, "w", encoding="utf-8") as f:
        f.write(render(picked, max(a.cols, max(len(l) for l in picked)), a.title))
    print("%s: %d lines" % (a.out, len(picked)))


if __name__ == "__main__":
    main()
