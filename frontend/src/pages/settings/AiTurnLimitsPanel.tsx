// AiTurnLimitsPanel.tsx — CoSRE kişi başı sohbet turu sınırı (paylaşılan
// yerel LLM'i korur). Kendi GET/PUT'u (/api/ai/turn-limits, admin + audit
// settings.ai_turn_limits.update). Admin ekranı: çevrilmez (TR).
// Boş kutu = varsayılan (60 / saat, 300 / 24 sa). Redis yokken sınır
// uygulanmaz (R0).
import { useEffect, useState, type FormEvent } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { CheckboxField, Field } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import type { AITurnLimits } from '@/lib/types';
import { FlashBox, SectionTitle, humanize } from './shared';

const CARD = 'mt-[18px] max-w-[640px] rounded-lg border border-(--border) bg-(--bg2) p-4';
const INTRO = 'mb-2.5 text-[length:var(--fs-sm)] text-(--text2)';

/** Kutu → sayı; boş = 0 (sunucu varsayılana çevirir); geçersiz → hata metni. */
function turnLimitField(v: string, label: string): number | string {
  const s = v.trim();
  if (s === '') return 0;
  const n = Number(s);
  if (!Number.isInteger(n) || n < 1 || n > 100000) return `${label}: 1–100000 arası tam sayı girin`;
  return n;
}

export function AiTurnLimitsPanel() {
  const [hour, setHour] = useState('');
  const [day, setDay] = useState('');
  const [disabled, setDisabled] = useState(false);
  const [exempt, setExempt] = useState(false);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  // Okuma React Query'den (effectFetch ratchet); form sunucu değeriyle bir kez dolar.
  const q = useQuery({ queryKey: ['settings', 'ai-turn-limits'], queryFn: () => api.aiTurnLimits(), retry: false });
  const loaded = q.isFetched;

  const apply = (c: AITurnLimits) => {
    setHour(String(c.perHour)); setDay(String(c.perDay)); setDisabled(!!c.disabled); setExempt(!!c.exemptAdmins);
  };
  useEffect(() => { if (q.data) apply(q.data); }, [q.data]);
  useEffect(() => { if (q.error) setFlash({ kind: 'err', text: humanize(q.error) }); }, [q.error]);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    const h = turnLimitField(hour, 'Saatlik');
    const d = turnLimitField(day, '24 saatlik');
    if (typeof h === 'string' || typeof d === 'string') { setFlash({ kind: 'err', text: typeof h === 'string' ? h : String(d) }); return; }
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putAITurnLimits({ perHour: h, perDay: d, disabled, exemptAdmins: exempt });
      apply(saved);
      setFlash({ kind: 'ok', text: saved.disabled ? 'Kaydedildi — sınır kapalı.' : `Kaydedildi — kişi başı ${saved.perHour} tur / saat, ${saved.perDay} tur / 24 sa.` });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const off = !loaded || busy;
  return (
    <form onSubmit={save} className={CARD}>
      <SectionTitle>CoSRE tur sınırı</SectionTitle>
      <div className={INTRO}>
        Paylaşılan modeli korumak için kişi başı sohbet turu sınırı. Arka plan SRE araştırmaları sayılmaz (kendi saatlik bütçeleri var).
        Sınırda model çağrılmaz; sohbet sınırı ve sıfırlanma süresini söyler. Redis yokken sınır uygulanmaz.
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Tur / saat" small hint="boş = 60" inputMode="numeric" value={hour}
          onChange={e => setHour(e.target.value)} disabled={off} placeholder="60" />
        <Field label="Tur / 24 saat" small hint="boş = 300" inputMode="numeric" value={day}
          onChange={e => setDay(e.target.value)} disabled={off} placeholder="300" />
      </div>
      <CheckboxField className="mt-2.5" label="Yöneticiler muaf" hint="admin rolündeki kullanıcılar sayılmaz"
        checked={exempt} disabled={off} onCheckedChange={v => setExempt(v)} />
      <CheckboxField className="mt-2.5" label="Sınırı kapat" hint="hiçbir tur sayılmaz"
        checked={disabled} disabled={off} onCheckedChange={v => setDisabled(v)} />
      <div className="mt-3">
        <ActionRow confirm={<Button type="submit" loading={busy} disabled={!loaded}>Kaydet</Button>} />
      </div>
      {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
    </form>
  );
}
