// signalKindClass — v0.10.1844: sinyal türü → Tailwind renk sınıfı.
// components/coremetry altında çünkü Tailwind yalnız @source dizinlerini
// tarar (styles/index.css); lib/ taranmaz ve sınıf üretilmezdi. Değer
// lib/signalKind.ts SIGNAL_KINDS[k].token ile aynı değişken (test pinler).
import type { SignalKind } from '@/lib/signalKind';

export const SIGNAL_KIND_CLASS: Record<SignalKind, string> = {
  metric: 'text-[color:var(--sig-metric)]',
  trace: 'text-[color:var(--sig-trace)]',
  log: 'text-[color:var(--sig-log)]',
  group: 'text-[color:var(--text2)]',
};
