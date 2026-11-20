// urlSchema.ts — 2026-11-21: tipli, zod-doğrulamalı URL param codec'i.
//
// lib/urlParams'ın ParamCodec sözleşmesinin (değer URL'den TÜRETİLİR, yazım
// yabancı paramları korur, varsayılan = param silinir, replace:true) üstüne
// şema katmanı. Elle yazılan `Number(sp.get('x') ?? 0) || 0` /
// `RUNGS.includes(raw)` ayrıştırmaları yerine TEK beyan:
//
//   const SPEC = urlSpec({
//     id:    zParam(z.string().check(z.maxLength(512)), ''),
//     at:    zParam(zInt({ min: 0 }), 0),
//     limit: zParam(zOneOf([100, 500, 1000]), 100),
//   });
//   const [{ id, at, limit }, set] = useUrlParams(SPEC);
//
// Kurallar:
//   - decode: param yok ya da şemadan geçmiyor → varsayılan (bozuk / elle
//     kurcalanmış link sayfayı kırmaz, sessizce varsayılana düşer);
//   - encode: varsayılana eşit → '' (param silinir); kodlanmış metin
//     şemadan geçmiyorsa da '' — URL'e GEÇERSİZ değer asla yazılmaz
//     (sunucu cache anahtarına sınırsız kardinalite taşınmaz, v0.8.270);
//   - şema HAM METNİ (string) doğrular; sayılar `zInt` / `zOneOf` gibi
//     metin→sayı borularıyla gelir.
// `zod/mini` kullanılır (ağaç-sallanabilir; tam `zod` ~4× büyük) ve bu
// modül yalnız tembel sayfalardan import edilir — ilk JS bütçesine girmez.
import * as z from 'zod/mini';
import type { ParamCodec, ParamSpec } from './urlParams';

export { useUrlParams, readUrlParams, writeUrlParams } from './urlParams';
export { z };

/** Ham URL metnini doğrulayan şema (girdi her zaman string). */
export type UrlSchema<T> = z.ZodMiniType<T, string>;

export function zParam<T>(schema: UrlSchema<T>, def: T, encode: (v: T) => string = v => String(v)): ParamCodec<T> {
  return {
    decode: raw => {
      if (raw === null) return def;
      const r = schema.safeParse(raw);
      return r.success ? r.data : def;
    },
    encode: v => {
      if (Object.is(v, def)) return '';
      const s = encode(v);
      return schema.safeParse(s).success ? s : '';
    },
  };
}

/** Tamsayı (ondalık rakamlar, ≤15 hane); isteğe bağlı alt/üst sınır. */
export function zInt(opts: { min?: number; max?: number } = {}): UrlSchema<number> {
  return z.pipe(
    z.string().check(z.regex(/^-?\d{1,15}$/)),
    z.transform(Number),
  ).check(z.refine(n => (opts.min === undefined || n >= opts.min) && (opts.max === undefined || n <= opts.max)));
}

/** Sabit sayı basamakları (limit/örneklem runglar); küme dışı → geçersiz. */
export function zOneOf<const T extends number>(values: readonly T[]): UrlSchema<T> {
  return z.pipe(zInt(), z.transform(n => n as T))
    .check(z.refine(n => (values as readonly number[]).includes(n)));
}

/** Sınırlı uzunlukta kırpılmış metin (boş → geçersiz, varsayılana düşer). */
export function zText(max = 512): UrlSchema<string> {
  return z.pipe(z.string(), z.transform(s => s.trim()))
    .check(z.minLength(1), z.maxLength(max));
}

/** Spec'i tipli kurar; modül düzeyinde bir kez çağrılmalı (sabit kimlik). */
export function urlSpec<V>(spec: ParamSpec<V>): ParamSpec<V> {
  return spec;
}
