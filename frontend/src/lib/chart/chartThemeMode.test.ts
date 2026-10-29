// @vitest-environment jsdom
//
// v0.10.1595 — Grafana köprüsü redhat temasını KARANLIK sanıyordu.
// Belirti: chartTheme() `data-theme !== 'light'` ile karar veriyordu, bu
// yüzden açık bir tema olan "Kurumsal" (data-theme="redhat": --bg1 #fff,
// --text #151515, color-scheme: light) Grafana display processor'a dark
// mod olarak gidiyordu. Kural CSS ile aynı olmalı: :root = dark, yalnız
// [data-theme="light"] ve [data-theme="redhat"] açık blok tanımlar; başka
// her değer (yok, 'dark', bilinmeyen) :root karanlık token'larına düşer.
//
// v0.10.1610 (shadcn Faz 1 R3, plan §2.4): tema okuması tek kaynağa
// (lib/theme.ts readTheme) geçti; öznitelik YOK / BİLİNMEYEN değer artık
// DEFAULT_THEME (redhat → açık) sayılır. Çalışma zamanında bu iki durum
// oluşmaz: index.html açılış betiği ilk boyamadan önce özniteliği hep
// geçerli bir temaya yazar, applyTheme/cycleTheme yalnız Theme yazar.
// 1595'in asıl hatası (redhat'in KOYU sanılması) aşağıda hâlâ çivili.
import { describe, it, expect, afterEach } from 'vitest';
import { chartTheme } from './dataFrame';
import { DEFAULT_THEME, isLightTheme } from '@/lib/theme';

describe('chartTheme mode follows the CSS theme blocks', () => {
  afterEach(() => document.documentElement.removeAttribute('data-theme'));

  const cases: { name: string; attr: string | null; dark: boolean }[] = [
    { name: 'attribute missing → DEFAULT_THEME', attr: null, dark: !isLightTheme(DEFAULT_THEME) },
    { name: 'dark', attr: 'dark', dark: true },
    { name: 'light', attr: 'light', dark: false },
    { name: 'redhat is a light theme', attr: 'redhat', dark: false },
    { name: 'unknown value falls back to DEFAULT_THEME', attr: 'bogus', dark: !isLightTheme(DEFAULT_THEME) },
  ];

  for (const c of cases) {
    it(c.name, () => {
      if (c.attr === null) document.documentElement.removeAttribute('data-theme');
      else document.documentElement.setAttribute('data-theme', c.attr);
      expect(chartTheme().isDark).toBe(c.dark);
    });
  }
});
