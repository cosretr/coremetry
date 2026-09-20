// @vitest-environment jsdom
//
// sortIdValidation.test.tsx — v0.10.831.
//
// İKİ sözleşme aynı anda yaşamak zorunda:
//
//  A) /endpoints sözleşmesi (Endpoints.tsx:431-434): hook'a YALNIZ görünür
//     kolonlar veriliyor ve GİZLENMİŞ bir kolona göre sıralama ÇALIŞMAYA
//     DEVAM EDİYOR ("serverSort forwards the persisted sort id to the fetch
//     regardless of visibility"). Yani kolon listesi bir "geçerli kimlikler"
//     kümesi DEĞİL — ondan kimlik TÜRETEN bir doğrulama bu sayfayı kırar.
//
//  B) /traces derdi (operatör-bildirimli, v0.10.831): bayat bir
//     `?s_traces-list=<bilinmeyen>` tablonun sıralama durumu oluyor, hiçbir
//     başlık aktif görünmüyor ve sunucu kendi varsayılanıyla çekiyor —
//     paylaşılan link sıralama VAAT edip tutmuyor.
//
// Uzlaşma: doğrulama OPT-IN. Kümeyi yalnız onu BİLEN sayfa verir (`sortIds`);
// verilmeyen tablolar bayt bayt eski davranışta kalır.
//
// Üçüncü sözleşme (kalıcılık): localStorage operatörün BU TABLODA yaptığı
// seçimi saklar. Mount'ta yazmak, reddedilen ya da URL'den gelen bir sırayı
// operatörün kişisel varsayılanının ÜZERİNE yazıyordu — self-heal yalnız URL
// için vardı, localStorage için yoktu.
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';

// jsdom'un localStorage'ı bu pakette KISMİ (setItem yok; tracesSortDefault.
// test.ts aynı notu taşıyor) — depo modülü bellek içi bir haritayla
// mock'lanıyor. DataTable.tsx aynı modülü kullandığı için okuma/yazma yolu
// birebir sınanıyor.
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

import { useDataTable, DataTableHead, type DataTable, type ColumnDef } from './index';
import { getItem, setItem, dtSortKey } from '@/lib/storage';
import type { SortState } from '@/lib/dataTable';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface Row { id: string; name: string; n: number }
const ROWS: Row[] = [{ id: 'a', name: 'alpha', n: 3 }, { id: 'b', name: 'beta', n: 1 }];
// Sayfanın BİLDİRDİĞİ küme; `hidden` kolonu operatör gizlemiş olabilir ve o
// hâlde hook'a HİÇ verilmez (Endpoints deseni).
const ALL: ColumnDef<Row>[] = [
  { id: 'name', label: 'Name', sortValue: r => r.name, width: 120 },
  { id: 'n', label: 'N', sortValue: r => r.n, numeric: true, width: 80 },
  { id: 'hidden', label: 'Hidden', sortValue: r => r.n, width: 80 },
];
const VISIBLE = ALL.filter(c => c.id !== 'hidden');
const KEY = 'sortid-test';
const INITIAL: SortState = { id: 'name', dir: 'asc' };

let root: Root | null = null;
let host: HTMLDivElement | null = null;
let last: DataTable<Row> | null = null;

function Probe({ cols, sortIds }: { cols: ColumnDef<Row>[]; sortIds?: readonly string[] }) {
  const dt = useDataTable<Row>({
    storageKey: KEY, columns: cols, rows: ROWS, serverSort: true,
    initialSort: INITIAL, sortIds,
  });
  last = dt;
  return <table><DataTableHead dt={dt} /><tbody /></table>;
}

function render(url: string, cols: ColumnDef<Row>[], sortIds?: readonly string[]) {
  window.history.replaceState(null, '', url);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<BrowserRouter><Probe cols={cols} sortIds={sortIds} /></BrowserRouter>); });
}

beforeEach(() => { mem.clear(); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null; last = null;
  document.body.innerHTML = '';
});

const urlSort = () => new URLSearchParams(window.location.search).get(`s_${KEY}`);

