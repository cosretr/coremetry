// @vitest-environment jsdom
//
// stmtTracesPivotRow.render.test.tsx — v0.10.1093. Operatör (prod, statement
// detayı): "Bu sayfada traces alanı yok, ilgili statement'ın trace'lerine
// gidemiyorum." Pinlenen: satır iki link çizer (tüm / hatalı), ikisi de
// /traces'e ifade KİMLİĞİYLE (db_stmt_hash) ve sayfanın penceresiyle gider;
// çağıranlar yalnız başlıkta (servis süzgeci yok); üç exemplar linki yerinde;
// sayfa satırı başlığın altında, detail beklemeden monte eder.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { ReactNode } from 'react';
import type { DBStmtDetail } from '@/lib/types';
import { StmtTracesPivotRow, StmtExemplarsSection } from './stmtDetailSections';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null;
let host: HTMLElement | null = null;
function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host;
}
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null; host = null;
});

const HASH = '12345678901234567890';
const q = (href: string | null) => new URLSearchParams((href ?? '').split('?')[1] ?? '');

describe('StmtTracesPivotRow', () => {
  it('iki link: tüm + hatalı; kimlik süzgeci, pencere, servis yok', () => {
    const el = render(
      <StmtTracesPivotRow hash={HASH} system="postgresql" range={{ preset: '6h' }}
        callers={[{ service: 'checkout-api', calls: 10, errors: 0, avgMs: 1, p95Ms: 2, totalMs: 10 },
          { service: 'billing-worker', calls: 4, errors: 1, avgMs: 1, p95Ms: 2, totalMs: 4 }]} />,
    );
    const links = [...el.querySelectorAll('[data-testid="stmt-traces-row"] a')] as HTMLAnchorElement[];
    expect(links.map(a => a.textContent)).toEqual(["Trace'ler →", "Hatalı trace'ler →"]);
    for (const a of links) {
      const href = a.getAttribute('href')!;
      expect(href.startsWith('/traces?')).toBe(true);
      const p = q(href);
      expect(JSON.parse(p.get('filters')!)).toEqual([
        { k: 'db_stmt_hash', op: '=', v: [HASH] },
        { k: 'db.system', op: '=', v: ['postgresql'] },
      ]);
      expect(p.get('range')).toBe('6h');
      expect(p.get('rootOnly')).toBe('false');
      expect(p.get('service')).toBeNull();
      // Çağıranlar başlıkta, süzgeçte değil.
      expect(a.title).toContain('checkout-api, billing-worker');
      expect(a.title).toContain(`db_stmt_hash = ${HASH}`);
    }
    expect(q(links[0].getAttribute('href')).get('hasError')).toBeNull();
    expect(q(links[1].getAttribute('href')).get('hasError')).toBe('true');
  });

  it('çağıran yokken de çizilir (detail yüklenirken)', () => {
    const el = render(<StmtTracesPivotRow hash={HASH} range={{ preset: '1h' }} />);
    const a = el.querySelector('[data-testid="stmt-traces-row"] a') as HTMLAnchorElement;
    expect(JSON.parse(q(a.getAttribute('href')).get('filters')!)).toEqual([{ k: 'db_stmt_hash', op: '=', v: [HASH] }]);
    expect(a.title).not.toContain('Çağıranlar');
  });

  it('üç exemplar linki yerinde (satır onların yerine geçmez)', () => {
    const detail: DBStmtDetail = {
      stmtHash: HASH, fromNs: 1, toNs: 2, summary: null, trend: null, callers: [],
      exemplars: { slowTraceId: 'a'.repeat(32), errorTraceId: 'b'.repeat(32) },
    };
    const el = render(<StmtExemplarsSection detail={detail} range={{ preset: '1h' }} />);
    const texts = [...el.querySelectorAll('a')].map(a => a.textContent);
    expect(texts).toEqual(['slowest →', 'worst error →', "N+1 trace'leri →"]);
  });

  it('StatementDetail satırı başlığın altında, kimlikle ve sayfa penceresiyle monte eder', () => {
    const src = readFileSync(resolve(__dirname, '../StatementDetail.tsx'), 'utf8');
    expect(src).toMatch(/<StmtTracesPivotRow hash=\{refObj\.hash\}[\s\S]*?range=\{range\}/);
    // detail'in DIŞINDA (yüklenirken de görünür) ve StmtText'ten önce.
    const row = src.indexOf('<StmtTracesPivotRow');
    expect(row).toBeGreaterThan(0);
    expect(row).toBeLessThan(src.indexOf('<StmtText'));
    expect(src.indexOf('<StmtExemplarsSection')).toBeGreaterThan(row);
  });
});
