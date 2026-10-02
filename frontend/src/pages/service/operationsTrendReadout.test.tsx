// @vitest-environment jsdom
// operationsTrendReadout.test.tsx — v0.10.1059. Operatör (prod, servis →
// Operations): "bir servisin herhangi birinin üzerine gelince bir şey çıkıyor
// ama anlaşılmıyor." Trend hücresinde iki şey açılıyordu: hücrenin İÇİNDE
// absolute çizilen kova okuması (`tbody td { overflow: hidden }` onu yarıdan
// kesiyordu, "kova 16/30" — saat yok) ve düğmenin yerel `title`ı (başka şey
// söyleyen pencere toplamları). Çiviler:
//   • okuma tek: kovanın SAAT penceresi + calls · errors · p99 + "tıkla: grafik";
//   • okuma satırın torunu DEĞİL (body portalı) → hücre/satır kırpamaz;
//   • trend düğmesi ve ataları `title` taşımaz, svg'de <title> yok;
//   • "All" satırı da aynı okumayı alır, yerel <title> basmaz;
//   • dokunmatik işaretçi okumayı açmaz; tık davranışı aynı.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { TrendSpark } from '@/components/TrendSpark';
import { OperationsTable } from './OperationsTable';
import { sparkBucketWindow, fmtBucketWindow } from '@/lib/sparkline';
import type { OperationRow } from '@/lib/types';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const FROM = Date.UTC(2026, 9, 2, 21, 0, 0);
const TO = FROM + 3600_000;
const calls = Array.from({ length: 60 }, (_, i) => 100 + i);
const errors = Array.from({ length: 60 }, () => 1);
const p99 = Array.from({ length: 60 }, (_, i) => 10 + i);

let host: HTMLDivElement; let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });

// jsdom ölçmez: svg'ye tarayıcıdaki gibi bir kutu ver.
function sizeSvg(svg: Element, width = 160) {
  (svg as SVGSVGElement).getBoundingClientRect = () =>
    ({ left: 0, top: 200, right: width, bottom: 230, width, height: 30, x: 0, y: 200, toJSON: () => ({}) }) as DOMRect;
}
function move(svg: Element, clientX: number, pointerType = 'mouse') {
  act(() => { svg.dispatchEvent(new PointerEvent('pointermove', { bubbles: true, clientX, clientY: 215, pointerType })); });
}
function leave(svg: Element) {
  act(() => { svg.dispatchEvent(new PointerEvent('pointerout', { bubbles: true, pointerType: 'mouse', relatedTarget: document.body })); });
}
const readout = () => document.body.querySelector<HTMLElement>('.spark-readout');

describe('sparkBucketWindow / fmtBucketWindow (v0.10.1059)', () => {
  it('çizilen kova birleşmiş k ham kovanın saat penceresidir', () => {
    // 160 px → bütçe 53 çubuk; 60 ham kova downsampleBuckets'ta k = 2 ile 30
    // çubuğa iner. Ham adım 60 sn → çubuk 15 = 21:30–21:32.
    const w = sparkBucketWindow(15, 30, 60, FROM, TO)!;
    expect(new Date(w.startMs).toISOString()).toBe('2026-10-02T21:30:00.000Z');
    expect(new Date(w.endMs).toISOString()).toBe('2026-10-02T21:32:00.000Z');
    expect(w.stepSec).toBe(120);
    // Son çubuk pencere sonunda kesilir.
    expect(sparkBucketWindow(29, 30, 60, FROM, TO)!.endMs).toBe(TO);
    // Geçersiz girdi → null.
    expect(sparkBucketWindow(30, 30, 60, FROM, TO)).toBeNull();
    // Birleşmemiş seri: çubuk = ham kova.
    expect(sparkBucketWindow(0, 60, 60, FROM, TO)).toEqual({ startMs: FROM, endMs: FROM + 60_000, stepSec: 60 });
    expect(sparkBucketWindow(0, 0, 60, FROM, TO)).toBeNull();
    expect(sparkBucketWindow(0, 10, 10, TO, FROM)).toBeNull();
  });
  it('etiket: tarih + saat, bitiş aynı gündeyse yalnız saat (TZ=UTC)', () => {
    expect(fmtBucketWindow(FROM, FROM + 300_000, 300)).toBe('02.10.2026 21:00 – 21:05');
    expect(fmtBucketWindow(FROM, FROM + 30_000, 30)).toBe('02.10.2026 21:00:00 – 21:00:30');
    const late = Date.UTC(2026, 9, 2, 23, 55);
    expect(fmtBucketWindow(late, late + 600_000, 600)).toBe('02.10.2026 23:55 – 03.10.2026 00:05');
  });
});

