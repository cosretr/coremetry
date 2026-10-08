// capabilityHints — v0.10.1128 (operatör): CoSRE karşılamasının "neler
// yapabilirim" ipucu. Boş sohbette, "Sana nasıl yardımcı olabilirim?"in
// altında 3–4 kısa satır; her satır composer'ı ÖRNEK bir soruyla DOLDURUR
// (göndermez) — operatör ifade kalıbını görerek öğrenir.
//
// Wiki satırı YALNIZ sohbet bu kullanıcıya wiki'den cevap verebiliyorsa
// (/api/copilot/config `wiki` bayrağı; sunucudaki kapı chatWikiAvailable).
// Örnek metinler i18n kataloğunda; `|` imlecin konacağı yeri işaretler
// (servis/operasyon adını operatör yazar — prefillEndpoint ile aynı desen).
//
// Dil (prod hatası, v0.10.1130): CoSRE sohbet yüzeyi TÜRKÇE-öncelikli — karşılama
// ("Merhaba", "Sana nasıl yardımcı olabilirim?"), başlangıç çipleri, şablon
// çipi CopilotChat'te sabit Türkçe. İpuçları useT ile UI dilinde (varsayılan
// EN) çizilince karşılama karışık dilli görünüyordu. Bu yüzden ipucu satırları,
// ipucu title'ı ve wiki çipi UI dilinden BAĞIMSIZ, sohbetin diliyle (tCosre)
// çözülür. EN metinler katalogda durur — sohbet-geneli i18n gelince
// COSRE_LANG yerine useT'ye geçilir; o güne kadar burada KULLANILMAZ.

import { t, type Lang } from '@/lib/i18n';

/** CoSRE sohbet yüzeyinin dili — karşılama metinleriyle aynı (sabit Türkçe). */
export const COSRE_LANG: Lang = 'tr';

/** Sohbetin diliyle (UI dilinden bağımsız) katalog araması. */
export function tCosre(key: string): string {
  return t(key, COSRE_LANG);
}

export type CapabilityKind = 'wiki' | 'service' | 'operation' | 'navigate';

export interface CapabilityItem {
  kind: CapabilityKind;
  /** i18n anahtarı — görünen satır. */
  labelKey: string;
  /** i18n anahtarı — composer'a dolan örnek ( `|` = imleç). */
  promptKey: string;
}

const WIKI: CapabilityItem = { kind: 'wiki', labelKey: 'cosre.cap.wiki', promptKey: 'cosre.cap.wiki.prompt' };
const REST: CapabilityItem[] = [
  { kind: 'service',   labelKey: 'cosre.cap.service',   promptKey: 'cosre.cap.service.prompt' },
  { kind: 'operation', labelKey: 'cosre.cap.operation', promptKey: 'cosre.cap.operation.prompt' },
  { kind: 'navigate',  labelKey: 'cosre.cap.navigate',  promptKey: 'cosre.cap.navigate.prompt' },
];

/** SAF — görünen satırlar; wiki satırı yalnız `wiki` true iken ve en başta. */
export function capabilityItems(wiki: boolean): CapabilityItem[] {
  return wiki ? [WIKI, ...REST] : REST;
}

/** SAF — `|` imleç işaretini söker; işaret yoksa imleç metnin sonunda. */
export function splitCaret(s: string): { text: string; caret: number } {
  const i = s.indexOf('|');
  if (i < 0) return { text: s, caret: s.length };
  return { text: s.slice(0, i) + s.slice(i + 1), caret: i };
}
