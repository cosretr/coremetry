// urlParams.ts — v0.10.1229 (paylaşılabilir URL programı, Faz 1-A).
//
// URL ↔ state için TEK yardımcı. Tasarım kararı: değerler URL'den TÜRETİLİR
// (useMemo(searchParams)), yerel kopya tutulmaz. Tek-yön okuma sınıfı
// (v0.8.253/256/265/267, v0.10.1216/1218/1220) "mount'ta kopyala, sonra
// unut" kalıbından doğuyordu; kopya yoksa geri/ileri, kayıtlı görünüm ve
// derin link kendiliğinden uygulanır, sig-guard'a gerek kalmaz.
// Yazımlar geçerli URL'i kopyalar (yabancı paramlar korunur), varsayılana eşit ya da
// boş değer paramı SİLER (URL temiz kalır), replace:true (geçmiş şişmez).
// Debounce'lu metin kutusu gibi taslaklar sayfada yerel kalır; commit edilen
// değer buradan yazılır.
import { useCallback, useMemo } from 'react';
import { useSearchParams } from 'react-router';

export interface ParamCodec<T> {
  decode: (raw: string | null) => T;
  /** '' → param silinir. */
  encode: (v: T) => string;
}

export type ParamSpec<V> = { [K in keyof V]: ParamCodec<V[K]> };

export const strParam = (def = ''): ParamCodec<string> => ({
  decode: raw => (raw ?? '').trim() || def,
  encode: v => (v.trim() === def ? '' : v.trim()),
});

export const boolParam = (def = false): ParamCodec<boolean> => ({
  decode: raw => (raw === null ? def : raw === '1' || raw === 'true'),
  encode: v => (v === def ? '' : v ? '1' : '0'),
});

/** Negatif olmayan tamsayı; bozuk değer varsayılana düşer. */
export const intParam = (def = 0): ParamCodec<number> => ({
  decode: raw => (raw !== null && /^\d{1,9}$/.test(raw) ? Number(raw) : def),
  encode: v => (v === def || !Number.isFinite(v) || v < 0 ? '' : String(Math.floor(v))),
});

/** Sabit sayı kümesi (ör. örneklem 50/100/200/500); küme dışı → varsayılan. */
export function intOneOfParam(values: readonly number[], def: number): ParamCodec<number> {
  return {
    decode: raw => (raw !== null && /^\d{1,9}$/.test(raw) && values.includes(Number(raw)) ? Number(raw) : def),
    encode: v => (v === def || !values.includes(v) ? '' : String(v)),
  };
}

/** Sabit küme; küme dışı değer varsayılana düşer. */
export function enumParam<T extends string>(values: readonly T[], def: T): ParamCodec<T> {
  return {
    decode: raw => (raw !== null && (values as readonly string[]).includes(raw) ? (raw as T) : def),
    encode: v => (v === def ? '' : v),
  };
}

/** Virgüllü liste (boş öğeler atılır, sıra korunur). */
export const csvParam = (): ParamCodec<string[]> => ({
  decode: raw => (raw ?? '').split(',').map(s => s.trim()).filter(Boolean),
  encode: v => v.map(s => s.trim()).filter(Boolean).join(','),
});

/** SAF: URL → değerler. */
export function readUrlParams<V>(sp: URLSearchParams, spec: ParamSpec<V>): V {
  const out = {} as V;
  for (const k of Object.keys(spec) as Array<keyof V>) {
    out[k] = spec[k].decode(sp.get(k as string));
  }
  return out;
}

/** SAF: prev + yama → yeni params (yabancılar korunur, '' siler). */
export function writeUrlParams<V>(prev: URLSearchParams, spec: ParamSpec<V>, patch: Partial<V>): URLSearchParams {
  const next = new URLSearchParams(prev);
  for (const k of Object.keys(patch) as Array<keyof V>) {
    const codec = spec[k];
    if (!codec) continue;
    const enc = codec.encode(patch[k] as V[typeof k]);
    if (enc === '') next.delete(k as string); else next.set(k as string, enc);
  }
  return next;
}

/**
 * useUrlParams — spec'teki paramlar URL'den canlı; set(patch) tek yazım.
 * spec modül düzeyinde sabit olmalı (her render yeni nesne → gereksiz memo).
 */
export function useUrlParams<V>(spec: ParamSpec<V>): [V, (patch: Partial<V>) => void] {
  const [sp, setSp] = useSearchParams();
  const values = useMemo(() => readUrlParams(sp, spec), [sp, spec]);
  // Taban window.location.search, router'ın `prev`i DEĞİL: aynı tikteki
  // ardışık yazımlar (ör. sayfa sıfırlama + filtre) ve ham yazıcılar
  // (useUrlPage) arasında `prev` bayat kalabiliyor ve birbirini geri alıyordu
  // (useUrlRange / useUrlPage aynı gerekçe).
  const set = useCallback((patch: Partial<V>) => {
    setSp(() => writeUrlParams(new URLSearchParams(window.location.search), spec, patch), { replace: true });
  }, [setSp, spec]);
  return [values, set];
}
