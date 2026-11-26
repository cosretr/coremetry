// inboxColumns — Problems (/inbox) tablosunun kolonları ve saf satır metinleri.
// v0.10.2170 (Problems S10, docs/frontend/problems-page-plan.md) — Inbox.tsx'ten
// taşındı; içerik aynen (davranış değişmez).
import { inboxRowHeadline, inboxOccurrencesLabel } from '@/lib/inboxRowText';
import type { DataTableColumn } from '@/lib/dataTable';
import { stdConfidenceRank } from '@/lib/problemStandardView';
import type { InboxItem } from '@/lib/types';
import { t, tf, type Lang } from '@/lib/i18n'; // v0.10.2283 — kolon ve kırpma metinleri arayüz dilinde (TR aynı)

const PRIO_RANK: Record<string, number> = { P1: 3, P2: 2, P3: 1 };

// Columns for the shared sortable + resizable DataTable. Default sort is
// priority desc (P1 first); rows are pre-sorted by lastSeen desc so the
// stable sort yields "P1 first, newest within priority" — the prior
// fixed ordering, now re-sortable + resizable per column.
// v0.10.1068 (operatör: "Kolonlar kayıyor, sığmıyor") — okunurluk tabanları
// + öncelik: kap taşıyamazsa önce First seen, sonra Assignee / Last seen /
// Source düşer ("+N sütun"); Detail en az 240 px. Damgalar yılsız
// (tsCompact, tam damga title'da) — 168 px'lik tam damga 1366'da kırpılıyordu.
// v0.10.1084 (operatör: "tekilleştir, Exceptions'taki format güzel") — kolon
// düzeni Exceptions listesininki: Prio · başlık (kalın cümle / tip + satır içi
// durum çipi, soluk ayrıntı) · Service · Occurrences · First seen · Last seen ·
// Assignee. Ayrı Source kolonu kalktı: kaynak ikinci satırın başında. Tabanlar
// ve öncelikler v0.10.1068'deki gibi.
// v0.10.1529 (Problem standardı R2; operatör 2026-10-25: "mockup + ikincil
// kolonlar") — Öncelik · Ne bozuk · Etki · Ne zamandan beri · Güven · Karar,
// sağda düşük öncelikli Occurrences · Last seen · Assignee (dar ekranda ilk
// onlar düşer). Service kolonu kalktı: özne "Ne bozuk"un başında (mono),
// takım çipleri ve yayılım üçüncü satırda. "Ne zamandan beri" first seen'in
// sıralama kimliğini (`firstSeen`) devralır — v0.10.1081 varsayılan sıralaması
// ve paylaşılan `s_inbox=firstSeen` linkleri aynen çalışır. Satırın std'si
// yoksa (eski önbellek) hücreler v0.10.1084 biçimine düşer.
// v0.10.2121 (Problems sadeleştirme S1, docs/frontend/problems-page-plan.md;
// mockup operatör onaylı: "güzel, tüm problemler gözüksün") — Öncelik · Ne
// bozuk · Servis · Ekip · Tür · Ne zamandan beri · İlgilenen. Etki / Güven /
// Karar / Occurrences / Last seen listeden kalktı (ayrıntı detay sayfasında);
// oluşum sayısı "Ne bozuk"un alt satırında, satır triyajı (D1) İlgilenen
// hücresinde hover/focus'ta (tablo standardı: eylemler tek sağ sütunda).
// "Ne zamandan beri" `firstSeen` sıralama kimliğini korur (s_inbox linkleri).
// v0.10.2283 — etiketler lib/i18n inbox.col.* (TR aynı); dil başına bir dizi.
// v0.10.2395 (operatör 2026-11-20, onaylı triage mockup'ı liste dilimi) — Etki /
// Güven / Kanıt "Ne bozuk"un hemen sağında geri. Etki = standardın etki çekirdeği
// (detay kartıyla aynı; hata oranında "Dakikada N başarısız · M başarılı"), Güven
// = std.confidence rozeti, Kanıt = bağlı olay sayısı (InboxItem.eventCount; liste
// yanıtında zaten var, ek okuma yok). Karar kolonu GERİ GELMEDİ: triyaj
// İlgilenen hücresinde (eylemler tek sağ sütunda), ayrı kolon aynı düğmeleri
// ikinci kez çizerdi. 1366 px (kenar çubuğu 220) bütçesinde tabanlar sığmaz;
// düşme sırası Tür (4) → Kanıt (4) → Servis (3; std manşeti özneyi zaten
// taşıyor) — Etki / Güven / İlgilenen kalır, düşenler "+N sütun"dan açılır.
// v0.10.2476 — closed: Kapanan görünümü; zaman kolonu kapanış + süreyi çizer.
export const inboxCols = (lang: Lang, closed = false): DataTableColumn<InboxItem>[] => [
  { id: 'priority', label: t('inbox.col.priority', lang), sortValue: it => PRIO_RANK[it.priority] ?? 0, naturalDir: 'desc', width: 84, minWidth: 80, priority: 1 },
  // v0.9.648 — ESNEK kolon: başlık serbest metin ve en geniş olan.
  { id: 'detail',   label: t('inbox.col.detail', lang),  sortValue: it => it.std?.headline ?? inboxRowHeadline(it), naturalDir: 'asc', flex: true, minWidth: 260, priority: 1 },
  { id: 'impact',   label: t('inbox.col.impact', lang), width: 260, minWidth: 150, priority: 2 },
  { id: 'confidence', label: t('inbox.col.confidence', lang), sortValue: it => stdConfidenceRank(it.std), naturalDir: 'desc', width: 92, minWidth: 80, priority: 2 },
  { id: 'evidence', label: t('inbox.col.evidence', lang), sortValue: it => it.eventCount ?? 0, naturalDir: 'desc', numeric: true, width: 84, minWidth: 72, priority: 4 },
  { id: 'service',  label: t('inbox.col.service', lang), sortValue: it => it.service, naturalDir: 'asc', width: 170, minWidth: 120, priority: 3 },
  { id: 'team',     label: t('inbox.col.team', lang), width: 150, minWidth: 110, priority: 2 },
  { id: 'kind',     label: t('inbox.col.kind', lang), width: 110, minWidth: 90, priority: 4 },
  { id: 'firstSeen', label: t(closed ? 'inbox.col.closedAt' : 'inbox.col.firstSeen', lang), sortValue: it => it.startedAt, naturalDir: 'desc', width: 160, minWidth: 140, priority: 2 },
  { id: 'assignee', label: t('inbox.col.assignee', lang), sortValue: it => it.assignee ?? '', naturalDir: 'asc', width: 236, minWidth: 150, priority: 2 },
];

