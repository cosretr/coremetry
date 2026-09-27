import type { CSSProperties } from 'react';
import { DisclosureButton, Field, SelectField } from '@/components/ui';
import type { ArgoCDBound, ArgoCDPin } from '@/lib/types';
import {
  ADV_GROUPS, advError, advInputId, advSummary, fmtTr,
  type AdvNumKey, type Draft, type Issue, type MetricsOnlyMode,
} from './argocdForm';

// ArgoCDAdvancedPanel — v0.10.974 — kapalı "Gelişmiş" bölümü + kapalı, salt
// okunur "Elle eşlemeler (pins)" notu (mockup Main).
//
// Aralıklar ve varsayılanlar GET `bounds`tan (sunucunun intField tablosu —
// kod ve arayüz ayrışamaz); boş kutu = varsayılan (PUT'ta 0). Çapraz alanlar
// (out_of_band ≥ pencere, namespace güveni < ad güveni) ETKİN değerlerle,
// sunucunun hata yoluyla. Pin düzenleyicisi yok: burada yalnız sayı ve bağlı
// instance'lar; Kaydet pins[]'i olduğu gibi geri gönderir. v0.10.985 (P3.2):
// notun metni pinlerin NE ZAMAN kullanıldığını söyler (metrik işçisi açıkken
// eşleyici her mapperMin turunda; GitOps sekmesi işçi kapalıyken de uygular).
// v0.10.974 — pins notunun açıklığı sekmede (`pinsOpen`): sunucunun
// `pins[i]…` 400'ünün bağlantısı notu açıp başlığına odaklanır; kip seçimi
// `classification.metricsOnlyMode` 400'ünü alanın altında gösterir.

const H3: CSSProperties = { margin: 0, fontSize: 'var(--fs-md)', fontWeight: 600 };
const SUMMARY: CSSProperties = { minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 'var(--fs-sm)', color: 'var(--text3)' };
const FIELDSET: CSSProperties = { minWidth: 0, margin: 0, padding: 0, border: 0, display: 'flex', flexDirection: 'column', gap: 'var(--sp-4)' };
const LEGEND: CSSProperties = { padding: 0, marginBottom: 'var(--sp-3)', fontSize: 'var(--fs-sm)', fontWeight: 600, color: 'var(--text2)' };
const TEXT: CSSProperties = { margin: 0, maxWidth: 760, fontSize: 'var(--fs-sm)', lineHeight: '18px', color: 'var(--text2)' };

export function ArgoCDAdvancedPanel({ draft, bounds, pins, issues, open, onOpen, pinsOpen, onPinsOpen, onAdv, onMode }: {
  draft: Draft;
  bounds: Record<string, ArgoCDBound>;
  pins: ArgoCDPin[];
  issues: Issue[];
  open: boolean;
  onOpen: (open: boolean) => void;
  pinsOpen: boolean;
  onPinsOpen: (open: boolean) => void;
  onAdv: (key: AdvNumKey, value: string) => void;
  onMode: (mode: MetricsOnlyMode) => void;
}) {
  const byInstance = new Map<string, number>();
  for (const p of pins) byInstance.set(p.instanceId, (byInstance.get(p.instanceId) ?? 0) + 1);
  const pinsList = [...byInstance].map(([id, n]) => `${id} (${n})`).join(', ') || 'yok';
  const mode = draft.metricsOnlyMode || 'estimate';
  const modeIssue = issues.find(i => i.target.kind === 'advMode');

  return (
    <>
      <section aria-labelledby="acd-adv-h" className="stack gap-3">
        <div className="row gap-4">
          <h3 id="acd-adv-h" style={H3}>
            <DisclosureButton id="acd-adv-btn" expanded={open} aria-controls="acd-adv-body" onClick={() => onOpen(!open)}>Gelişmiş</DisclosureButton>
          </h3>
          <span style={SUMMARY}>{advSummary(draft, bounds)}</span>
        </div>
        {open && (
          <div id="acd-adv-body" className="card stack gap-4">
            <p style={TEXT}>Boş alan = varsayılan. Aralık dışı değer kaydedilmez (400, alan yolu ile). Okuyucu tavanları karar 2'den: ≤ 50.000 seri, ≤ 64 MiB, ≤ 45 sn.</p>
            <div className="grid-3 gap-6">
              {ADV_GROUPS.map(g => (
                <fieldset key={g.title} style={FIELDSET}>
                  <legend style={LEGEND}>{g.title}</legend>
                  {g.fields.map(f => {
                    const b = bounds[f.key];
                    const server = issues.find(i => i.target.kind === 'adv' && i.target.key === f.key);
                    const err = advError(draft.adv, f.key, bounds) || server?.message || '';
                    return (
                      <Field key={f.key} id={advInputId(f.key)} label={f.label} inputMode="decimal" autoComplete="off"
                        value={draft.adv[f.key]} error={err || undefined}
                        hint={b ? `${fmtTr(b.min)}–${fmtTr(b.max)} · varsayılan ${fmtTr(b.default)}` : undefined}
                        onChange={e => onAdv(f.key, e.target.value)} />
                    );
                  })}
                  {g.mode && (
                    <SelectField id="acd-adv-mode" label="tokenRef yokken" value={mode} error={modeIssue?.message}
                      hint={mode === 'unknown' ? 'Yalnız unknown; tahmin gösterilmez.' : 'Metriklerden “tahmini” argo_manual / argo_auto (karar 15).'}
                      onChange={e => onMode(e.target.value === 'unknown' ? 'unknown' : 'estimate')}>
                      <option value="estimate">estimate</option>
                      <option value="unknown">unknown</option>
                    </SelectField>
                  )}
                </fieldset>
              ))}
            </div>
          </div>
        )}
      </section>

      <section aria-labelledby="acd-pins-h" className="stack gap-3">
        <div className="row gap-4">
          <h3 id="acd-pins-h" style={H3}>
            <DisclosureButton id="acd-pins-btn" expanded={pinsOpen} aria-controls="acd-pins-body" onClick={() => onPinsOpen(!pinsOpen)}>
              Elle eşlemeler <span className="mono cell-faint">pins</span>
            </DisclosureButton>
          </h3>
          <span style={SUMMARY}>{pins.length} kayıt (API ile yazıldı) · GitOps sekmesi ve eşleyici kullanır</span>
        </div>
        {pinsOpen && (
          <p id="acd-pins-body" style={TEXT}>
            Bir iş yükünü bir Application'a elle bağlar; elle bağlantı o iş yükü için ad tahminini devre dışı bırakır. Metrik işçisi
            (<code>argocd-metrics</code>) açıkken eşleyici pin'leri her turda (<code>intervals.mapperMin</code>){' '}
            <code>argocd_app_mapping</code>'e <code>match_method='manual'</code> olarak kopyalar; GitOps sekmesi işçi kapalıyken de pin'leri
            istek anında uygular. Düzenleyici yok — şimdilik yalnız API: <code>PUT /api/settings/argocd</code>{' '}
            gövdesinde <code>pins[]</code>. Kaydet <code>pins[]</code>'i olduğu gibi geri gönderir. Pin'i olan instance tablodan kaldırılamaz;
            önce API'den pin'leri kaldırın. Bağlı instance'lar: <span className="mono">{pinsList}</span>.
          </p>
        )}
      </section>
    </>
  );
}
