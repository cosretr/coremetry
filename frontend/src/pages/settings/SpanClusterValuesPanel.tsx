// SpanClusterValuesPanel — v0.10.141 (cluster değeri otomatik eşleme brief'i).
// Span verisindeki cluster değerleri (son 7 gün, entity_seen_5m; yoksa spans
// 1 saat) sayaç + ilk/son görülme ile; her değer bir Remote Cluster'a bağlı
// ya da EŞLEŞMEMİŞ. Eşleşmemiş değer bir kayda atanır (kalıcı; isteğe bağlı
// 24 saatlik geriye dönük span geçişi). Bir değer tek kayda: çakışmayı
// sunucu reddeder, bağlı kayıt gösterilir.
import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { CheckboxField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { DataTableStateRow, type DataTableStateStaticProps } from '@/components/coremetry/DataTable';
import { api } from '@/lib/api';
import { fmtDateTime } from '@/lib/utils';
import { humanize } from './shared';

export function SpanClusterValuesPanel({ clusters, onAssigned }: {
  clusters: { id?: string; name: string }[];
  /** Atama sonrası ebeveyn formun satırı güncellenir — "Save all" eski listeyi gönderip atamayı sessizce geri almasın (inceleme). */
  onAssigned?: (clusterId: string, values: string[]) => void;
}) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['thanos-span-clusters'], queryFn: () => api.thanosSpanClusters(), staleTime: 60_000 });
  const [pick, setPick] = useState<Record<string, string>>({});
  const [backfill, setBackfill] = useState(true);
  const [msg, setMsg] = useState<Record<string, string>>({});
  const assign = async (value: string) => {
    const clusterId = pick[value];
    if (!clusterId) return;
    setMsg(m => ({ ...m, [value]: '…' }));
    try {
      const res = await api.thanosAssignSpanCluster({ value, clusterId, backfill });
      if (!res.ok) {
        setMsg(m => ({ ...m, [value]: `✗ ${res.error ?? 'reddedildi'}` }));
        return;
      }
      setMsg(m => ({ ...m, [value]: `✓ ${res.clusterName} · backfill ${res.backfill}` }));
      if (res.clusterId && res.values) onAssigned?.(res.clusterId, res.values);
      void qc.invalidateQueries({ queryKey: ['thanos-span-clusters'] });
    } catch (err) {
      setMsg(m => ({ ...m, [value]: `✗ ${err instanceof Error ? err.message : 'assign failed'}` }));
    }
  };
  const rows = q.data?.rows ?? [];
  // v0.10.967 — tablo standardı dilim 5 (P-2): Spinner / Empty zinciri
  // tablonun İÇİNE indi, başlık durur. Sıra aynı: yükleniyor, hata, boş.
  // Hata önbellekteki eski satırları da gizler (eskiden hata dalı tabloyu
  // hiç çizmiyordu): düşen tazelemenin altında bayat eşleme "güncel" gibi
  // okunmasın. Geriye dönük tarama anahtarı da aynı kapıya bağlı.
  const showRows = !q.isPending && !q.error && rows.length > 0;
  const state: Omit<DataTableStateStaticProps, 'colSpan'> =
    q.isPending ? { kind: 'loading' }
    : q.error ? { kind: 'error', message: `Span cluster değerleri yüklenemedi: ${humanize(q.error)}` }
    : { kind: 'empty', message: "Span verisinde cluster değeri yok — span'ler k8s.cluster.name / openshift.cluster.name / cluster taşımıyor." };
  // Faz 2 dilim 1 R7 (shadcn pilotu): inline stil yok; statik tablo shadcn
  // Table (T1 gerekçesi aynı: satır başına seçici + Assign, düzenlenebilir
  // eşleme listesi); durum satırı coremetry DataTableStateRow; kolonlar ve
  // sıraları aynı.
  return (
    <div className="card mt-4">
      <div className="ov-card-h">
        <h3>Span cluster values</h3>
        <span className="ov-sub">
          {q.data ? `${rows.length} value${rows.length === 1 ? '' : 's'} · ${q.data.unmapped} unmapped · ${q.data.source}${q.data.source === 'spans-1h' ? ' (entity şeması yok — son 1 saat)' : ' (7d)'}` : ''}
        </span>
      </div>
      <div className="ov-card-b">
        {/* v0.10.954 — geriye dönük tarama anahtarı yalnız atanacak satır varken anlamlı (eskisi gibi). */}
        {showRows && (
          <CheckboxField className="mb-2" checked={backfill} onCheckedChange={setBackfill}
            label="atamada son 24 saati geriye dönük tara (pod/servis entity'leri)" />
        )}
        {/* v0.10.942 — statik tablo: satır başına kayıt seçici + Assign, düzenlenebilir eşleme listesi (T1). */}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Value</TableHead><TableHead className="num">Spans</TableHead><TableHead>First seen</TableHead>
              <TableHead>Last seen</TableHead><TableHead>Bound to</TableHead><TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {/* v0.10.967 (P-2) — statik tablonun durum satırı; colSpan = thead'deki 6 <th>. */}
            {!showRows ? <DataTableStateRow colSpan={6} {...state} /> : rows.map(r => (
              <TableRow key={r.value}>
                <TableCell className="font-mono">{r.value}</TableCell>
                <TableCell className="num">{r.spans.toLocaleString()}</TableCell>
                <TableCell className="font-mono">{fmtDateTime(new Date(r.firstSeen))}</TableCell>
                <TableCell className="font-mono">{fmtDateTime(new Date(r.lastSeen))}</TableCell>
                <TableCell>
                  {r.ownerName
                    ? <Badge variant="outline" title={r.ownerId}>{r.ownerName}</Badge>
                    : <StatusBadge status="warn" title="Hiçbir Remote Cluster kaydı bu değeri taşımıyor">unmapped</StatusBadge>}
                </TableCell>
                <TableCell>
                  {!r.ownerName && (
                    <div className="flex items-center gap-2">
                      <NativeSelect size="sm" aria-label={`${r.value} için kayıt`} value={pick[r.value] ?? ''}
                        onChange={e => setPick(p => ({ ...p, [r.value]: e.target.value }))}>
                        <NativeSelectOption value="">— cluster —</NativeSelectOption>
                        {clusters.filter(c => c.id).map(c => <NativeSelectOption key={c.id} value={c.id}>{c.name}</NativeSelectOption>)}
                      </NativeSelect>
                      <Button size="xs" disabled={!pick[r.value]} onClick={() => assign(r.value)}>Assign</Button>
                      {msg[r.value] && <span className="text-[length:var(--fs-xs)] text-(--text3)">{msg[r.value]}</span>}
                    </div>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