// capSortLabel — v0.10.1081: kırpma rozetinin "neye göre" yarısı. Sunucu
// tavanı seçili sıralamayla keser; eski metin her durumda "öncelik sırasına
// göre" diyordu ve varsayılan artık ilk görülme.
// v0.10.2283 — adlar lib/i18n inbox.capSort.* (TR aynı).
const CAP_SORT_IDS = new Set(['priority', 'firstSeen', 'lastSeen', 'occurrences', 'service', 'source', 'detail', 'assignee',
  'confidence', 'evidence']); // confidence: v0.10.1529; evidence: v0.10.2395
export function capSortLabel(id: string | null, dir: 'asc' | 'desc', lang: Lang = 'tr'): string {
  const name = t(`inbox.capSort.${CAP_SORT_IDS.has(id ?? '') ? id : 'priority'}`, lang);
  const order = id === 'firstSeen' || id === 'lastSeen'
    ? t(dir === 'desc' ? 'inbox.capSort.newest' : 'inbox.capSort.oldest', lang)
    : '';
  return tf('inbox.capSort', lang, { name, order });
}

// occLine — v0.10.2121: "Ne bozuk" alt satırındaki oluşum sayısı (exception →
// "N oluşum", katlanmış incident → "N problem"); yoksa null.
// v0.10.2395 — Problem'in "N olay"ı Kanıt kolonunda; burada ikinci kez yazılmaz.
export function occLine(it: InboxItem, lang: Lang = 'tr'): string | null {
  if (it.kind === 'problem') return null;
  const l = inboxOccurrencesLabel(it, lang);
  if (!l) return null;
  return it.exception ? tf('inbox.occ', lang, { l }) : l;
}
