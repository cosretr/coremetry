// @vitest-environment jsdom
//
// ServiceHealthcheck sözleşmesi — v0.10.2039 (servis sağlık kontrolü).
// "Ölçülemedi" ASLA "İyi" gibi çizilmez: kendi etiketi + gri; renk yalnız
// sapan satırda (Dikkat / Sorun); İyi satırı nötr (sınıfsız). Cümle tablonun
// ÜSTÜNDE ilk öğe.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { HealthcheckResultView, hcStateClass, HC_STATE_LABEL } from './ServiceHealthcheck';
import type { HealthcheckResult } from '@/lib/types';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

const data: HealthcheckResult = {
  service: 'orders-api', window: '15m', verdict: 'crit',
  sentence: 'orders-api: 1 sorun var: pod restart (cluster-a). 1 kontrol ölçülemedi.',
  rows: [
    { check: 'red', title: 'Hata oranı ve gecikme', state: 'ok', seen: 'hata oranı %0.40', link: '/service?name=orders-api', linkLabel: 'Servis özeti' },
    { check: 'pod_restarts', title: 'Pod restart ve OOM', subject: 'cluster-a', state: 'crit', seen: '1 pod’da 4 restart', evidence: 'increase(kube_pod_container_status_restarts_total[15m])' },
    { check: 'pod_resources', title: 'Pod CPU / bellek limiti', subject: 'cluster-a', state: 'unknown', seen: 'limit tanımlı değil' },
  ],
  counts: { ok: 1, warn: 0, crit: 1, unknown: 1 }, measuredAt: 0, partial: false, durationMs: 1200,
  notApplicable: ['Kafka consumer lag'],
};

describe('HealthcheckResultView', () => {
  it('cümle önce, dört sütun, Ölçülemedi kendi etiketiyle gri', () => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => { root!.render(<MemoryRouter><HealthcheckResultView data={data} /></MemoryRouter>); });
    const first = host.querySelector('p');
    expect(first?.textContent).toBe(data.sentence);
    const heads = [...host.querySelectorAll('th')].map(th => th.textContent);
    expect(heads).toEqual(['Kontrol', 'Durum', 'Ne görüldü', 'Nereye bakmalı']);
    const states = [...host.querySelectorAll('tbody tr')].map(tr => tr.children[1]);
    expect(states.map(s => s.textContent)).toEqual(['İyi', 'Sorun', 'Ölçülemedi']);
    expect(states[0].className).not.toMatch(/--err|--warn|--text3/);
    expect(states[1].className).toMatch(/--err/);
    expect(states[2].className).toMatch(/--text3/);
    expect(host.querySelector('tbody tr:nth-child(2) td')?.textContent).toBe('Pod restart ve OOM · cluster-a');
    expect(host.textContent).toContain('Uygulanmadı (bu serviste veri yok): Kafka consumer lag');
    expect(host.querySelector('details')).not.toBeNull();
  });

  it('durum sınıfları: yalnız sapma renkli', () => {
    expect(hcStateClass('ok')).toBe('');
    expect(hcStateClass('warn')).toMatch(/--warn/);
    expect(HC_STATE_LABEL.unknown).toBe('Ölçülemedi');
  });
});
