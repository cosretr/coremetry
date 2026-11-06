// MLRecommendedCard — v0.10.1886 (operatör 2026-11-06: "Settings'te anomali
// ayarları çok karmaşık, sen default kur"; VM'de HTTP, gRPC ve Kafka var).
// Tek düğme: sunucu VictoriaMetrics'te hangi kaynağın bulunduğunu ölçer ve
// verisi olan dedektörleri açan kurulumu önerir. Ölçüm YALNIZ düğmeye basınca
// koşar (sayfa açılışında VM'e sorgu yok). "Uygula" bildik PUT /api/admin/ml'i
// çağırır (audit sunucuda); log dedektörlerine, terfiye ve eşiklere dokunulmaz.
import { useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { api } from '@/lib/api';
import { joinTr, mlRecommendView } from '@/lib/mlRecommend';
import type { MLConfig, MLRecommended } from '@/lib/types';
import { FlashBox, humanize } from './shared';

const LINE = 'm-0 text-[length:var(--fs-sm)] leading-[1.55] text-(--text2)';

export function MLRecommendedCard({ labelOf, onApplied }: {
  labelOf(detector: string): string;
  onApplied(cfg: MLConfig): void;
}) {
  const [rec, setRec] = useState<MLRecommended | null>(null);
  const [busy, setBusy] = useState<'measure' | 'apply' | null>(null);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const measure = async () => {
    setBusy('measure'); setFlash(null);
    try {
      setRec(await api.getMlRecommended());
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(null);
    }
  };
  const apply = async () => {
    if (!rec) return;
    setBusy('apply'); setFlash(null);
    try {
      onApplied(await api.putMl(rec.config));
      setRec(null);
      setFlash({ kind: 'ok', text: 'Önerilen kurulum uygulandı — bir sonraki ML tikinde geçerli, restart gerekmez.' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(null);
    }
  };
  const v = rec ? mlRecommendView(rec, labelOf) : null;

  return (
    <section aria-label="Önerilen kurulum" data-testid="ml-recommended" className="mb-4 grid gap-2 rounded-md border border-border px-3 py-2.5">
      <h3 className="m-0 text-[length:var(--fs-md)] font-semibold">Önerilen kurulum</h3>
      <p className={LINE}>
        Ayarlarla tek tek uğraşmak gerekmez: VictoriaMetrics&apos;te hangi metriklerin bulunduğu ölçülür,
        verisi olan dedektörler açılır, verisi olmayanlar kapatılır. Olaylar gölge olarak kaydedilir; Problem açmaz.
      </p>
      {v && (
        <div className="grid gap-1" data-testid="ml-recommended-result">
          <p className={`${LINE} font-semibold text-(--text)`}>{v.summary}</p>
          {v.present.length > 0 && <p className={LINE}>VictoriaMetrics&apos;te var: {joinTr(v.present)}.</p>}
          {v.absent.length > 0 && <p className={LINE}>Bulunamadı: {joinTr(v.absent)}.</p>}
          {v.unknown.length > 0 && rec?.vmConfigured && (
            <p className={LINE}>Ölçülemedi (dokunulmaz): {joinTr(v.unknown)}.</p>
          )}
          {v.turnOn.length > 0 && <p className={LINE}>Açılacak: {joinTr(v.turnOn)}.</p>}
          {v.turnOff.length > 0 && <p className={LINE}>Kapanacak: {joinTr(v.turnOff)}.</p>}
          <p className="m-0 text-[length:var(--fs-xs)] leading-normal text-(--text3)">
            Log dedektörlerine (Elasticsearch), &quot;Problem&apos;e terfi&quot; anahtarlarına ve eşiklere dokunulmaz.
          </p>
        </div>
      )}
      <div className="flex flex-wrap gap-2">
        <Button variant="outline" size="sm" onClick={measure} disabled={busy !== null}>
          {busy === 'measure' ? 'VictoriaMetrics ölçülüyor…' : rec ? 'Yeniden ölç' : 'Önerilen kurulumu göster'}
        </Button>
        {v?.canApply && (
          <Button size="sm" onClick={apply} disabled={busy !== null}>
            {busy === 'apply' ? 'Uygulanıyor…' : 'Uygula'}
          </Button>
        )}
      </div>
      {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
    </section>
  );
}
