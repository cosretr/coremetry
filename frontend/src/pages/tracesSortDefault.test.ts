import { describe, it, expect, beforeEach, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.669 — operatör (prod): "Traces sayfası start time desc sıralı olsun".
// Kök neden: sıralama önceliği URL > localStorage > initialSort; bir kez
// başlığa tıklanınca tercih localStorage'a yazılıyor ve her yeni ziyaret onu
// açıyordu. persistSort:false ile localStorage basamağı atlanır (URL yine
// kazanır).
//
// Bu test ortamında gerçek localStorage YOK (aggSort.test.ts notu: kısmî
// stub / opak origin, yazma sessizce düşer) — storage modülü bellek içi bir
// haritayla mock'lanır; DataTable.tsx aynı modülü kullandığı için okuma
// yolu birebir sınanır.
const mem = new Map<string, string>();
vi.mock('@/lib/storage', async (importOriginal) => {
  const orig = await importOriginal<typeof import('@/lib/storage')>();
  return {
    ...orig,
    getRaw: (k: string) => mem.get(k) ?? null,
    setRaw: (k: string, v: string) => { mem.set(k, v); },
    removeRaw: (k: string) => { mem.delete(k); },
    getItem: <T,>(k: string, fb: T): T => {
      const r = mem.get(k);
      if (r == null) return fb;
      try { return JSON.parse(r) as T; } catch { return fb; }
    },
    setItem: <T,>(k: string, v: T) => { mem.set(k, JSON.stringify(v)); },
  };
});

import { resolveInitialSort } from '@/components/ui/DataTable';
import { setItem, dtSortKey } from '@/lib/storage';

const KEY = 'traces-list-test';
const TIME = { id: 'time', dir: 'desc' as const };

describe('resolveInitialSort persist=false (v0.10.669)', () => {
  beforeEach(() => mem.clear());

  it('localStorage daki eski tıklama persist=true ile kazanır, persist=false ile yok sayılır', () => {
    setItem(dtSortKey(KEY), { id: 'duration', dir: 'desc' });
    expect(resolveInitialSort(KEY, null, TIME, null, true)).toEqual({ id: 'duration', dir: 'desc' });
    expect(resolveInitialSort(KEY, null, TIME, null, false)).toEqual(TIME);
  });

  it('URL s_ parametresi persist=false ile de kazanır (paylaşılan link)', () => {
    setItem(dtSortKey(KEY), { id: 'duration', dir: 'desc' });
    expect(resolveInitialSort(KEY, 'spans.asc', TIME, null, false)).toEqual({ id: 'spans', dir: 'asc' });
  });

  it('varsayılan persist=true (imza geriye uyumlu: diğer tablolar aynen)', () => {
    setItem(dtSortKey(KEY), { id: 'status', dir: 'asc' });
    expect(resolveInitialSort(KEY, null, TIME)).toEqual({ id: 'status', dir: 'asc' });
  });
});

// v0.10.831 — bayat kimlik doğrulaması. Öncelik zinciri aynı (URL > köprü >
// localStorage > initialSort); değişen tek şey her basamağın kimliğinin
// TANINMASI şartı. Tanınmayan değer bir SONRAKİ basamağa düşer, en dipte
// sayfanın kendi sırası durur — tablo asla "sırasız" görünmez.
describe('resolveInitialSort — tanınmayan kolon kimliği (v0.10.831)', () => {
  beforeEach(() => mem.clear());
  const IDS = ['time', 'duration', 'spans', 'status', 'service', 'operation'];

  it('bayat URL kimliği yok sayılır → sayfanın kendi sırası', () => {
    expect(resolveInitialSort(KEY, 'startTime.desc', TIME, null, false, IDS)).toEqual(TIME);
  });

  it('bayat URL kimliği varken localStorage basamağı hâlâ işler (persist=true)', () => {
    setItem(dtSortKey(KEY), { id: 'duration', dir: 'asc' });
    expect(resolveInitialSort(KEY, 'startTime.desc', TIME, null, true, IDS))
      .toEqual({ id: 'duration', dir: 'asc' });
  });

  it('bayat localStorage kimliği de yok sayılır (eski kolon silinmiş olabilir)', () => {
    setItem(dtSortKey(KEY), { id: 'silinmis_kolon', dir: 'asc' });
    expect(resolveInitialSort(KEY, null, TIME, null, true, IDS)).toEqual(TIME);
  });

  it('bayat köprü (urlSortFallback) kimliği yok sayılır', () => {
    expect(resolveInitialSort(KEY, null, TIME, { id: 'eskiSema', dir: 'asc' }, false, IDS)).toEqual(TIME);
  });

  it('GEÇERLİ kimlik her basamakta kazanır (paylaşılan link bozulmadı)', () => {
    expect(resolveInitialSort(KEY, 'spans.asc', TIME, null, false, IDS)).toEqual({ id: 'spans', dir: 'asc' });
    expect(resolveInitialSort(KEY, null, TIME, { id: 'status', dir: 'desc' }, false, IDS))
      .toEqual({ id: 'status', dir: 'desc' });
  });

  it('küme VERİLMEZSE imza geriye uyumlu: doğrulama yapılmaz', () => {
    expect(resolveInitialSort(KEY, 'startTime.desc', TIME, null, false))
      .toEqual({ id: 'startTime', dir: 'desc' });
  });
});

describe('BAĞLANMA (Traces.tsx)', () => {
  const src = readFileSync(resolve(__dirname, 'Traces.tsx'), 'utf8');
  it('liste tablosu persistSort:false ile kurulur; varsayılan time/desc', () => {
    const i = src.indexOf("storageKey: 'traces-list'");
    expect(i).toBeGreaterThan(-1);
    const block = src.slice(i, i + 900);
    expect(block).toContain('persistSort: false');
    expect(block).toContain('initialSort: { id: sort, dir: order }');
    expect(src).toContain("(searchParams.get('sort') as SortColumn) || 'time'");
    expect(src).toContain("searchParams.get('order') === 'asc' ? 'asc' : 'desc'");
  });
  it('useDataTable persistSort:false localStorage a yazmaz', () => {
    const dt = readFileSync(resolve(__dirname, '../components/ui/DataTable/DataTable.tsx'), 'utf8');
    expect(dt).toContain('if (persistSort) setItem(sortLSKey, s);');
    // v0.10.831 — yazım artık bir EFEKT değil: `sort` her değiştiğinde (MOUNT
    // dahil) yazmak, gelen bir linkin sırasını ve doğrulamayla reddedilen bir
    // kimlikten düşülen fallback'i operatörün KİŞİSEL varsayılanının üzerine
    // yazıyordu. Tek yazıcı `setSort` (gerçek bir eylem); davranış ölçümü
    // components/ui/DataTable/sortIdValidation.test.tsx.
    expect(dt).not.toMatch(/useEffect\(\(\) => \{\s*if \(persistSort\)/);
    expect(dt).toContain('persist(s); // v0.10.831');
  });
});
