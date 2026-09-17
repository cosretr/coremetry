/**
 * spoolOrder — Spool runbook'unun eylem listesi (v0.10.761; operatör prod
 * ekranı: 30 Distributed tablo düğmeyle sıralı ama hangisinde kuyruk var
 * belli değil). Kuyruğu olan tablolar başa (dosya sayısı azalan), gerisi
 * ada göre; satır rozeti bekleyen/bozuk dosyayı söyler.
 */
export interface SpoolHostLike { host: string; files: number; blocked: boolean }
export interface SpoolQueueEntryLike {
  table: string;
  files: number;
  brokenFiles?: number;
  errorCount?: number;
  /** v0.10.773 — gönderici durmuş (is_blocked) + düğüm kırılımı. */
  blocked?: boolean;
  hosts?: SpoolHostLike[];
}

export interface SpoolActionRow {
  table: string;
  files: number;
  brokenFiles: number;
  errorCount: number;
  blocked: boolean;
  hosts: SpoolHostLike[];
}

/** Sunucunun Distributed envanteri (tables) × kuyruk ölçümü (queue.tables) → sıralı satırlar. */
export function orderSpoolTables(tables: readonly string[] | null | undefined, queue: readonly SpoolQueueEntryLike[] | null | undefined): SpoolActionRow[] {
  const depth = new Map<string, SpoolQueueEntryLike>();
  for (const q of queue ?? []) depth.set(q.table, q);
  const rows: SpoolActionRow[] = (tables ?? []).map(t => {
    const q = depth.get(t);
    return { table: t, files: q?.files ?? 0, brokenFiles: q?.brokenFiles ?? 0, errorCount: q?.errorCount ?? 0, blocked: !!q?.blocked, hosts: q?.hosts ?? [] };
  });
  return rows.sort((a, b) => {
    const aq = a.files > 0 || a.brokenFiles > 0 ? 1 : 0;
    const bq = b.files > 0 || b.brokenFiles > 0 ? 1 : 0;
    if (aq !== bq) return bq - aq;
    if (a.files !== b.files) return b.files - a.files;
    return a.table.localeCompare(b.table);
  });
}

// v0.10.773 — satır rozeti. Durmuş gönderici dosya sayısının yanında
// SÖYLENİR: prod 2026-09-17'de 514K dosya + 28 kümülatif hata bir saat
// "sarkan MV" diye arandı; gönderici sadece kapalıydı. title düğüm kırılımı.
export function spoolRowBadge(row: SpoolActionRow): { tone: 'b-err' | 'b-gray'; text: string; title: string } {
  const hosts = row.hosts.map(h => `${h.host}: ${h.files.toLocaleString('tr-TR')}${h.blocked ? ' (durmuş)' : ''}`).join(' · ');
  if (row.files > 0) {
    const extra = [row.blocked ? 'GÖNDERİCİ DURMUŞ' : '', row.brokenFiles > 0 ? `${row.brokenFiles.toLocaleString('tr-TR')} bozuk` : ''].filter(Boolean);
    const title = [hosts, row.errorCount > 0 ? `${row.errorCount.toLocaleString('tr-TR')} gönderim hatası (kümülatif)` : ''].filter(Boolean).join(' — ');
    return { tone: 'b-err', text: `${row.files.toLocaleString('tr-TR')} dosya bekliyor${extra.length ? ' · ' + extra.join(' · ') : ''}`, title };
  }
  if (row.brokenFiles > 0) return { tone: 'b-err', text: `${row.brokenFiles.toLocaleString('tr-TR')} bozuk`, title: hosts };
  if (row.blocked) return { tone: 'b-err', text: 'kuyruk boş · gönderici durmuş', title: hosts || 'is_blocked=1 — yeni dosya birikecek' };
  return { tone: 'b-gray', text: 'kuyruk boş', title: 'Bu tabloda bekleyen spool dosyası yok' };
}

// v0.10.775 — "Göndericiyi başlat" cevabı satırın YANINDA, düğüm başına.
// (prod: sonuç notu 30 satırlık listenin altındaydı, operatör "ok demiyor"
// dedi.) Düğüm yoksa (tek düğüm) tek kelime.
export function startResultText(res: { ok: boolean; hosts?: { host: string; ok: boolean; error?: string }[]; error?: string }): { tone: 'ok' | 'err'; text: string } {
  const hosts = res.hosts ?? [];
  if (hosts.length === 0) return res.ok ? { tone: 'ok', text: 'gönderici başlatıldı' } : { tone: 'err', text: res.error || 'başlatılamadı' };
  const parts = hosts.map(h => `${h.host.split(':')[0]} ${h.ok ? 'ok' : 'HATA: ' + (h.error || '?')}`);
  const bad = hosts.filter(h => !h.ok).length;
  return { tone: bad === 0 ? 'ok' : 'err', text: `${bad === 0 ? 'başlatıldı' : `${bad}/${hosts.length} düğümde hata`} · ${parts.join(' · ')}` };
}
