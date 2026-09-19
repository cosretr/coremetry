/**
 * priorityRank — P1/P2/P3 için sayısal sıralama anahtarı (v0.10.796).
 * P1 en yüksek; alan yoksa (eski sunucu) 0 → tablo sonunda. Tek kaynak:
 * Incidents sütunu ve ileride başka P sütunları aynı işlevi okur.
 */
export function priorityRank(p?: 'P1' | 'P2' | 'P3' | string): number {
  switch (p) {
    case 'P1': return 3;
    case 'P2': return 2;
    case 'P3': return 1;
    default: return 0;
  }
}
