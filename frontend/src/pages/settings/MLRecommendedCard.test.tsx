// @vitest-environment jsdom
//
// MLRecommendedCard — v0.10.1886: ölçüm YALNIZ düğmeyle koşar (açılışta istek
// yok); sonuç düz cümle + var / bulunamadı / açılacak / kapanacak; "Uygula"
// önerilen ayarı PUT eder ve üst bileşene verir; fark yoksa "Uygula" çıkmaz.
import { describe, it, expect, afterEach, vi, beforeEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { MLConfig, MLRecommended } from '@/lib/types';

const getMlRecommended = vi.fn();
const putMl = vi.fn();
vi.mock('@/lib/api', () => ({ api: { getMlRecommended: () => getMlRecommended(), putMl: (c: unknown) => putMl(c) } }));

import { MLRecommendedCard } from './MLRecommendedCard';

let root: Root | null = null;
let host: HTMLDivElement | null = null;
const recCfg = { enabled: true, detectors: { op_latency: true } } as unknown as MLConfig;
const rec: MLRecommended = {
  vmConfigured: true, config: recCfg,
  checks: [
    { id: 'http_server', label: 'HTTP sunucu', checked: true, present: true },
    { id: 'grpc_server', label: 'gRPC sunucu', checked: true, present: true },
    { id: 'metric_kafka', label: 'Kafka', checked: true, present: true },
    { id: 'metric_jvm', label: 'JVM', checked: true, present: false },
  ],
  changes: [
    { key: 'op_latency', from: false, to: true, reason: '' },
    { key: 'metric_jvm', from: true, to: false, reason: '' },
  ],
};
const label = (d: string) => ({ op_latency: 'Endpoint gecikmesi', metric_jvm: 'JVM' }[d] ?? d);
const flush = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });
const btn = (text: string) => [...host!.querySelectorAll('button')].find(b => b.textContent?.trim() === text) as HTMLButtonElement | undefined;

function mount(onApplied = vi.fn()) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(<MLRecommendedCard labelOf={label} onApplied={onApplied} />));
  return onApplied;
}

beforeEach(() => { getMlRecommended.mockReset(); putMl.mockReset(); });
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; });

describe('MLRecommendedCard', () => {
  it('açılışta ölçüm isteği atmaz; düğmeyle ölçer ve düz dille gösterir', async () => {
    getMlRecommended.mockResolvedValue(rec);
    mount();
    expect(getMlRecommended).not.toHaveBeenCalled();
    expect(btn('Uygula')).toBeUndefined();
    await act(async () => { btn('Önerilen kurulumu göster')!.click(); });
    await flush();
    const text = host!.textContent ?? '';
    expect(text).toContain('Önerilen kurulum 1 dedektörü açar ve 1 dedektörü kapatır.');
    expect(text).toContain("VictoriaMetrics'te var: HTTP sunucu, gRPC sunucu ve Kafka.");
    expect(text).toContain('Bulunamadı: JVM.');
    expect(text).toContain('Açılacak: Endpoint gecikmesi.');
    expect(text).toContain('Kapanacak: JVM.');
  });

  it('"Uygula" önerilen ayarı kaydeder ve üst bileşene verir', async () => {
    getMlRecommended.mockResolvedValue(rec);
    putMl.mockResolvedValue(recCfg);
    const onApplied = mount();
    await act(async () => { btn('Önerilen kurulumu göster')!.click(); });
    await flush();
    await act(async () => { btn('Uygula')!.click(); });
    await flush();
    expect(putMl).toHaveBeenCalledWith(recCfg);
    expect(onApplied).toHaveBeenCalledWith(recCfg);
    expect(host!.textContent).toContain('Önerilen kurulum uygulandı');
    expect(btn('Uygula')).toBeUndefined();
  });

  it('fark yoksa "Uygula" çıkmaz', async () => {
    getMlRecommended.mockResolvedValue({ ...rec, changes: [] });
    mount();
    await act(async () => { btn('Önerilen kurulumu göster')!.click(); });
    await flush();
    expect(host!.textContent).toContain('Bugünkü ayar önerilen kurulumla aynı');
    expect(btn('Uygula')).toBeUndefined();
    expect(putMl).not.toHaveBeenCalled();
  });
});
