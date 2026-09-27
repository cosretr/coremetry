// @vitest-environment jsdom
//
// PodContextTables.states — v0.10.967 (tablo standardı dilim 5, P-2).
//
// NE ÇİVİLİYOR: Konteynerler statik tablosu (≤ ~10 satır, dt yok)
//   • yüklenirken artık tablonun İÇİNDE iskelet (eskiden `pending` Spinner
//     erken dönüşü tabloyu hiç çizmiyordu); kaynak notu yalnız yanıt gelince;
//   • boşken standart DataTableState satırı (dilim 4'ün ara işaretlemesi
//     değil), entity kayıtlarındaki konteyner adları cümlede;
//   • okuma düştüyse tablo HATA satırı basar, "KSM serisi yok" (boş sonuç)
//     DEMEZ: Thanos hatası (sunucu 200 + `error`, liste her zaman boş —
//     internal/api/entities.go getEntityContainers) ve HTTP hatası (`error`
//     prop'u; Pod.tsx `ctrQ.error`) ikisi de;
//   • tazeleme düştüğü hâlde önbellekteki satırlar ekrandaysa şerit görünür
//     (eski liste sessizce "güncel" okunmasın); hata iki kez basılmaz.
import { describe, it, expect, afterEach } from 'vitest';
import { act, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { EntityContainersResponse, EntityRecord } from '@/lib/types';
import { PodContainersTable } from './PodContextTables';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const REC = (name: string): EntityRecord => ({
  type: 'container', clusterId: 'c1', id: `ctr:${name}`, name, source: 'thanos',
  validFrom: '2026-09-27T00:00:00Z', firstSeen: '2026-09-27T00:00:00Z', lastSeen: '2026-09-27T01:00:00Z',
});

const CTR: EntityContainersResponse = {
  entity: 'pod:c1/ns/api-0',
  containers: [
    { name: 'app', ready: true, readyKnown: true, restarts: 0 },
    { name: 'sidecar', ready: false, readyKnown: true, restarts: 3, lastTermReason: 'OOMKilled' },
  ],
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(node); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

const stateRow = (el: HTMLElement, kind: string) => el.querySelector<HTMLTableRowElement>(`tbody tr[data-dt-state="${kind}"]`);
const bodyRows = (el: HTMLElement) =>
  Array.from(el.querySelectorAll('tbody tr')).filter(tr => !tr.hasAttribute('data-dt-state'));

describe('PodContainersTable — durumlar tablonun içinde (P-2)', () => {
  it('yükleniyor: başlık durur, tek iskelet satırı colSpan = 5 <th>; kaynak notu ve rozet yok', () => {
    const el = mount(<PodContainersTable pending ctr={undefined} />);
    expect(el.querySelectorAll('thead th')).toHaveLength(5);
    const row = stateRow(el, 'loading');
    expect(row, 'yükleniyor tablonun içinde değil').not.toBeNull();
    expect(row!.querySelector('td')!.colSpan).toBe(5);
    expect(row!.querySelector('[role="status"][aria-busy="true"]')).not.toBeNull();
    expect(el.querySelectorAll('tbody tr')).toHaveLength(1);
    expect(el.textContent).not.toContain('GET /api/entity/containers');
    expect(el.textContent).not.toContain('Thanos: durum alınamadı');
  });

  it('boş: tek boş satır, entity kayıtlarındaki konteyner adlarıyla; kaynak notu durur', () => {
    const el = mount(<PodContainersTable pending={false} ctr={{ entity: 'x', containers: [] }}
      containerRecs={[REC('app'), REC('istio-proxy')]} />);
    const row = stateRow(el, 'empty');
    expect(row).not.toBeNull();
    expect(row!.querySelector('td')!.colSpan).toBe(5);
    expect(row!.textContent).toBe('KSM serisi yok · konteynerler: app, istio-proxy');
    expect(el.textContent).toContain('GET /api/entity/containers');
  });

  it('satırlar: durum satırı yok', () => {
    const el = mount(<PodContainersTable pending={false} ctr={CTR} />);
    expect(el.querySelector('tbody tr[data-dt-state]')).toBeNull();
    expect(bodyRows(el)).toHaveLength(2);
  });

  it('Thanos hatası (liste boş, sunucunun tek hata biçimi): hata satırı sunucu metniyle; "KSM serisi yok" yok, şerit yok', () => {
    const el = mount(<PodContainersTable pending={false} ctr={{ entity: 'x', containers: [], error: 'thanos 503' }}
      containerRecs={[REC('app')]} />);
    const row = stateRow(el, 'error');
    expect(row, 'Thanos hatası boş sonuç gibi çizildi').not.toBeNull();
    expect(row!.querySelector('td')!.colSpan).toBe(5);
    expect(row!.textContent).toContain('thanos 503');
    expect(stateRow(el, 'empty')).toBeNull();
    expect(el.textContent).not.toContain('KSM serisi yok');
    // Hata tablonun içinde; üstteki şerit ikinci kez söylemez.
    expect(el.textContent).not.toContain('Thanos: durum alınamadı');
    expect(el.querySelectorAll('tbody tr')).toHaveLength(1);
  });

  it('HTTP hatası, veri yok: hata satırı (varsayılan "boş sonuç değil" metni); "KSM serisi yok" yok', () => {
    const el = mount(<PodContainersTable pending={false} ctr={undefined} error={new Error('HTTP 502')} />);
    const row = stateRow(el, 'error');
    expect(row, 'düşen HTTP okuması boş sonuç gibi çizildi').not.toBeNull();
    expect(row!.querySelector('td')!.colSpan).toBe(5);
    expect(row!.textContent).toContain('boş sonuç değil');
    expect(el.textContent).not.toContain('KSM serisi yok');
  });

  it('HTTP tazeleme hatası + önbellekteki satırlar: satırlar durur, şerit hatayı söyler (sessiz eski liste yok)', () => {
    const el = mount(<PodContainersTable pending={false} ctr={CTR} error={new Error('HTTP 502')} />);
    expect(bodyRows(el)).toHaveLength(2);
    expect(el.querySelector('tbody tr[data-dt-state]')).toBeNull();
    expect(el.textContent).toContain('Konteyner durumu tazelenemedi');
    expect(el.querySelector('[title="HTTP 502"]')).not.toBeNull();
  });

  it('hata yokken şerit yok', () => {
    const el = mount(<PodContainersTable pending={false} ctr={CTR} />);
    expect(el.textContent).not.toContain('tazelenemedi');
    expect(el.textContent).not.toContain('Thanos: durum alınamadı');
  });
});
