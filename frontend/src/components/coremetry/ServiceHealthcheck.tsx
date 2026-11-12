// coremetry/ServiceHealthcheck — v0.10.2039 (servis sağlık kontrolü, dilim 1;
// spec docs/plans/spec-service-healthcheck-runbook.md).
//
// Önce tek düz cümle, altında 4 sütunlu kontrol listesi: Kontrol · Durum ·
// Ne görüldü · Nereye bakmalı. Renk YALNIZ sapan satırda (Dikkat / Sorun);
// "Ölçülemedi" gri ve nedenini yazar — asla "İyi" gibi çizilmez. Teknik kanıt
// (PromQL / SQL) kapalı ayrıntıda.
//
// Tablo kararı (/frontend-design-system §5, CLAUDE.md "dört tablo türü"):
// statik shadcn Table — kontrol listesi sabit ve sıralı (katalog sırası, en çok
// ~20 satır: 5 servis kontrolü + cluster başına 3), sıralanmaz, satır
// tıklanmaz (bağlantı yalnız son sütunda). useDataTable'ın sıralama/genişlik
// kaydı bu listeye anlam katmaz.
//
// Yük: istek YALNIZ panel açıkken (useServiceHealthcheck enabled); sayfa
// açılışına eklenmez. "Yeniden çalıştır" sunucunun 60 sn önbelleğini AŞMAZ —
// art arda tıklama kaynakları yeniden dövmez; ölçüm saati yazılır.
import { Link } from 'react-router-dom';
import { Button } from '@/components/shadcn/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { DetailSheet, useSheetParam } from '@/components/coremetry/DetailSheet';
import { Spinner, Empty } from '@/components/Spinner';
import { useServiceHealthcheck } from '@/lib/queries';
import { cn } from '@/lib/cn';
import { fmtClock } from '@/lib/utils';
import type { HealthcheckResult, HealthcheckRow, HealthcheckState } from '@/lib/types';

/** Durum etiketi (Türkçe; renk değil metin taşır). */
export const HC_STATE_LABEL: Record<HealthcheckState, string> = {
  ok: 'İyi', warn: 'Dikkat', crit: 'Sorun', unknown: 'Ölçülemedi',
};

/** SAF: durum → hücre sınıfı. İyi nötr; yalnız sapma renkli; Ölçülemedi gri. */
export function hcStateClass(s: HealthcheckState): string {
  switch (s) {
    case 'crit': return 'text-(--err) font-medium';
    case 'warn': return 'text-(--warn) font-medium';
    case 'unknown': return 'text-(--text3)';
    default: return '';
  }
}

function rowTitle(r: HealthcheckRow): string {
  // " · " ayırıcı: datasource öznesi zaten "ORDERS_DS (cluster-a)" biçiminde.
  return r.subject ? `${r.title} · ${r.subject}` : r.title;
}

/** Sonucun gövdesi — cümle + tablo + kapalı kanıt. Servis sayfası, /runbooks
 *  ve sohbet aynı görünümü kullanır. */
export function HealthcheckResultView({ data }: { data: HealthcheckResult }) {
  const evid = data.rows.filter(r => r.evidence);
  const at = fmtClock(data.measuredAt);
  return (
    <div className="flex flex-col gap-3">
      <p className={cn('m-0 text-[length:var(--fs-md)]', data.verdict === 'crit' && 'text-(--err)')}>{data.sentence}</p>
      <div className="field-hint m-0">
        Son {data.window} · ölçüm {at} · {(data.durationMs / 1000).toFixed(1)} sn
        {data.env ? ` · ortam ${data.env} (yalnız hata oranı ve gecikme)` : ''}
        {data.partial ? ' · kısmi: bazı kaynaklar zamanında cevap vermedi' : ''}
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Kontrol</TableHead><TableHead>Durum</TableHead>
            <TableHead>Ne görüldü</TableHead><TableHead>Nereye bakmalı</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {data.rows.map((r, i) => (
            <TableRow key={`${r.check}:${r.subject ?? ''}:${i}`} data-state-hc={r.state}>
              <TableCell>{rowTitle(r)}</TableCell>
              <TableCell className={hcStateClass(r.state)}>{HC_STATE_LABEL[r.state]}</TableCell>
              <TableCell className="whitespace-normal [overflow-wrap:anywhere]">{r.seen}</TableCell>
              <TableCell>{r.link ? <Link to={r.link}>{r.linkLabel || 'Aç'}</Link> : <span className="text-(--text3)">—</span>}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {data.notApplicable && data.notApplicable.length > 0 && (
        <div className="field-hint m-0">Uygulanmadı (bu serviste veri yok): {data.notApplicable.join(', ')}</div>
      )}
      {evid.length > 0 && (
        <details>
          <summary className="field-hint cursor-pointer">Teknik kanıt (çalışan sorgular)</summary>
          {evid.map((r, i) => (
            <div key={i} className="mt-2">
              <div className="field-hint">{rowTitle(r)}</div>
              <pre className="m-0 font-mono text-[length:var(--fs-xs)] whitespace-pre-wrap [overflow-wrap:anywhere]">{r.evidence}</pre>
            </div>
          ))}
        </details>
      )}
    </div>
  );
}

/** Koşu + durumlar. `active` false iken istek atılmaz. */
export function ServiceHealthcheckPanel({ service, env = '', window = '15m', active = true }: {
  service: string; env?: string; window?: string; active?: boolean;
}) {
  const q = useServiceHealthcheck(service, env, window, active);
  if (q.isPending || (q.isFetching && !q.data)) {
    return <Spinner label="Sağlık kontrolü koşuyor" hint="Pod, cluster ve servis sinyalleri okunuyor — en çok 15 sn." />;
  }
  if (q.isError) {
    return (
      <Empty icon="!" title="Sağlık kontrolü koşamadı"
        action={<Button variant="secondary" size="sm" onClick={() => void q.refetch()}>Yeniden dene</Button>}>
        {q.error instanceof Error ? q.error.message : 'Bilinmeyen hata'}
      </Empty>
    );
  }
  if (!q.data || q.data.rows.length === 0) {
    return <Empty icon="—" title="Kontrol sonucu yok">Sunucu boş bir sonuç döndü.</Empty>;
  }
  return (
    <div className="flex flex-col gap-3">
      <HealthcheckResultView data={q.data} />
      <div>
        <Button variant="secondary" size="sm" loading={q.isFetching} onClick={() => void q.refetch()}>Yeniden çalıştır</Button>
      </div>
    </div>
  );
}

/** Servis sayfası düğmesi + çekmece. Açık durum URL'de (`?hc=1`). */
export function ServiceHealthcheckButton({ service, env }: { service: string; env?: string }) {
  const [hc, setHc] = useSheetParam('hc');
  const open = hc !== null;
  return (
    <>
      <Button variant="secondary" size="sm" onClick={() => setHc('1')}
        title="Pod, cluster ve servis sinyallerini salt-okunur kontrol et">
        Sağlık kontrolü
      </Button>
      {open && (
        <DetailSheet open onOpenChange={o => { if (!o) setHc(null); }} width={760}
          resizeKey="coremetry-healthcheck-drawer-w" title={`Sağlık kontrolü · ${service}`}
          ariaLabel="Servis sağlık kontrolü">
          <ServiceHealthcheckPanel service={service} env={env} />
        </DetailSheet>
      )}
    </>
  );
}
