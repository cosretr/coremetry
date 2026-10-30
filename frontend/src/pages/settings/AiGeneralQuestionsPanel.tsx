// AiGeneralQuestionsPanel.tsx — v0.10.1458 (operatör kararı 2026-10-23: "Genel
// soruları açık bırakalım, anahtar da olabilir."). Ayarlar › CoSRE, sağlayıcı
// sekmesi: konu dışı (telemetri dışı) sorulara modelin kısa genel cevap verip
// vermeyeceği. GET /api/ai/general-questions her rol (viewer salt-okunur GÖRÜR),
// PUT admin + audit (sunucu kapısı esas — canEdit yalnız kutuyu kilitler).
// Varsayılan AÇIK (blob yokken sunucu allow=true döner). Kutu tıklanınca hemen
// kaydeder (AiToolsetsPanel'in aç/kapa duruşu); polling yok.
import { useEffect, useState } from 'react';
import { CheckboxField } from '@/components/coremetry/Field';
import { useAuthRole } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import { toast } from '@/lib/toast';
import { fmtDateTime } from '@/lib/utils';
import type { AIGeneralQuestions } from '@/lib/types';
import { SectionTitle, humanize } from './shared';

export function AiGeneralQuestionsPanel({ canEdit: canEditProp }: { canEdit?: boolean }) {
  const t = useT();
  const role = useAuthRole();
  const canEdit = canEditProp ?? role === 'admin';
  const [data, setData] = useState<AIGeneralQuestions | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const ctl = new AbortController();
    api.aiGeneralQuestions(ctl.signal)
      .then(d => setData(d))
      .catch(e => { if (!ctl.signal.aborted) setErr(humanize(e)); });
    return () => ctl.abort();
  }, []);

  const toggle = async (allow: boolean) => {
    setBusy(true);
    try {
      const next = await api.putAIGeneralQuestions(allow);
      setData(next);
      toast.success(t('aiGeneralQ.saved').replace('{state}', t(next.allow ? 'aiGeneralQ.on' : 'aiGeneralQ.off')));
    } catch (e) {
      toast.error(t('aiGeneralQ.saveFailed').replace('{err}', humanize(e)));
    } finally {
      setBusy(false);
    }
  };

  // v0.10.1553 — kaynak keşfi anahtarı (aynı blob; undefined = açık).
  const toggleDiscovery = async (on: boolean) => {
    setBusy(true);
    try {
      const next = await api.putAISourceDiscovery(on);
      setData(next);
      toast.success(t('aiSourceDisc.saved').replace('{state}', t(next.sourceDiscovery !== false ? 'aiGeneralQ.on' : 'aiGeneralQ.off')));
    } catch (e) {
      toast.error(t('aiGeneralQ.saveFailed').replace('{err}', humanize(e)));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div data-testid="ai-general-questions-panel" className="mt-[18px] max-w-[640px] rounded-lg border border-(--border) bg-(--bg2) p-4">
      <SectionTitle>{t('aiGeneralQ.title')}</SectionTitle>
      {err && <p className="field-hint" role="alert">{t('aiGeneralQ.loadFailed').replace('{err}', err)}</p>}
      <CheckboxField label={t('aiGeneralQ.label')} hint={t('aiGeneralQ.hint')}
        checked={data?.allow ?? false} disabled={!data || !canEdit || busy}
        onCheckedChange={v => { void toggle(v); }} />
      <CheckboxField id="ai-source-discovery" className="mt-2.5" label={t('aiSourceDisc.label')} hint={t('aiSourceDisc.hint')}
        checked={data ? data.sourceDiscovery !== false : false} disabled={!data || !canEdit || busy}
        onCheckedChange={v => { void toggleDiscovery(v); }} />
      {!canEdit && <p className="field-hint">{t('aiGeneralQ.readOnly')}</p>}
      {data?.updatedBy && data.updatedAt && (
        <p className="field-hint">
          {t('aiGeneralQ.lastChange').replace('{by}', data.updatedBy).replace('{at}', fmtDateTime(new Date(data.updatedAt)))}
        </p>
      )}
    </div>
  );
}
