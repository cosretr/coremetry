import { describe, it, expect } from 'vitest';
import { greetHello, greetErrors, greetHref, greetTooltip, agoTR } from './greeting';
import type { CosreGreeting } from '@/lib/types';

// v0.9.528 — karşılama saf olduğu için sözleşmesi burada pinli.
// Kritik nokta: karşılama bir İDDİA taşıyor ("3 açık P1 var"). Yanlış
// iddia, operatörün sohbeti kapatmasına yol açar — bu yüzden yükleme
// hâli ile "gerçekten sıfır" hâli ASLA aynı cümleye düşmemeli.

const NOW = 1_800_000_000_000; // sabit "şimdi" (ms)
const nsAgo = (mins: number) => (NOW - mins * 60_000) * 1e6;

describe('greetHello', () => {
  it('adı varsa isimle hitap eder', () => {
    expect(greetHello('Fatih')).toBe('Merhaba Fatih.');
  });
  it('ad yoksa isimsiz — uydurmaz', () => {
    expect(greetHello()).toBe('Merhaba 👋');
    expect(greetHello('')).toBe('Merhaba 👋');
    expect(greetHello('   ')).toBe('Merhaba 👋');
  });
});

describe('agoTR', () => {
  it('her birim ayrı ayrı doğru — birim karışması tekrar eden hata sınıfı', () => {
    expect(agoTR(nsAgo(0.2), NOW)).toBe('12 sn önce');
    expect(agoTR(nsAgo(1), NOW)).toBe('1 dk önce');
    expect(agoTR(nsAgo(12), NOW)).toBe('12 dk önce');
    expect(agoTR(nsAgo(59), NOW)).toBe('59 dk önce');
    expect(agoTR(nsAgo(60), NOW)).toBe('1 sa önce');
    expect(agoTR(nsAgo(300), NOW)).toBe('5 sa önce');
    expect(agoTR(nsAgo(60 * 24), NOW)).toBe('1 gün önce');
    expect(agoTR(nsAgo(60 * 24 * 3), NOW)).toBe('3 gün önce');
  });

  it('gelecek zaman damgası negatife düşmez (saat kayması)', () => {
    expect(agoTR((NOW + 60_000) * 1e6, NOW)).toBe('az önce');
  });
});

// v0.10.1254 — sayı Inbox toplamından (liste limit'le kırpılır).

// v0.10.1278 — operatör 2026-10-17: CoSRE karşılaması tüm hatalı span'leri
// sayıyordu ("445.394 hata · 812 exception grubu"). Yeni dil: hatalı GİRİŞ
// isteği + AÇIK grup (yeni ayrıca); ortam etiketi yalnız uygulandıysa.
const g = (o: Partial<CosreGreeting>): CosreGreeting => ({
  failedRequests1h: 0, openExceptionGroups1h: 0, newExceptionGroups1h: 0,
  env: '', envApplied: false, exceptionsEnvApplied: false, ...o,
});

describe('greetErrors', () => {
  it('yüklenmeden boş', () => {
    expect(greetErrors(undefined)).toBe('');
  });
  it('sıfırda sakin satır (ortamlı da)', () => {
    expect(greetErrors(g({}))).toBe('Son 1 saatte hata yok — sistem sakin görünüyor.');
    expect(greetErrors(g({ env: 'prod', envApplied: true, exceptionsEnvApplied: true }))).toBe('Son 1 saatte hata yok — sistem sakin görünüyor.');
  });
  it('tüm ortamlar: etiket yok, yeni ayrı', () => {
    expect(greetErrors(g({ failedRequests1h: 1234, openExceptionGroups1h: 37, newExceptionGroups1h: 5 })))
      .toBe('Son 1 saatte 1.234 hatalı istek · 37 açık exception grubu (5 yeni)');
  });
  it('ortam uygulandı: (prod) etiketi', () => {
    expect(greetErrors(g({ failedRequests1h: 1234, openExceptionGroups1h: 37, newExceptionGroups1h: 5, env: 'prod', envApplied: true, exceptionsEnvApplied: true })))
      .toBe('Son 1 saatte 1.234 hatalı istek (prod) · 37 açık exception grubu (5 yeni)');
  });
  it('ortam istendi ama uygulanamadı: etiket BASILMAZ (yalan olurdu)', () => {
    expect(greetErrors(g({ failedRequests1h: 3, openExceptionGroups1h: 1, env: 'prod', envApplied: false })))
      .toBe('Son 1 saatte 3 hatalı istek · 1 açık exception grubu');
  });
  it('yeni=0 → parantez yok; yalnız istek ya da yalnız grup', () => {
    expect(greetErrors(g({ failedRequests1h: 0, openExceptionGroups1h: 4 }))).toBe('Son 1 saatte 0 hatalı istek · 4 açık exception grubu');
    expect(greetErrors(g({ failedRequests1h: 445394, openExceptionGroups1h: 0 }))).toBe('Son 1 saatte 445.394 hatalı istek · 0 açık exception grubu');
  });
});

describe('greetTooltip', () => {
  it('tanımlar her zaman; ortamsız "tümü"', () => {
    const t = greetTooltip(g({}));
    expect(t).toContain("giriş span'leri (server+consumer)");
    expect(t).toContain('çözülmemiş (new/acknowledged/regressed), son 1 saatte görülen');
    expect(t).toContain('Ortam: tümü.');
    expect(greetTooltip(undefined)).toBe('');
  });
  it('ortam uygulandı / uygulanamadı ayrımı', () => {
    expect(greetTooltip(g({ env: 'prod', envApplied: true, exceptionsEnvApplied: true }))).toContain('hata payıyla prod');
    expect(greetTooltip(g({ env: 'prod', envApplied: true, exceptionsEnvApplied: true }))).toContain('prod ortamında koşan servislerinkiler');
    expect(greetTooltip(g({ env: 'prod' }))).toContain('sayı tüm ortamlar');
    expect(greetTooltip(g({ env: 'prod' }))).toContain('Exception grupları: tüm ortamlar');
  });
});

describe('greetHref', () => {
  it('Exceptions sayfası, ortam taşınır', () => {
    expect(greetHref('')).toBe('/problems');
    expect(greetHref('prod')).toBe('/problems?env=prod');
    expect(greetHref('a b')).toBe('/problems?env=a%20b');
  });
});