describe('TrendSpark kova okuması (v0.10.1059)', () => {
  it('üzerine gelinen kovanın saati + üç değer + ipucu satırı; tablo DIŞINDA', () => {
    act(() => {
      root.render(
        <table><tbody><tr><td>
          <TrendSpark calls={calls} errors={errors} p99={p99} width={160} fromMs={FROM} toMs={TO} hint="tıkla: grafik" />
        </td></tr></tbody></table>,
      );
    });
    const svg = host.querySelector('.trend-spark svg')!;
    sizeSvg(svg);
    move(svg, 80); // 30 çubuk: barIndexAt(80, 160, 30) = 15
    const ro = readout();
    expect(ro, 'okuma açılmadı').not.toBeNull();
    expect(ro!.getAttribute('role')).toBe('tooltip');
    expect(ro!.querySelector('.ov-tt-t')!.textContent).toBe('02.10.2026 21:30 – 21:32');
    const rows = [...ro!.querySelectorAll('.ov-tt-r')].map(r => r.textContent);
    // calls 130+131, errors 1+1, p99 max(40, 41)
    expect(rows).toEqual(['Calls261', 'Errors2', 'P9941ms']);
    expect(ro!.querySelector('.ov-tt-hint')!.textContent).toBe('tıkla: grafik');
    // Kırpılamaz: satırın/hücrenin torunu değil, body'nin çocuğu.
    expect(host.querySelector('tr')!.contains(ro)).toBe(false);
    expect(ro!.parentElement).toBe(document.body);
    // Eski hücre-içi okuma yok.
    expect(host.querySelector('.trend-spark__tt')).toBeNull();
    // İmleçle yürür: sonraki kova başka saat.
    move(svg, 150); // barIndexAt(150, 160, 30) = 28
    expect(readout()!.querySelector('.ov-tt-t')!.textContent).toBe('02.10.2026 21:56 – 21:58');
    expect(document.body.querySelectorAll('.spark-readout').length).toBe(1);
    leave(svg);
    expect(readout()).toBeNull();
  });
  it('dokunmatik işaretçi okumayı açmaz (dokunuş tıklamadır)', () => {
    act(() => { root.render(<TrendSpark calls={calls} fromMs={FROM} toMs={TO} />); });
    const svg = host.querySelector('.trend-spark svg')!;
    sizeSvg(svg);
    move(svg, 80, 'touch');
    expect(readout()).toBeNull();
  });
});

describe('OperationsTable trend hücresi (v0.10.1059)', () => {
  const op = (name: string, n: number): OperationRow => ({
    name, spanCount: n, errorCount: 2, errorRate: 0.1, avgDurationMs: 5, p50DurationMs: 4, p95DurationMs: 9,
    p99DurationMs: 15, apdex: 1, sparkline: calls, errorsSparkline: errors, p99Sparkline: p99,
  });
  function renderTable() {
    act(() => {
      root.render(
        <MemoryRouter>
          <QueryClientProvider client={new QueryClient()}>
            <OperationsTable service="demo-svc" rows={[op('GET /api/items', 9000), op('POST /api/orders', 4000)]}
              range={{ preset: 'custom', fromMs: FROM, toMs: TO }} normalized={false}
              onToggleNormalized={() => {}} loading={false} />
          </QueryClientProvider>
        </MemoryRouter>,
      );
    });
  }

  it('trend düğmesi ve ataları yerel title taşımaz; okuma tek ve satır dışında', () => {
    renderTable();
    const btn = host.querySelector<HTMLButtonElement>('button[aria-label="GET /api/items — trend grafiğini aç"]')!;
    expect(btn, 'trend düğmesi bulunamadı').not.toBeNull();
    for (let el: HTMLElement | null = btn; el && el !== host; el = el.parentElement) {
      expect(el.getAttribute('title') ?? '', `${el.tagName} title`).toBe('');
    }
    const svg = btn.querySelector('svg')!;
    expect(svg.querySelector('title')).toBeNull();
    sizeSvg(svg);
    move(svg, 80);
    const ro = readout()!;
    expect(ro.textContent).toContain('02.10.2026 21:30 – 21:32');
    expect(ro.textContent).toContain('tıkla: grafik');
    expect(btn.closest('tr')!.contains(ro)).toBe(false);
    expect(document.body.querySelectorAll('.spark-readout').length).toBe(1);
    leave(svg);
  });

  it('tık davranışı aynı: trend düğmesi detay panelini açar', () => {
    renderTable();
    const btn = host.querySelector<HTMLButtonElement>('button[aria-label="POST /api/orders — trend grafiğini aç"]')!;
    act(() => { btn.click(); });
    expect(host.textContent).toContain('kapat');
  });

  it('"All" satırı aynı okumayı alır, yerel <title> basmaz', () => {
    renderTable();
    const svg = host.querySelector('tr.agg-row svg')!;
    expect(svg.querySelector('title')).toBeNull();
    sizeSvg(svg);
    act(() => { svg.dispatchEvent(new MouseEvent('mousemove', { bubbles: true, clientX: 0 })); });
    const ro = readout()!;
    expect(ro, 'All satırı okuması açılmadı').not.toBeNull();
    expect(ro.querySelector('.ov-tt-t')!.textContent).toBe('02.10.2026 21:00 – 21:01');
    const rows = [...ro.querySelectorAll('.ov-tt-r')].map(r => r.textContent);
    expect(rows).toEqual(['Calls (2 op)200', 'Errors2', 'P99 (op maks.)10ms']);
    expect(svg.closest('tr')!.contains(ro)).toBe(false);
  });
});
