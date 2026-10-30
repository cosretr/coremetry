// browserTz.test.ts — v0.10.745 (operatör bildirimi: CoSRE exception
// açıklaması "tepe 22:30" dedi, ekran 01:30 gösteriyordu; Explain ucu
// tarayıcı dilimini göndermiyordu, sunucu damgaları UTC yazıyordu).
//
// Saf yarı: çiftin işareti/şekli. Kablolama yarısı: api.ts'te Explain
// gövdesi, sohbet bağlamı ve insight yolu AYNI yardımcıdan geçer — üç
// yoldan biri geri kayarsa (yeni bir explain ucu eski `{ method: 'POST' }`
// şekline dönerse) burada görünür.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { browserTz, tzQuery, withTzQuery } from './browserTz';

describe('browserTz — saf', () => {
  it('ofset DOĞU POZİTİF: getTimezoneOffset işareti çevrilir', () => {
    // Gate TZ=UTC ile koşar; yine de sabit bir Date üzerinden
    // işaret kuralını doğrudan pinliyoruz: -offset.
    const d = new Date('2026-09-15T22:30:00Z');
    const got = browserTz(d);
    expect(got.tzOffsetMin).toBe(-d.getTimezoneOffset());
    expect(typeof got.tz).toBe('string');
  });

  it('tzQuery / withTzQuery — boş çift yol bırakır, dolu çift ekler, mevcut ? korunur', () => {
    expect(tzQuery({ tz: '', tzOffsetMin: 0 })).toBe('');
    expect(withTzQuery('/api/insight/exception/fp', { tz: '', tzOffsetMin: 0 })).toBe('/api/insight/exception/fp');
    expect(tzQuery({ tz: 'Europe/Istanbul', tzOffsetMin: 180 })).toBe('tz=Europe%2FIstanbul&tzOffsetMin=180');
    expect(withTzQuery('/api/insight/exception/fp', { tz: 'Europe/Istanbul', tzOffsetMin: 180 }))
      .toBe('/api/insight/exception/fp?tz=Europe%2FIstanbul&tzOffsetMin=180');
    expect(withTzQuery('/api/insight/exception/fp?window=3600s', { tz: 'Europe/Istanbul', tzOffsetMin: 180 }))
      .toBe('/api/insight/exception/fp?window=3600s&tz=Europe%2FIstanbul&tzOffsetMin=180');
    // Yalnız ad (ofset 0 = UTC): ofset alanı gitmez, ad gider — sunucu adı çözer.
    expect(tzQuery({ tz: 'UTC', tzOffsetMin: 0 })).toBe('tz=UTC');
    // Yalnız ofset (Intl yok): ad gitmez.
    expect(tzQuery({ tz: '', tzOffsetMin: 330 })).toBe('tzOffsetMin=330');
  });
});

describe('browserTz — api.ts kablolaması', () => {
  const src = readFileSync(resolve(__dirname, 'api.ts'), 'utf8');
  const ctx = readFileSync(resolve(__dirname, 'copilotChatContext.ts'), 'utf8');

  // v0.10.1407 — ✨ Explain istemci yöntemleri (explainInit gövdesi: tz +
  // tzOffsetMin) özne kipiyle silindi: her açıklama sohbet turu ve dilim
  // çifti sohbet bağlamından gider (sunucu chatExplainTurn → chatLocationNamed).
  it('explainInit kalktı; açıklama turu dilimi sohbet bağlamıyla taşır', () => {
    expect(src).not.toContain('function explainInit(');
    const chat = src.slice(src.indexOf('copilotChat: async ('), src.indexOf('copilotChat: async (') + 6000);
    // v0.10.1648 — bağlam gövdesi lib/copilotChatContext'e çıktı (v1/v2 ortak).
    expect(chat).toContain('const context = buildCopilotChatContext({');
    expect(ctx).toContain('const { tz, tzOffsetMin } = browserTz(now);');
  });

  it('sohbet bağlamı ve insight yolu aynı yardımcıdan geçer', () => {
    expect(ctx).toContain('const { tz, tzOffsetMin } = browserTz(now);');
    expect(src).toContain('path = withTzQuery(path);');
    // Tek kaynak: ham getTimezoneOffset okuması api.ts'te kalmadı.
    expect(src).not.toContain('new Date().getTimezoneOffset()');
  });
});
