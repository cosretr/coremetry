// @vitest-environment jsdom
// crossSourceMapping.contract.test.tsx — v0.10.948 (CoSRE Faz B): çapraz-kaynak
// eşleme formları. Faz A backend'i ES alan haritasına cluster / namespace /
// pod / version, VM ayarına labelMap {service, env, cluster, namespace, pod,
// version} ekledi (ikisi de İŞARETÇİ sözleşme: anahtar yoksa saklı korunur,
// boş dize temizler → keşif). Form artık bu alanları BİLDİĞİ için:
//   (1) GET'teki saklı değer kutuya dolar (okuyamayan kutu ilgisiz her kayıtta
//       boş gönderip eşlemeyi silerdi — aiTuning sınıfı),
//   (2) PUT her kayıtta DÖRT / ALTI anahtarı da taşır (boş = keşfe dön),
//   (3) VM etiket adı MetricsQL dilbilgisine uymuyorsa kutu uyarır (karar yine
//       sunucuda: LabelMapProblem → 400).
// Adlar sentetik (cluster-a, prod). Ağ mock.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import type { ESLogstoreInput, ESLogstoreSnapshot, VMSettingsInput, VMSnapshot } from '@/lib/types';

const m = vi.hoisted(() => ({
  es: null as ESLogstoreSnapshot | null,
  vm: null as VMSnapshot | null,
  putES: [] as ESLogstoreInput[],
  putVM: [] as VMSettingsInput[],
}));
vi.mock('@/lib/api', () => ({
  api: {
    getLogstoreSettings: vi.fn(async () => m.es),
    putLogstoreSettings: vi.fn(async (s: ESLogstoreInput) => { m.putES.push(s); return m.es; }),
    testLogstoreSettings: vi.fn(async () => ({ ok: true })),
    getVMSettings: vi.fn(async () => m.vm),
    putVMSettings: vi.fn(async (s: VMSettingsInput) => { m.putVM.push(s); return { ...m.vm, labelMap: s.labelMap }; }),
    testVMSettings: vi.fn(async () => ({ ok: true })),
  },
}));

import { ElasticTab } from './ElasticTab';
import { MetricsBackendTab } from './MetricsBackendTab';

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(node: ReactNode): HTMLElement {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  act(() => { root!.render(node); });
  return host;
}
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); root = null; host = null; m.putES.length = 0; m.putVM.length = 0; });
const tick = async () => { await act(async () => { await new Promise(r => setTimeout(r, 20)); }); };
function setInput(el: HTMLInputElement, text: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  setter.call(el, text); el.dispatchEvent(new Event('input', { bubbles: true }));
}
// Etiket metninden input: ElasticTab `<label><div>Ad</div><input/></label>`,
// Field atomu `<label htmlFor>Ad</label><input id>`.
function input(el: HTMLElement, label: string): HTMLInputElement {
  for (const l of Array.from(el.querySelectorAll('label'))) {
    const own = l.querySelector('div')?.textContent ?? l.textContent;
    if (own?.trim() !== label) continue;
    const i = l.querySelector('input') ?? (l.htmlFor ? document.getElementById(l.htmlFor) : null);
    if (i) return i as HTMLInputElement;
  }
  throw new Error(`input yok: ${label}`);
}
async function submit(el: HTMLElement) {
  await act(async () => { el.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
  await tick();
}

describe('ElasticTab — cluster / namespace / pod / version alan eşlemesi', () => {
  it('saklı değerler dolar; PUT dört anahtarı da taşır (boş = keşfe dön)', async () => {
    m.es = {
      backend: 'elasticsearch', addresses: ['https://es-0:9200'], username: '', hasPassword: false, hasApiKey: false,
      insecureSkipVerify: false, index: 'app-*', indexTemplate: '', source: 'ui',
      fields: { timestamp: '@timestamp', cluster: 'resource_attributes.k8s.cluster.name', pod: 'kubernetes.pod_name' },
    };
    const el = render(<ElasticTab />);
    await tick();
    expect(input(el, 'Cluster').value).toBe('resource_attributes.k8s.cluster.name');
    expect(input(el, 'Pod').value).toBe('kubernetes.pod_name');
    expect(input(el, 'Namespace').value).toBe('');
    expect(input(el, 'Version').placeholder).toBe('empty = self-discover');
    expect(el.textContent).toContain('never silently');

    act(() => { setInput(input(el, 'Namespace'), 'kubernetes.namespace.name'); });
    act(() => { setInput(input(el, 'Pod'), ''); });
    await submit(el);

    expect(m.putES).toHaveLength(1);
    const f = m.putES[0].fields!;
    expect(f).toMatchObject({ cluster: 'resource_attributes.k8s.cluster.name', namespace: 'kubernetes.namespace.name', pod: '', version: '' });
    for (const k of ['cluster', 'namespace', 'pod', 'version']) expect(Object.prototype.hasOwnProperty.call(f, k), k).toBe(true);
    expect(f.timestamp).toBe('@timestamp'); // mevcut alanlar aynen
  });
});

describe('MetricsBackendTab — VM etiket eşlemesi (labelMap)', () => {
  const snap = (labelMap?: VMSnapshot['labelMap']): VMSnapshot => ({
    enabled: true, baseUrl: 'http://victoria-metrics:8428', hasToken: false, tokenResolved: false, writeEnabled: false, labelMap,
  });

  it('saklı eşleme dolar; PUT altı rolün tamamını gönderir', async () => {
    m.vm = snap({ env: 'deployment_environment', cluster: 'k8s_cluster_name' });
    const el = render(<MetricsBackendTab />);
    await tick();
    expect(el.textContent).toContain('Bağlam etiketleri (CoSRE)');
    expect(input(el, 'Ortam').value).toBe('deployment_environment');
    expect(input(el, 'Cluster').value).toBe('k8s_cluster_name');
    expect(input(el, 'Servis').placeholder).toContain('service_name');

    act(() => { setInput(input(el, 'Pod'), ' k8s_pod_name '); });
    await submit(el);

    expect(m.putVM).toHaveLength(1);
    expect(m.putVM[0].labelMap).toEqual({
      service: '', env: 'deployment_environment', cluster: 'k8s_cluster_name', namespace: '', pod: 'k8s_pod_name', version: '',
    });
  });

  it('noktalı ad uyarılır, doğru yazım önerilir; düzeltilince uyarı düşer', async () => {
    m.vm = snap({});
    const el = render(<MetricsBackendTab />);
    await tick();
    const pod = input(el, 'Pod');
    act(() => { setInput(pod, 'k8s.pod.name'); });
    expect(pod.getAttribute('aria-invalid')).toBe('true');
    expect(el.textContent).toContain('geçersiz etiket adı');
    expect(el.textContent).toContain('k8s_pod_name');
    act(() => { setInput(pod, 'k8s_pod_name'); });
    expect(pod.getAttribute('aria-invalid')).toBeNull();
  });

  it('eşlemesiz snapshot (hiç yapılandırılmamış): kutular boş, PUT boş rollerle', async () => {
    m.vm = snap(undefined);
    const el = render(<MetricsBackendTab />);
    await tick();
    expect(input(el, 'Namespace').value).toBe('');
    await submit(el);
    expect(m.putVM[0].labelMap).toEqual({ service: '', env: '', cluster: '', namespace: '', pod: '', version: '' });
  });
});
