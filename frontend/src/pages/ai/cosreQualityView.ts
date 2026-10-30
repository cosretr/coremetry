// cosreQualityView — CoSRE öğrenme Faz 2: /ai "En kötü cevaplar / tool'lar"
// panelinin saf yardımcıları (internal/cosrelearn sinyal türleri).
import type { CosrePrefixStats } from '@/lib/types';

const REASON_LABEL: Record<string, string> = {
  copy: 'kopyalandı',
  link_click: 'link tıklandı',
  evidence_click: 'kanıt açıldı',
  problem_note: 'probleme not',
  followup_click: 'takip çipi', // v0.10.1291
  problem_resolved: 'problem çözüldü',
  thumbs_up: '👍',
  rephrase: 'yeniden soruldu',
  regenerate: 'yeniden üretildi',
  retry: 'yeniden denendi',
  edit: 'soru düzenlendi',
  abandon: 'yarıda bırakıldı',
  timeout: 'zaman aşımı',
  error: 'hata',
  tool_error: 'tool hatası',
  empty: 'boş cevap',
  no_evidence: 'kanıtsız cevap',
  thumbs_down: '👎',
  ungrounded: 'dayanaksız iddia',
};

const POSITIVE = new Set(['copy', 'link_click', 'evidence_click', 'problem_note', 'followup_click', 'problem_resolved', 'thumbs_up']);

export function reasonLabel(kind: string): string {
  return REASON_LABEL[kind] ?? kind;
}

export function reasonIsPositive(kind: string): boolean {
  return POSITIVE.has(kind);
}

/** Puan/oran 0..1 → "%42". */
export function pct(v: number): string {
  if (!Number.isFinite(v)) return '—';
  return `%${Math.round(Math.max(0, Math.min(1, v)) * 100)}`;
}

/** Yalnız sapan değer renklenir (tablo standardı): düşük puan kırmızı, orta sarı. */
export function scoreTone(v: number): 'err' | 'warn' | undefined {
  if (v < 0.25) return 'err';
  if (v < 0.45) return 'warn';
  return undefined;
}

/** v0.10.1637 — "dış bağımlılık bekleme" öneki satırı: payda yoksa açık
 *  boş-durum cümlesi (sunucu henüz kural uygulamadı), varsa eklendi/düzeltildi
 *  sayısı + kural uygulanan açıklamalara oranı. */
export function prefixStatsLine(p: CosrePrefixStats | undefined): string {
  if (!p || p.applied <= 0) return 'Dış bağımlılık öneki: son 7 günde kuralın uygulandığı trace açıklaması yok';
  return `Dış bağımlılık öneki (son 7 gün, ${p.applied} açıklama): eklendi ${p.added} (${pct(p.addedRate)}) · düzeltildi ${p.corrected} (${pct(p.correctedRate)})`;
}
