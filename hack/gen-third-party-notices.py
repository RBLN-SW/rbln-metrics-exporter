# Copyright 2026 Rebellions Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Generate THIRD_PARTY_NOTICES for the Go modules compiled into the binaries.

Usage: python3 hack/gen-third-party-notices.py <repo root> [output path]

Two modes, selected automatically:
- vendor mode: vendor/modules.txt exists. Every vendored module's LICENSE,
  COPYING and NOTICE files (including nested ones for forked or internal
  third-party packages) are copied into one document.
- module mode: no vendor directory. The linked package set is taken from
  `GOOS=linux go list -deps ./...` and license files are collected from the
  module cache, at the module root and in the directories of the linked
  packages (walking up to the module root).

Modules without any license file are listed with "License: NONE" so the gap
is visible; they must be fixed at the source.
"""
import os, subprocess, sys
from collections import Counter, OrderedDict

R = sys.argv[1].rstrip('/') + '/'
OUT = sys.argv[2] if len(sys.argv) > 2 else R + 'THIRD_PARTY_NOTICES'
def _product():
    try:
        first = open(R + 'NOTICE', encoding='utf-8').readline().strip()
        if first: return first
    except OSError:
        pass
    return os.path.basename(os.path.abspath(R))
PRODUCT = _product()

def classify(t):
    t2 = ' '.join(t.lower().split())
    if 'apache license' in t2 and 'version 2.0' in t2: return 'Apache-2.0'
    if 'mozilla public license' in t2: return 'MPL-2.0'
    if 'permission is hereby granted, free of charge' in t2: return 'MIT'
    if 'redistribution and use in source and binary forms' in t2:
        return 'BSD-3-Clause' if 'neither the name' in t2 else 'BSD-2-Clause'
    if 'permission to use, copy, modify, and/or distribute this software for any purpose' in t2 or 'permission to use, copy, modify, and distribute this software for any purpose' in t2: return 'ISC'
    if 'gnu general public license' in t2 or 'gnu lesser' in t2: return 'GPL/LGPL?'
    return 'UNKNOWN'

def is_lic(n): return n.lower().startswith(('license', 'copying')) and not n.lower().endswith(('.go', '.py', '.sh', '.md.go'))
def is_notice(n): return n.lower().startswith('notice') and not n.lower().endswith(('.go', '.py', '.sh'))

def files_in(d):
    try: names = sorted(os.listdir(d))
    except FileNotFoundError: return [], []
    lic = [d + '/' + n for n in names if is_lic(n) and os.path.isfile(d + '/' + n)]
    notice = [d + '/' + n for n in names if is_notice(n) and os.path.isfile(d + '/' + n)]
    return lic, notice

def nested_vendor(d, modroots):
    out = []
    for dp, dn, fn in os.walk(d):
        dn.sort()  # deterministic traversal order across filesystems
        if dp == d: continue
        rel = os.path.relpath(dp, R + 'vendor/')
        if rel in modroots: dn[:] = []; continue
        for n in sorted(fn):
            if (is_lic(n) or is_notice(n)) and os.path.isfile(dp + '/' + n): out.append(dp + '/' + n)
    return sorted(out)

def has_go_sources(d, modroots):
    """True if the vendored module directory contains Go sources outside nested module roots."""
    for dp, dn, fn in os.walk(d):
        rel = os.path.relpath(dp, R + 'vendor/')
        if dp != d and rel in modroots: dn[:] = []; continue
        if any(n.endswith('.go') for n in fn): return True
    return False

entries = []  # (module, version, license, [root lic], [root notice], [nested files], base)
skipped = []
if os.path.exists(R + 'vendor/modules.txt'):
    MODE = 'vendor'
    mods = []          # (module, version, has vendored packages)
    for ln in open(R + 'vendor/modules.txt'):
        if ln.startswith('# '):
            parts = ln[2:].split(); mods.append([parts[0], parts[1] if len(parts) > 1 else '', False])
        elif ln.strip() and not ln.startswith('#') and mods:
            mods[-1][2] = True
    modroots = set(m for m, _, _ in mods)
    for m, v, has_pkgs in mods:
        d = R + 'vendor/' + m
        if not has_pkgs: skipped.append((m, 'no packages vendored (not compiled in)')); continue
        if not os.path.isdir(d): skipped.append((m, 'not vendored')); continue
        lic, notice = files_in(d)
        nested = nested_vendor(d, modroots)
        if not has_go_sources(d, modroots): skipped.append((m, 'no vendored packages')); continue
        if not lic and not nested: entries.append((m, v, 'NONE', [], notice, [], R)); continue
        l = classify(open(lic[0], encoding='utf-8', errors='replace').read()) if lic else 'see nested'
        entries.append((m, v, l, lic, notice, nested, R))
else:
    MODE = 'module'
    env = dict(os.environ, GOOS='linux', GOFLAGS='-mod=mod')
    out = subprocess.run(['go', 'list', '-deps', '-f', '{{if and .Module (not .Module.Main)}}{{.Module.Path}}\t{{.Module.Version}}\t{{.Module.Dir}}\t{{.Dir}}{{end}}', './...'],
                         capture_output=True, text=True, cwd=R, env=env)
    if out.returncode != 0:
        sys.stderr.write(out.stderr); sys.exit(1)
    bymod = OrderedDict()
    for ln in out.stdout.splitlines():
        if not ln.strip(): continue
        m, v, mdir, pdir = ln.split('\t')
        bymod.setdefault((m, v, mdir), []).append(pdir)
    for (m, v, mdir), pdirs in sorted(bymod.items()):
        if not mdir: skipped.append((m, 'module dir not in cache')); continue
        lic, notice = files_in(mdir)
        nested = []
        seen = set()
        for pd in pdirs:
            d = pd
            while d.startswith(mdir) and d != mdir:
                if d not in seen:
                    seen.add(d)
                    l2, n2 = files_in(d)
                    nested += l2 + n2
                d = os.path.dirname(d)
        nested = sorted(set(nested))
        if not lic and not nested: entries.append((m, v, 'NONE', [], notice, [], mdir)); continue
        l = classify(open(lic[0], encoding='utf-8', errors='replace').read()) if lic else 'see nested'
        entries.append((m, v, l, lic, notice, nested, mdir))

c = Counter(e[2] for e in entries)

def clean(text):
    return '\n'.join(l.rstrip() for l in text.rstrip('\n').split('\n'))

def relname(f, base, m):
    if MODE == 'vendor': return os.path.relpath(f, R)
    return m + '/' + os.path.relpath(f, base)

out = ['THIRD-PARTY NOTICES FOR ' + PRODUCT.upper() + '\n',
       'This file lists the third-party Go modules that are compiled into the binaries\n'
       'of this project, with the full text of their licenses and NOTICE files.\n'
       f'Generated in {MODE} mode by hack/gen-third-party-notices.py; do not edit by hand.\n',
       'Summary: ' + ', '.join(f'{k} {n}' for k, n in sorted(c.items())) + f' (total {len(entries)} modules)\n',
       '=' * 78 + '\nMODULE LIST\n' + '=' * 78]
for m, v, l, lic, notice, nested, base in entries:
    out.append(f'{m} {v}  [{l}]')
    for f in nested:
        rel = os.path.relpath(f, base if MODE == 'module' else R + 'vendor/' + m)
        out.append(f'    nested: {os.path.dirname(rel)}  [{classify(open(f, encoding="utf-8", errors="replace").read())}]')
out.append('')
for m, v, l, lic, notice, nested, base in entries:
    out.append('=' * 78 + f'\n{m} {v}\nLicense: {l}\n' + '=' * 78)
    if l == 'NONE':
        out.append('No license file is distributed with this module. See NOTICE.'); out.append(''); continue
    for f in lic + notice + nested:
        out.append(f'--- {relname(f, base, m)} ---'); out.append(clean(open(f, encoding='utf-8', errors='replace').read())); out.append('')
open(OUT, 'w', encoding='utf-8').write('\n'.join(l.rstrip() for l in out).rstrip('\n') + '\n')
print(f'mode: {MODE}; modules: {len(entries)}; nested license files: {sum(len(e[5]) for e in entries)}; summary: {dict(c)}')
print('skipped:', skipped)
for e in entries:
    if e[2] in ('UNKNOWN', 'GPL/LGPL?', 'NONE'): print('CHECK:', e[0], e[2])
