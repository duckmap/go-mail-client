#!/usr/bin/env python3
"""ansi2html.py — конвертер ANSI-снимков терминала в один HTML-файл.

Использование: python3 scripts/ansi2html.py demo/*.ansi -o demo/index.html
"""
import argparse
import glob
import html as htmlmod
import os
import re
import sys

SGR_RE = re.compile(r'\x1b\[([0-9;:]*)m')
CSI_RE = re.compile(r'\x1b\[[0-9;:?]*[a-zA-Z]')
OSC_RE = re.compile(r'\x1b\][^\x07\x1b]*(\x07|\x1b\\)')

DEFAULT_FG = '#c0caf5'
DEFAULT_BG = '#1a1b26'

# базовая палитра xterm 0..15
BASE16 = [
    '#15161e', '#f7768e', '#9ece6a', '#e0af68',
    '#7aa2f7', '#bb9af7', '#7dcfff', '#a9b1d6',
    '#414868', '#f7768e', '#9ece6a', '#e0af68',
    '#7aa2f7', '#bb9af7', '#7dcfff', '#c0caf5',
]


def color256(n):
    if n < 16:
        return BASE16[n]
    if n < 232:
        n -= 16
        r, g, b = (n // 36) % 6, (n // 6) % 6, n % 6
        vals = [0, 95, 135, 175, 215, 255]
        return '#%02x%02x%02x' % (vals[r], vals[g], vals[b])
    v = 8 + (n - 232) * 10
    return '#%02x%02x%02x' % (v, v, v)


class State:
    def __init__(self):
        self.fg = None
        self.bg = None
        self.bold = False
        self.faint = False
        self.italic = False
        self.underline = False
        self.inverse = False

    def copy(self):
        s = State()
        s.__dict__.update(self.__dict__)
        return s

    def css(self):
        fg, bg = self.fg, self.bg
        if self.inverse:
            fg, bg = (bg or DEFAULT_BG), (fg or DEFAULT_FG)
        parts = []
        if fg:
            parts.append('color:%s' % fg)
        if bg:
            parts.append('background:%s' % bg)
        if self.bold:
            parts.append('font-weight:bold')
        if self.faint:
            parts.append('opacity:.65')
        if self.italic:
            parts.append('font-style:italic')
        if self.underline:
            parts.append('text-decoration:underline')
        return ';'.join(parts)


def apply_sgr(state, params):
    codes = [int(p) if p else 0 for p in re.split('[;]', params)] if params else [0]
    i = 0
    while i < len(codes):
        c = codes[i]
        if c == 0:
            state.__dict__.update(State().__dict__)
        elif c == 1:
            state.bold = True
        elif c == 2:
            state.faint = True
        elif c == 3:
            state.italic = True
        elif c == 4:
            state.underline = True
        elif c == 7:
            state.inverse = True
        elif c in (22,):
            state.bold = state.faint = False
        elif c == 23:
            state.italic = False
        elif c == 24:
            state.underline = False
        elif c == 27:
            state.inverse = False
        elif 30 <= c <= 37:
            state.fg = BASE16[c - 30]
        elif c == 38:
            if i + 1 < len(codes) and codes[i + 1] == 5:
                state.fg = color256(codes[i + 2]); i += 2
            elif i + 4 < len(codes) and codes[i + 1] == 2:
                state.fg = '#%02x%02x%02x' % tuple(codes[i + 2:i + 5]); i += 4
        elif c == 39:
            state.fg = None
        elif 40 <= c <= 47:
            state.bg = BASE16[c - 40]
        elif c == 48:
            if i + 1 < len(codes) and codes[i + 1] == 5:
                state.bg = color256(codes[i + 2]); i += 2
            elif i + 4 < len(codes) and codes[i + 1] == 2:
                state.bg = '#%02x%02x%02x' % tuple(codes[i + 2:i + 5]); i += 4
        elif c == 49:
            state.bg = None
        elif 90 <= c <= 97:
            state.fg = BASE16[c - 90 + 8]
        elif 100 <= c <= 107:
            state.bg = BASE16[c - 100 + 8]
        i += 1


def ansi_to_html(text):
    text = OSC_RE.sub('', text)
    out = []
    state = State()
    pos = 0
    for m in SGR_RE.finditer(text):
        chunk = CSI_RE.sub('', text[pos:m.start()])
        if chunk:
            css = state.css()
            esc = htmlmod.escape(chunk)
            out.append('<span style="%s">%s</span>' % (css, esc) if css else esc)
        apply_sgr(state, m.group(1))
        pos = m.end()
    tail = CSI_RE.sub('', text[pos:])
    if tail:
        css = state.css()
        esc = htmlmod.escape(tail)
        out.append('<span style="%s">%s</span>' % (css, esc) if css else esc)
    return ''.join(out)


TITLES = {
    '01_main': 'Главный экран: папки · список писем · просмотр',
    '02_main_attachment': 'Письмо с вложениями',
    '03_picker_move': 'Перемещение письма в папку',
    '04_search': 'Поиск по папке',
    '05_compose_reply': 'Ответ на письмо (с цитатой)',
    '06_help': 'Справка по горячим клавишам',
    '07_accounts': 'Настройка учётных записей',
    '08_accounts_form': 'Форма параметров ящика',
    '09_accounts_new': 'Добавление нового ящика',
}

PAGE = """<!DOCTYPE html>
<html lang="ru"><head><meta charset="utf-8">
<title>gomail — скриншоты интерфейса</title>
<style>
 body{{background:#16161e;color:#c0caf5;font-family:system-ui,sans-serif;margin:0;padding:24px}}
 h1{{color:#7aa2f7;font-size:22px}}
 p.desc{{color:#8a90b8;max-width:900px;line-height:1.5}}
 section{{margin:28px 0}}
 h2{{color:#bb9af7;font-size:15px;font-weight:600;margin-bottom:8px}}
 pre{{background:{bg};border:1px solid #2f3549;border-radius:8px;padding:10px 12px;
     font-family:"SF Mono",Consolas,"Liberation Mono",Menlo,monospace;
     font-size:12px;line-height:1.32;overflow-x:auto;white-space:pre;margin:0}}
 .note{{color:#565f89;font-size:12px;margin-top:18px}}
</style></head><body>
<h1>gomail — консольный почтовый клиент</h1>
<p class="desc">Настоящие снимки отрисовки TUI (140×42), полученные при автоматических
тестах на встроенном демо-сервере. В реальном терминале интерфейс занимает всё окно
и обновляется в реальном времени.</p>
{sections}
<p class="note">Сгенерировано scripts/ansi2html.py из demo/*.ansi</p>
</body></html>
"""


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('files', nargs='+')
    ap.add_argument('-o', '--output', default='demo/index.html')
    args = ap.parse_args()

    files = []
    for pat in args.files:
        files.extend(sorted(glob.glob(pat)) if any(ch in pat for ch in '*?[') else [pat])

    sections = []
    for f in files:
        name = os.path.splitext(os.path.basename(f))[0]
        title = TITLES.get(name, name)
        with open(f, encoding='utf-8') as fh:
            data = fh.read()
        sections.append('<section><h2>%s</h2><pre>%s</pre></section>'
                        % (htmlmod.escape(title), ansi_to_html(data)))

    os.makedirs(os.path.dirname(args.output) or '.', exist_ok=True)
    with open(args.output, 'w', encoding='utf-8', newline='\n') as fh:
        fh.write(PAGE.format(bg=DEFAULT_BG, sections='\n'.join(sections)))
    print('written:', args.output)


if __name__ == '__main__':
    main()
