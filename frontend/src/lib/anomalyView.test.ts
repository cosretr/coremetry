import { describe, expect, it } from 'vitest';
import { resolveAnomalyView } from './anomalyView';
import { t } from './i18n';

describe('resolveAnomalyView', () => {
  it('platform herkese, held yalnız admine; diğerleri servis listesi', () => {
    expect(resolveAnomalyView('platform', false)).toBe('platform');
    expect(resolveAnomalyView('held', true)).toBe('held');
    expect(resolveAnomalyView('held', false)).toBe('');
    expect(resolveAnomalyView('x', true)).toBe('');
    expect(resolveAnomalyView(null, true)).toBe('');
  });
  it('sekme metinleri iki dilde; İngilizce arayüz İngilizce sözcük gösterir', () => {
    expect(t('anomalies.view.held', 'en')).toBe('Filtered out');
    expect(t('anomalies.view.held', 'tr')).toBe('Elenenler');
    expect(t('anomalies.view.service', 'en')).toBe('Services');
    expect(t('anomalies.view.heldHint', 'en')).toMatch(/not enforced/);
    expect(t('anomalies.view.heldHint', 'tr')).toMatch(/yürürlükte değil/);
  });
});
