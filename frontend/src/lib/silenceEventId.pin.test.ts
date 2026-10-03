// v0.10.1042 (operatör: "Anomalide 'Mute' sonrası satır listeden düşsün") —
// bir OLAY için yazılan susturma O OLAYIN kimliğini taşır. Sunucu iyi biçimli
// olay kimliğini olduğu gibi saklar (anomaly_extra.go silenceFingerprint);
// `kind|pattern|service` gönderen olay yolu log_template_new / behavior_change
// türlerinde (desen = görüntü metni) olayla hiç eşleşmeyen bir parmak izi
// yazdırıyordu. Olaydan susturan HER yol gövdeyi ortak kurucudan alır.
// İstisna (bilerek): /anomalies canlı akışı (streams.tsx) olay değil, dedektör
// isabeti susturur — kimliği yok, düz anahtar gönderir; o türlerde (log_pattern,
// trace_op) sunucunun yeniden hesabı olay kimliğine eşittir.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8');

describe('v0.10.1042 — olay susturma gövdesi olay kimliğini taşır', () => {
  it('servis sayfası «Değil → sessize al» ortak kurucuyu kullanır, düz anahtar göndermez', () => {
    const src = read('../pages/service/Overview.tsx');
    expect(src).toContain('const silenceBody = anomalyEventSilenceBody(e, durationSec);');
    expect(src).toContain("createSilence.mutateAsync({ ...silenceBody, reason: 'operator: değil (servis sayfası)' })");
    expect(src).not.toMatch(/fingerprint:\s*silenceKey\(/);
  });
  // v0.10.1081 — inbox çekmecesi (ikinci tüketici) kalktı; tek tüketici
  // anomali detay sayfası, aynı ortak kurucudan.
  it('anomali detay sayfası ortak kurucudan', () => {
    expect(read('../features/anomalies/AnomalyEventDetail.tsx')).toContain('anomalyEventSilenceBody(');
  });
  it("Cmd-K olay kimliğini gönderir (öneri satırının id'si = olay kimliği)", () => {
    expect(read('./actions.ts')).toContain('fingerprint: picked.id,');
  });
});
