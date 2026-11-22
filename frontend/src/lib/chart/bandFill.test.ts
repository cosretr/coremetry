import { describe, expect, it } from 'vitest';
import { bandFillColor, BAND_FILL_ALPHA } from './bandFill';

describe('bandFillColor', () => {
  it.each([
    ['#abc', `rgba(170, 187, 204, ${BAND_FILL_ALPHA})`],
    ['#58a6ff', `rgba(88, 166, 255, ${BAND_FILL_ALPHA})`],
    ['#58A6FFCC', `rgba(88, 166, 255, ${BAND_FILL_ALPHA})`],
    ['rgb(1, 2, 3)', `rgba(1, 2, 3, ${BAND_FILL_ALPHA})`],
    ['rgba(1,2,3,0.5)', `rgba(1, 2, 3, ${BAND_FILL_ALPHA})`],
    ['rgb(1 2 3 / 50%)', `rgba(1, 2, 3, ${BAND_FILL_ALPHA})`],
  ])('%s → saydam', (input, want) => {
    expect(bandFillColor(input)).toBe(want);
  });

  it('tanınmayan metin aynen döner (bant kaybolmaz)', () => {
    expect(bandFillColor('var(--text3)')).toBe('var(--text3)');
    expect(bandFillColor('tomato')).toBe('tomato');
  });

  it('alfa 0–1 aralığına kıstırılır', () => {
    expect(bandFillColor('#000000', 2)).toBe('rgba(0, 0, 0, 1)');
    expect(bandFillColor('#000000', -1)).toBe('rgba(0, 0, 0, 0)');
  });
});
