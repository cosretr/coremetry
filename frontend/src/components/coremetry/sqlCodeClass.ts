// sqlCodeClass — v0.10.1859: SQL belirteç türü → Tailwind renk sınıfı + SqlCode
// metinleri. components/coremetry altında çünkü Tailwind yalnız @source
// dizinlerini tarar (styles/index.css); lib/ taranmaz ve sınıf üretilmezdi.
// Renkler yalnız mevcut, metin kontrastı ölçülmüş tokenlar (yeni renk yok).
import type { SqlTokenKind } from '@/lib/sqlHighlight';

export const SQL_TOKEN_CLASS: Record<Exclude<SqlTokenKind, 'plain'>, string> = {
  kw: 'font-semibold text-[color:var(--accent2)]',
  fn: 'text-[color:var(--sig-trace)]',
  str: 'text-[color:var(--sig-metric)]',
  num: 'text-[color:var(--sig-log)]',
  param: 'text-[color:var(--sig-log)]',
  cmt: 'italic text-[color:var(--text3)]',
};

export const SQL_CODE_STRINGS = {
  copy: "SQL'i kopyala",
  copied: 'Kopyalandı',
} as const;
