import { describe, expect, it } from 'vitest';
import { heldCounterText, heldReasonText, resolveAnomalyView } from './anomalyView';
import { t } from './i18n';

describe('resolveAnomalyView', () => {
  it('platform herkese, held yalnız admine; diğerleri servis listesi', () => {
    expect(resolveAnomalyView('platform', false)).toBe('platform');
    expect(resolveAnomalyView('held', true)).toBe('held');
    expect(resolveAnomalyView('held', false)).toBe('');
    expect(resolveAnomalyView('x', true)).toBe('');
    expect(resolveAnomalyView(null, true)).toBe('');
    expect(resolveAnomalyView('load', false)).toBe('load'); // v0.10.2476 Yük ve yapı herkese
  });
  it('v0.10.2476: invalid kayıt Elenenler nedeni ve sayacı', () => {
    expect(heldReasonText([{ name: 'invalid_record', value: 0, threshold: 0 }], 'tr')).toBe('Kayıt doğrulanamadı (olay kaydı sözleşmeye uymuyor)');
    expect(heldCounterText({ events: 1, byGate: { invalid_record: 1 } }, false, 'en')).toBe('Events filtered out in the last 24 hours: 1 (invalid record 1)');
  });
  it('sekme metinleri iki dilde; İngilizce arayüz İngilizce sözcük gösterir', () => {
    expect(t('anomalies.view.held', 'en')).toBe('Filtered out');
    expect(t('anomalies.view.held', 'tr')).toBe('Elenenler');
    expect(t('anomalies.view.service', 'en')).toBe('Services');
    expect(t('anomalies.view.heldHint', 'en')).toMatch(/Shadow mode/);
    expect(t('anomalies.view.heldHint', 'tr')).toMatch(/Gölge kipi/);
  });
  it('heldReasonText: kalan kapı → neden cümlesi (değer + eşik), iki dilde', () => {
    const gates = [{ name: 'min_volume', value: 42, threshold: 100 }, { name: 'min_abs_diff', value: 3.5, threshold: 25 }];
    expect(heldReasonText(gates, 'en')).toBe('Low volume: 42 calls (at least 100) · Small difference: 3.5 ms (at least 25 ms)');
    expect(heldReasonText(gates, 'tr')).toBe('Az hacim: 42 çağrı (en az 100) · Küçük fark: 3,5 ms (en az 25 ms)');
    expect(heldReasonText(undefined, 'en')).toBe('');
    expect(heldReasonText([{ name: 'other_gate', value: 1, threshold: 2 }], 'en')).toBe('other_gate');
  });
  it('heldCounterText: sayaç + kapı başına (sıfır kapı yazılmaz); gölge "elenecek"', () => {
    const held = { events: 5, byGate: { min_volume: 3, min_abs_diff: 0, visible_deviation: 2 } };
    expect(heldCounterText(held, false, 'en')).toBe('Events filtered out in the last 24 hours: 5 (low volume 3 · no visible deviation 2)');
    expect(heldCounterText(held, true, 'tr')).toBe('Son 24 saatte elenecek olay: 5 (az hacim 3 · görünür sapma yok 2)');
    expect(heldCounterText({ events: 0, byGate: {} }, false, 'tr')).toBe('Son 24 saatte elenen olay: 0');
  });
});
