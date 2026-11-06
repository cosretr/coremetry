// HistogramMarkers — v0.10.1895 (Log Search Faz 4 dilim 4.1). Histogramın
// altında işaretlerin METİN karşılığı: grafikteki şerit etiketleri canvas'ta
// (ekran okuyucu görmez, isabet şeridi ince); burada her işaret simge + tür +
// saat taşıyan bir düğme, tık = pencereyi olayın çevresine daralt (grafik
// şeridine tıkla aynı). İşaret yoksa hiçbir şey çizilmez — bağlam süsü, panel
// değil (ServiceAnnotationLane emsali). Çok işarette ilk 6 düğme + "+N".
import { Button } from '@/components/shadcn/button';
import { LOGS_MARKER_META, logsMarkersSummary, type LogsMarker } from '@/lib/logsMarkers';
import { tsShort } from '@/lib/utils';

export interface HistogramMarkersProps {
  markers: LogsMarker[];
  onPick: (m: LogsMarker) => void;
  /** Sunucu 500 olay tavanına çarptı. */
  truncated?: boolean;
}

const SHOWN = 6;

const KIND_TEXT: Record<LogsMarker['kind'], string> = {
  rollout: 'text-muted-foreground',
  anomaly: 'text-status-warn',
  problem: 'text-status-critical',
};

export function HistogramMarkers({ markers, onPick, truncated }: HistogramMarkersProps) {
  if (markers.length === 0) return null;
  // En yeniler önce (operatörün aradığı genelde son değişiklik).
  const shown = markers.slice(-SHOWN).reverse();
  const rest = markers.length - shown.length;
  return (
    <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground" data-testid="logs-markers">
      <span>İşaretler: {logsMarkersSummary(markers)}{truncated ? ' (tavan)' : ''}</span>
      {shown.map(m => {
        const meta = LOGS_MARKER_META[m.kind];
        const when = tsShort(m.tsNs).slice(0, 8);
        return (
          <Button key={m.key} variant="outline" size="xs" data-kind={m.kind}
            title={`${meta.label} · ${tsShort(m.tsNs)}${m.text ? ` · ${m.text}` : ''} — tıkla: pencereyi bu olayın çevresine daralt`}
            onClick={() => onPick(m)}>
            <span aria-hidden="true" className={KIND_TEXT[m.kind]}>{meta.glyph}</span>
            <span>{meta.label} {when}</span>
          </Button>
        );
      })}
      {rest > 0 && <span>+{rest} daha (grafikte)</span>}
    </div>
  );
}
