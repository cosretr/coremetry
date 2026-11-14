// inboxColumns — Problems (/inbox) tablosunun kolonları ve saf satır metinleri.
// v0.10.2170 (Problems S10, docs/frontend/problems-page-plan.md) — Inbox.tsx'ten
// taşındı; içerik aynen (davranış değişmez).
import { inboxRowHeadline, inboxOccurrencesLabel } from '@/lib/inboxRowText';
import type { DataTableColumn } from '@/lib/dataTable';
import type { InboxItem } from '@/lib/types';

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
export const INBOX_COLS: DataTableColumn<InboxItem>[] = [
  { id: 'priority', label: 'Öncelik', sortValue: it => PRIO_RANK[it.priority] ?? 0, naturalDir: 'desc', width: 84, minWidth: 80, priority: 1 },
  // v0.9.648 — ESNEK kolon: başlık serbest metin ve en geniş olan.
  { id: 'detail',   label: 'Ne bozuk',  sortValue: it => it.std?.headline ?? inboxRowHeadline(it), naturalDir: 'asc', flex: true, minWidth: 260, priority: 1 },
  { id: 'service',  label: 'Servis', sortValue: it => it.service, naturalDir: 'asc', width: 170, minWidth: 120, priority: 2 },
  { id: 'team',     label: 'Ekip', width: 150, minWidth: 110, priority: 2 },
  { id: 'kind',     label: 'Tür', width: 110, minWidth: 90, priority: 3 },
  { id: 'firstSeen', label: 'Ne zamandan beri', sortValue: it => it.startedAt, naturalDir: 'desc', width: 160, minWidth: 140, priority: 2 },
  { id: 'assignee', label: 'İlgilenen', sortValue: it => it.assignee ?? '', naturalDir: 'asc', width: 236, minWidth: 150, priority: 3 },
];

// capSortLabel — v0.10.1081: kırpma rozetinin "neye göre" yarısı. Sunucu
// tavanı seçili sıralamayla keser; eski metin her durumda "öncelik sırasına
// göre" diyordu ve varsayılan artık ilk görülme.
const CAP_SORT_LABEL: Record<string, string> = {
  priority: 'öncelik', firstSeen: 'ilk görülme', lastSeen: 'son görülme', occurrences: 'oluşum sayısı',
  service: 'servis', source: 'kaynak', detail: 'başlık', assignee: 'atanan',
  confidence: 'güven', // v0.10.1529
};
export function capSortLabel(id: string | null, dir: 'asc' | 'desc'): string {
  const name = CAP_SORT_LABEL[id ?? ''] ?? CAP_SORT_LABEL.priority;
  const order = id === 'firstSeen' || id === 'lastSeen'
    ? (dir === 'desc' ? ', en yeni önce' : ', en eski önce')
    : '';
  return `${name} sırasına göre${order}`;
}

// occLine — v0.10.2121: "Ne bozuk" alt satırındaki oluşum sayısı (exception →
// "N oluşum", katlanmış incident → "N problem"); yoksa null.
export function occLine(it: InboxItem): string | null {
  const l = inboxOccurrencesLabel(it);
  if (!l) return null;
  return it.exception ? `${l} oluşum` : l;
}