describe('sortIds VERİLMEDİĞİNDE doğrulama YOK (/endpoints sözleşmesi)', () => {
  it('gizlenmiş kolona göre sıralama YAŞAR — kolon listesi kimlik kümesi değildir', () => {
    render(`/x?s_${KEY}=hidden.desc`, VISIBLE);
    expect(last!.sort).toEqual({ id: 'hidden', dir: 'desc' });
  });

  it('localStorage’daki gizli-kolon sıralaması da yaşar', () => {
    setItem(dtSortKey(KEY), { id: 'hidden', dir: 'asc' });
    render('/x', VISIBLE);
    expect(last!.sort).toEqual({ id: 'hidden', dir: 'asc' });
  });
});

describe('sortIds VERİLDİĞİNDE bayat kimlik yok sayılır (/traces derdi)', () => {
  const IDS = ['name', 'n'] as const;

  it('bilinmeyen URL kimliği sayfanın kendi sırasına düşer', () => {
    render(`/x?s_${KEY}=startTime.desc`, ALL, IDS);
    expect(last!.sort).toEqual(INITIAL);
    // Başlık da doğruyu gösterir: aktif ok sayfanın sırasında.
    const active = [...host!.querySelectorAll('th')]
      .filter(th => (th.getAttribute('aria-sort') ?? 'none') !== 'none')
      .map(th => th.textContent?.replace(/[▲▼↕]/g, '').trim());
    expect(active).toEqual(['Name']);
  });

  // İnceleme kararı (2026-09-20): reddedilen parametre URL'de KALIR. Onu
  // yeniden yazmak her bayat linkte fazladan bir URL yazımıydı ve görünen
  // hiçbir şeyi düzeltmiyordu — durum ve başlık okları zaten etkin sıralamayı
  // gösteriyor. Bu test o kararı çiviliyor (kimse "temizlik" diye geri
  // eklemesin).
  it('reddedilen parametre URL’de KALIR — churn yok', () => {
    render(`/x?s_${KEY}=startTime.desc&range=3h`, ALL, IDS);
    expect(urlSort()).toBe('startTime.desc');
    expect(new URLSearchParams(window.location.search).get('range')).toBe('3h');
    expect(last!.sort).toEqual(INITIAL);   // durum yine de doğru
  });

  it('GEÇERLİ kimlik kazanır (paylaşılan link bozulmadı)', () => {
    render(`/x?s_${KEY}=n.desc`, ALL, IDS);
    expect(last!.sort).toEqual({ id: 'n', dir: 'desc' });
    expect(urlSort()).toBe('n.desc');
  });
});

describe('localStorage yalnız OPERATÖR eyleminde yazılır', () => {
  it('mount HİÇBİR şey yazmaz (URL’den gelen sıra kişisel varsayılanı EZMEZ)', () => {
    setItem(dtSortKey(KEY), { id: 'n', dir: 'desc' });
    render(`/x?s_${KEY}=name.asc`, ALL, ['name', 'n']);
    expect(last!.sort).toEqual({ id: 'name', dir: 'asc' });   // link kazanır (görünüm)
    expect(getItem<SortState | null>(dtSortKey(KEY), null))    // ama kayıt DOKUNULMAZ
      .toEqual({ id: 'n', dir: 'desc' });
  });

  it('reddedilen kimlik kaydı EZMEZ', () => {
    setItem(dtSortKey(KEY), { id: 'n', dir: 'desc' });
    render(`/x?s_${KEY}=startTime.desc`, ALL, ['name', 'n']);
    expect(getItem<SortState | null>(dtSortKey(KEY), null)).toEqual({ id: 'n', dir: 'desc' });
  });

  it('başlık tıklaması YAZAR', () => {
    render('/x', ALL, ['name', 'n']);
    // Dikkat: 'Name' de 'N' ile başlıyor — başlık METNİ okla birlikte gelir,
    // o yüzden ok soyulup TAM eşleşme aranıyor.
    const nTh = [...host!.querySelectorAll('th')]
      .find(t => (t.textContent ?? '').replace(/[▲▼↕]/g, '').trim() === 'N')!;
    act(() => { nTh.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); });
    expect(getItem<SortState | null>(dtSortKey(KEY), null)).toEqual({ id: 'n', dir: 'desc' });
  });
});
