/**
 * spoolOrder — Spool runbook'unun eylem listesi (v0.10.761; operatör prod
 * ekranı: 30 Distributed tablo düğmeyle sıralı ama hangisinde kuyruk var
 * belli değil). Kuyruğu olan tablolar başa (dosya sayısı azalan), gerisi
 * ada göre; satır rozeti bekleyen/bozuk dosyayı söyler.
 */
export interface SpoolQueueEntryLike {
  table: string;
  files: number;
  brokenFiles?: number;
  errorCount?: number;
}

export interface SpoolActionRow {
  table: string;
  files: number;
  brokenFiles: number;
  errorCount: number;
}

/** Sunucunun Distributed envanteri (tables) × kuyruk ölçümü (queue.tables) → sıralı satırlar. */
export function orderSpoolTables(tables: readonly string[] | null | undefined, queue: readonly SpoolQueueEntryLike[] | null | undefined): SpoolActionRow[] {
  const depth = new Map<string, SpoolQueueEntryLike>();
  for (const q of queue ?? []) depth.set(q.table, q);
  const rows: SpoolActionRow[] = (tables ?? []).map(t => {
    const q = depth.get(t);
    return { table: t, files: q?.files ?? 0, brokenFiles: q?.brokenFiles ?? 0, errorCount: q?.errorCount ?? 0 };
  });
  return rows.sort((a, b) => {
    const aq = a.files > 0 || a.brokenFiles > 0 ? 1 : 0;
    const bq = b.files > 0 || b.brokenFiles > 0 ? 1 : 0;
    if (aq !== bq) return bq - aq;
    if (a.files !== b.files) return b.files - a.files;
    return a.table.localeCompare(b.table);
  });
}
