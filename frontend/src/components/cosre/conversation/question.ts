// question — v0.10.2102 (docs/cosre/experience-audit.md D2). SAF.
// CoSRE netleştirme sorusunu düz metin olarak sorar (balonda aynen kalır);
// v2 ayrıca data-question yayar ve cevabın altında ≤3 düğmeli kart çizilir.
//   • Tık → seçeneğin ETİKETİ yeni kullanıcı mesajı + context.clarifyOption
//     (sunucu bekleyen soruya birebir eşler; serbest metin yolu aynen).
//   • Kart yalnız son, bitmiş cevapta etkin; sonraki turdan sonra PASİF ve
//     görünür; seçim lib/cosre/cosrePersist questionChoice (arşiv → tık →
//     metin, v0.10.2104 D5).
import type { CosreQuestion } from '@/lib/types';
import { parseDataQuestion } from '@/lib/cosre/protocol';

type Part = { type: string; data?: unknown };

/** Mesajın soru yükü (son geçerli data-question; yoksa null). */
export function questionOf(parts: readonly Part[]): CosreQuestion | null {
  let out: CosreQuestion | null = null;
  for (const p of parts) {
    if (p.type === 'data-question') out = parseDataQuestion(p) ?? out;
  }
  return out;
}

/** Kart tıkının bağlamı: son katman olarak clarifyOption. */
export function withClarifyOption(base: Record<string, unknown> | undefined, id: string): Record<string, unknown> {
  return { ...(base ?? {}), clarifyOption: id };
}
