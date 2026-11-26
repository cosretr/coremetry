// @vitest-environment jsdom
//
// v0.10.2473 — "Köken: <varlık> — <gerekçe>": dört kuralın düz cümlesi (TR +
// EN), köken yok / belirsiz / tanınmayan kural → etiket yok; bağlantı entity
// kimliğinden; detay başlığı aynı satırı çizer.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import type { ProblemStandard, StdOrigin } from '@/lib/types';
import { stdOriginView } from '@/lib/problemStandardView';

const m = vi.hoisted(() => ({ lang: 'tr' as 'en' | 'tr' }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
import { StdOriginLine } from './StdOriginLine';
import { ProblemStandardBlock } from './ProblemStandardHeader';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ORIGIN: StdOrigin = { entity: 'svc:payments', type: 'service', name: 'payments', rule: 'K-DEEP', confidence: 'weak', entities: 3 };

function std(origin?: Partial<StdOrigin> | null): ProblemStandard {
  return {
    family: 'error', kind: 'anomaly_auto', subject: 'checkout',
    headline: 'checkout servisinde hata oranı arttı.',
    impact: { primary: { text: 'x', value: 0.05, normal: 0.01 }, known: true },
    since: { startedAt: 0, durationSec: 600, ongoing: true },
    evidence: { sentence: 's', source: 'vm', sampled: false },
    confidence: { level: 'high', gates: [] },
    nextStep: { text: 'n' },
    ...(origin === null ? {} : { origin: { ...ORIGIN, ...origin } as StdOrigin }),
  };
}

describe('stdOriginView', () => {
  it('reason in plain words per rule (TR + EN)', () => {
    const tr: Record<string, string> = {
      'K-ROLLOUT': 'deploy sonrası başladı', 'K-SHARED': 'diğerlerinin ortak bağımlılığı',
      'K-DEEP': 'çağrı zincirinde en derinde', 'K-FIRST': 'ilk bozulan',
    };
    const en: Record<string, string> = {
      'K-ROLLOUT': 'started after a deploy', 'K-SHARED': 'shared dependency of the others',
      'K-DEEP': 'deepest in the call chain', 'K-FIRST': 'first to break',
    };
    for (const rule of Object.keys(tr)) {
      const s = std({ rule: rule as StdOrigin['rule'] });
      expect(stdOriginView(s, 'tr')).toMatchObject({ label: 'Köken', name: 'payments', reason: tr[rule] });
      expect(stdOriginView(s, 'en')).toMatchObject({ label: 'Origin', name: 'payments', reason: en[rule] });
    }
  });
  it('no origin / unclear / unknown rule / single entity / no id → no label', () => {
    expect(stdOriginView(std(null))).toBeNull();
    expect(stdOriginView(undefined)).toBeNull();
    expect(stdOriginView(std({ confidence: 'unclear' as StdOrigin['confidence'] }))).toBeNull();
    expect(stdOriginView(std({ rule: 'K-OTHER' as StdOrigin['rule'] }))).toBeNull();
    expect(stdOriginView(std({ entities: 1 }))).toBeNull();
    expect(stdOriginView(std({ entity: '' }))).toBeNull();
  });
});

let root: Root | null = null;
let host: HTMLElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.lang = 'tr'; });
function mount(node: React.ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host;
}

describe('StdOriginLine', () => {
  it('links the origin by entity id, never by name', () => {
    const d = mount(<StdOriginLine std={std({ entity: 'db:orders-db', type: 'database', name: 'orders-db', rule: 'K-SHARED' })} />);
    const line = d.querySelector('[data-testid="std-origin"]')!;
    expect(line.textContent).toBe('Köken: orders-db — diğerlerinin ortak bağımlılığı');
    expect(line.querySelector('a')?.getAttribute('href')).toBe('/entity?id=db%3Aorders-db');
  });
  it('EN wording', () => {
    m.lang = 'en';
    const d = mount(<StdOriginLine std={std({ rule: 'K-ROLLOUT' })} />);
    expect(d.textContent).toBe('Origin: payments — started after a deploy');
  });
  it('renders nothing without origin', () => {
    expect(mount(<StdOriginLine std={std(null)} />).innerHTML).toBe('');
  });
  it('detail header shows the same line; facts-only view does not', () => {
    const d = mount(<ProblemStandardBlock std={std({ rule: 'K-FIRST' })} />);
    expect(d.querySelector('[data-testid="std-origin"]')?.textContent).toBe('Köken: payments — ilk bozulan');
    act(() => root?.unmount()); host?.remove();
    const f = mount(<ProblemStandardBlock std={std({ rule: 'K-FIRST' })} factsOnly />);
    expect(f.querySelector('[data-testid="std-origin"]')).toBeNull();
    const n = (act(() => root?.unmount()), host?.remove(), mount(<ProblemStandardBlock std={std(null)} />));
    expect(n.querySelector('[data-testid="std-origin"]')).toBeNull();
  });
});
