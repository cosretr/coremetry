import { describe, it, expect, afterEach, vi } from 'vitest';
import { getPickerRecents, recordPickerRecent, RECENT_CAP, SCOPE_CAP } from './pickerRecents';
import { STORAGE_KEYS } from './storage';

// v0.10.1089 — seçicilerin "Son kullanılan" deposu. localStorage burada
// (node ortamı) elle taklit ediliyor; depolama kapalı/bozuk/kotası dolu
// olsa da seçici ÇÖKMEMELİ — okuma boş, yazma sessiz.

function memoryStorage(): Storage {
  const m = new Map<string, string>();
  return {
    get length() { return m.size; },
    clear: () => m.clear(),
    getItem: k => (m.has(k) ? m.get(k)! : null),
    key: i => [...m.keys()][i] ?? null,
    removeItem: k => { m.delete(k); },
    setItem: (k, v) => { m.set(k, String(v)); },
  };
}

afterEach(() => { vi.unstubAllGlobals(); });

describe('pickerRecents', () => {
  it('yazılan son değer başa gelir, tekilleşir, 5’te kesilir', () => {
    vi.stubGlobal('localStorage', memoryStorage());
    for (const v of ['svc-a', 'svc-b', 'svc-c', 'svc-d', 'svc-e', 'svc-f']) recordPickerRecent('service', v);
    recordPickerRecent('service', 'svc-c');
    const got = getPickerRecents('service');
    expect(got).toHaveLength(RECENT_CAP);
    expect(got).toEqual(['svc-c', 'svc-f', 'svc-e', 'svc-d', 'svc-b']);
  });

  it('kapsamlar ayrı: operasyon servis başına', () => {
    vi.stubGlobal('localStorage', memoryStorage());
    recordPickerRecent('operation:svc-a', 'GET /alpha');
    recordPickerRecent('operation:svc-b', 'GET /beta');
    expect(getPickerRecents('operation:svc-a')).toEqual(['GET /alpha']);
    expect(getPickerRecents('operation:svc-b')).toEqual(['GET /beta']);
    expect(getPickerRecents('service')).toEqual([]);
  });

  it('kapsam tavanı: en uzun süredir dokunulmayan kapsam düşer', () => {
    vi.stubGlobal('localStorage', memoryStorage());
    for (let i = 0; i <= SCOPE_CAP; i++) recordPickerRecent(`metric:svc-${i}`, 'synthetic.metric');
    expect(getPickerRecents('metric:svc-0')).toEqual([]);
    expect(getPickerRecents(`metric:svc-${SCOPE_CAP}`)).toEqual(['synthetic.metric']);
  });

  it('bozuk / yanlış şekilli kayıt boş liste döner', () => {
    const s = memoryStorage();
    vi.stubGlobal('localStorage', s);
    s.setItem(STORAGE_KEYS.pickerRecents, '{bozuk');
    expect(getPickerRecents('service')).toEqual([]);
    s.setItem(STORAGE_KEYS.pickerRecents, JSON.stringify({ service: ['ok-svc', 42, null, ''] }));
    expect(getPickerRecents('service')).toEqual(['ok-svc']);
    s.setItem(STORAGE_KEYS.pickerRecents, JSON.stringify(['dizi']));
    expect(getPickerRecents('service')).toEqual([]);
  });

  it('depolama ATAN (özel pencere / kota) ortamda okuma boş, yazma sessiz', () => {
    const boom = () => { throw new Error('SecurityError'); };
    vi.stubGlobal('localStorage', {
      getItem: boom, setItem: boom, removeItem: boom, clear: boom, key: boom, length: 0,
    });
    expect(() => recordPickerRecent('service', 'svc-a')).not.toThrow();
    expect(getPickerRecents('service')).toEqual([]);
  });

  it('depolama HİÇ yokken de çökmez', () => {
    vi.stubGlobal('localStorage', undefined);
    expect(() => recordPickerRecent('service', 'svc-a')).not.toThrow();
    expect(getPickerRecents('service')).toEqual([]);
  });
});
