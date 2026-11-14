import { useState } from 'react';
import { CollapsedSection } from '@/components/coremetry/CollapsedSection';

// InfraSourceNote — v0.10.2145 (servis sekmeleri S6, docs/frontend/
// service-tabs-plan.md §3; mockup operatör onayı 2026-11-13). Infrastructure
// sayfasının ALTINDA kapalı tek satır: eskiden tablo başlıklarında duran
// kaynak rozetleri (E / T / E+T, eşleşme kuralı, unmapped, topk kısmi,
// "entity katmanı okunamadı") burada düz cümle. Hiçbiri silinmedi; yalnız
// varsayılan görünümden çıktı. Hiçbir şey çekmez — yanıtlar çağıranda.
export interface InfraSourceNoteProps {
  entityEnabled: boolean;
  /** Eşleşme kuralı cümlesi ("k8s.namespace=… · deploy" ya da pod adı kalıbı). */
  match: string;
  /** Pod sayıları, kaynağa göre. */
  counts: { both: number; entity: number; thanos: number };
  scanned: string;
  unmapped: string[];
  ambiguous: string[];
  statusNotes: string[];
  truncated: string[];
  unreachable: string[];
  /** Thanos kaynak listesi yenilemesi düştü (önbellekteki liste duruyor). */
  sourcesError: string;
  entityError: string;
}

/**
 * Kapalı satırın cümlesi. Uyarı / hata varken onları ADLARIYLA sayar: kapalı
 * satır bir okuma hatasını gizleyemez (eski başlık rozetlerinin görünürlüğü).
 */
export function infraSourceSummary(p: Pick<InfraSourceNoteProps, 'unmapped' | 'statusNotes' | 'truncated' | 'unreachable' | 'sourcesError' | 'entityError'>): string {
  const flags: string[] = [];
  if (p.sourcesError) flags.push('Thanos kaynaklarına erişilemedi');
  if (p.unreachable.length > 0) flags.push(`${p.unreachable.length} cluster yanıt vermedi`);
  if (p.entityError) flags.push('entity katmanı okunamadı');
  if (p.truncated.length > 0) flags.push(`Thanos kısmi (${p.truncated.length} cluster)`);
  if (p.unmapped.length > 0) flags.push(`${p.unmapped.length} eşlenmemiş cluster değeri`);
  if (p.statusNotes.length > 0) flags.push(`${p.statusNotes.length} not`);
  if (flags.length > 0) return `${flags.join(' · ')} — ayrıntı için aç.`;
  return "Pod listesi Thanos (kube-state, cAdvisor) ve span'lerin gördüğü pod'lardan; ayrıntı için aç.";
}

export function InfraSourceNote(p: InfraSourceNoteProps) {
  const [open, setOpen] = useState(false);
  return (
    <CollapsedSection id="infra-source" title="Kaynak" open={open} onOpenChange={setOpen}
      summary={infraSourceSummary(p)}>
      <ul className="infra-src">
        <li>Thanos · kube-state / cAdvisor: pod durumu, restart, CPU, bellek. Eşleşme: <span className="mono">{p.match}</span>. {p.scanned}</li>
        <li>{p.entityEnabled
          ? <>Span&#39;ler (entity_seen_5m): pod&#39;un bu pencerede span ürettiğini gösterir; Thanos keşfinden bağımsızdır.</>
          : <>Entity katmanı kapalı: yalnız Thanos envanteri gösteriliyor.</>}</li>
        {p.entityEnabled && (
          <li>
            Pod kaynakları: <b>{p.counts.both}</b> pod iki kaynakta da (E+T), <b>{p.counts.entity}</b> yalnız span&#39;lerde (E),{' '}
            <b>{p.counts.thanos}</b> yalnız Thanos&#39;ta (T). Rozet &quot;Tüm kolonlar&quot;daki Kaynak kolonunda.
          </li>
        )}
        {p.unmapped.length > 0 && (
          <li className="cell-warn">Remote Cluster kaydı olmayan span cluster değerleri: <span className="mono">{p.unmapped.join(', ')}</span> — bu pod&#39;lar Thanos ile birleşmez.</li>
        )}
        {p.ambiguous.length > 1 && (
          <li className="cell-warn">Aynı servis adı {p.ambiguous.length} cluster&#39;da görülüyor; Topbar Cluster seçicisiyle daraltılabilir.</li>
        )}
        {p.statusNotes.map(n => <li key={n} className="cell-warn">{n}</li>)}
        {p.truncated.length > 0 && (
          <li className="cell-warn">Thanos kısmi — <span className="mono">{p.truncated.join(', ')}</span>: sunucu en işlek 500 pod&#39;u döndürdü (topk tavanı); sakin pod&#39;lar listede olmayabilir.</li>
        )}
        {p.unreachable.length > 0 && (
          <li className="cell-err">Yanıt vermeyen Thanos cluster&#39;ları: <span className="mono">{p.unreachable.join(', ')}</span>.</li>
        )}
        {p.sourcesError && <li className="cell-err">Thanos kaynaklarına erişilemedi (önbellekteki cluster listesi kullanılıyor): {p.sourcesError}</li>}
        {p.entityError && <li className="cell-err">Entity katmanı okunamadı: {p.entityError}</li>}
      </ul>
    </CollapsedSection>
  );
}
