// AiSreStatusPanel — v0.10.1286 (AI SRE ayar ekranı). GET /api/sre/status'un
// salt-okunur özeti: açık mı, drain nerede koşuyor (launcher), rol görüldü mü,
// kuyruk derinliği, koşan iş ve taze kalp atışı olan pod'lar. 10 s yoklama
// (useSREStatus — gizli sekmede durur; sunucu önbelleği 5 s). Hiçbir eylem yok.
import { KeyValue } from '@/components/coremetry/KeyValue';
import { Spinner } from '@/components/Spinner';
import { QueryError } from '@/components/QueryError';
import { useSREStatus } from '@/lib/queries';
import { agoTR, tsLong } from '@/lib/utils';
import type { SREStatus } from '@/lib/types';
import { sreRoleMissing } from '@/features/anomalies/sreInvestigationView';
import { SRE_LAUNCHER_LABEL } from './sreSettingsForm';
import { SectionTitle, humanize } from './shared';

export function AiSreStatusPanel() {
  const q = useSREStatus(true);
  return (
    <section aria-label="AI SRE durumu">
      <SectionTitle>Canlı durum</SectionTitle>
      {q.isLoading ? (
        <Spinner label="Durum okunuyor…" />
      ) : q.error || !q.data ? (
        <QueryError title="AI SRE durumu okunamadı" message={q.error ? humanize(q.error) : null}
          onRetry={() => { void q.refetch(); }}>
          Kuyruk / rol durumu alınamadı; aşağıdaki ayar formu bundan etkilenmez.
        </QueryError>
      ) : (
        <StatusBody s={q.data} />
      )}
    </section>
  );
}

function StatusBody({ s }: { s: SREStatus }) {
  return (
    <>
      {!s.enabled && (
        <div className="cm-callout" role="status">AI SRE kapalı — yeni araştırma açılmaz, drain yeni iş almaz.</div>
      )}
      {sreRoleMissing(s) && (
        <div className="cm-callout is-warning" role="alert">
          SRE rolü yok — kuyrukta {s.queueDepth} iş bekliyor. COREMETRY_MODE=sre bir deployment ya da all modu gerekir.
        </div>
      )}
      <KeyValue items={[
        { id: 'enabled', k: 'Ajan', v: s.enabled ? 'açık' : 'kapalı' }, // K5: ayar durumu nötr, yeşil rozet yok
        { id: 'launcher', k: 'Drain', v: SRE_LAUNCHER_LABEL[s.launcher] ?? s.launcher },
        { id: 'role', k: 'Rol görüldü', v: s.roleSeen ? 'evet (son 60 sn)' : 'hayır' },
        { id: 'queue', k: 'Kuyruk', v: String(s.queueDepth) },
        { id: 'running', k: 'Koşan', v: String(s.running) },
      ]} />
      {s.pods.length > 0 ? (
        <KeyValue aria-label="SRE pod'ları" items={s.pods.map(p => ({
          id: p.pod,
          k: 'Pod',
          v: (
            <>
              <span className="mono">{p.pod}</span>
              {' · '}{p.mode}{p.version ? ` · ${p.version}` : ''}
              {' · '}{p.inFlight} koşan · {p.drained} tamamlanan
              {' · '}<span title={tsLong(p.at)}>{agoTR(p.at)}</span>
            </>
          ),
        }))} />
      ) : (
        <div className="text-[length:var(--fs-xs)] text-(--text3)">Taze kalp atışı olan SRE pod'u yok.</div>
      )}
    </>
  );
}
