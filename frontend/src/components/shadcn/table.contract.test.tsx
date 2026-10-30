// @vitest-environment jsdom
//
// shadcn/table sözleşmesi — v0.10.1613 (shadcn Faz 1 R5, plan §5.4).
// İzinli düzenleme: satır ritmi --row-h'den (36/24/20, [data-density]
// yeniden eşlemesi; ui/DataTable/rowHeight.ts ROW_H eşitliği ayrıca çivili),
// sabit h-10 / p-2 değil. data-slot her parçada (globals.css'in eleman
// kuralları `:where(:not([data-slot]))` ile bunları atlıyor, R4).
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { Table, TableHeader, TableBody, TableRow, TableHead, TableCell } from './table';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

describe('shadcn Table — yoğunluk ritmi', () => {
  it('th ve td yüksekliği var(--row-h); dikey dolgu yok; data-slot var', () => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => {
      root!.render(
        <Table><TableHeader><TableRow><TableHead>A</TableHead></TableRow></TableHeader>
          <TableBody><TableRow><TableCell>1</TableCell></TableRow></TableBody></Table>,
      );
    });
    const th = host.querySelector('th')!;
    const td = host.querySelector('td')!;
    expect(th.className).toContain('h-(--row-h)');
    expect(th.className).not.toMatch(/(^|\s)h-10(\s|$)/);
    expect(td.className).toContain('h-(--row-h)');
    expect(td.className).toContain('py-0');
    expect(td.className).not.toMatch(/(^|\s)p-2(\s|$)/);
    for (const el of [host.querySelector('table')!, th, td]) expect(el.getAttribute('data-slot')).toBeTruthy();
  });

  it('--row-h her yoğunlukta globals.css\'te tanımlı (36 / 24 / 20)', () => {
    const g = readFileSync(resolve(__dirname, '../../styles/globals.css'), 'utf8');
    expect(g).toMatch(/--row-h: 36px;/);
    expect(g).toMatch(/\[data-density="compact"\] \{ --row-h: 24px;/);
    expect(g).toMatch(/\[data-density="dense"\] \{ --row-h: 20px;/);
  });
});
