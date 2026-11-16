import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { Spinner, Empty } from '@/components/Spinner';
import { QueryErrorInline } from '@/components/QueryError';
import { tsLong } from '@/lib/utils';
import { summarizeNotifyRouting } from '@/lib/notifyRouting';
import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2286 — metinler arayüz dilinde (TR aynı)

// ProblemNotifyPanel — v0.9.1344. "Bu problemden kimin haberi var?"
//
// OPERATÖR RAPORU: hiçbir bildirim kanalıyla eşleşmeyen bir problem
// sessizce düşüyordu ve operatörün bunu ÖĞRENMESİNİN hiçbir yolu yoktu.
// /events tüm defteri gösteriyor ama triyaj eden kişi orada değil —
// açık problemin sayfasında. Cevabın olması gereken yer burası.
//
// ÜÇ HÂL, ÜÇ AYRI ÇİZİM (ikisini birleştirmek yalan olurdu):
//   • işaret var        → KIRMIZI. Yol teklif edildi, kimse almadı.
//     Gerekçe haberin NEREDE kaybolduğunu söyler.
//   • gönderim var      → kime gittiği, sonucuyla.
//   • hiçbiri yok       → "bildirim yok" — KUSUR İDDİASI DEĞİL.
//     Yapılandırılmamış bir kurulumda backend işaret yazmaz, o yüzden
//     boş liste "kimse haber almadı" diye okunmamalı.
//
// Talep üzerine çekilir (problem sayfası açılışı), POLLING YOK: bildirim
// geçmişi bir problemin ömrü boyunca birkaç satır değişir.
export function ProblemNotifyPanel({ problemId }: { problemId: string }) {
  const lang = useLang();
  const q = useQuery({
    queryKey: ['problem-notifications', problemId],
    queryFn: () => api.problemNotifications(problemId),
    enabled: !!problemId,
    // Sunucu tarafı 15sn önbellekli; altına inmek boşuna istek.
    staleTime: 15_000,
  });

  if (q.isLoading) return <Spinner />;
  // Hata BOŞ SONUÇ DEĞİL: sessizce "bildirim yok" çizmek, tam da bu
  // sürümün kapattığı sessiz-kayıp sınıfını geri açardı.
  if (q.isError) {
    return <QueryErrorInline text={t('pnotify.err', lang)}
      onRetry={() => void q.refetch()} />;
  }

  const s = summarizeNotifyRouting(q.data);

  if (!s.unmatched && s.sends.length === 0) {
    return (
      <Empty icon="✉" title={t('pnotify.empty', lang)}>
        {t('pnotify.empty.body', lang)}
      </Empty>
    );
  }

  return (
    <>
      {s.unmatched && (
        <div style={{
          marginBottom: s.sends.length > 0 ? 10 : 0,
          padding: '8px 10px', borderRadius: 'var(--radius-sm)',
          background: 'var(--err-soft, var(--bg2))',
          border: '1px solid var(--err, var(--border))',
        }}>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 8 }}>
            <span className="badge b-err">{t('pnotify.unmatched', lang)}</span>
            <span style={{ fontSize: 11, color: 'var(--text3)' }}>
              {tsLong(s.unmatched.sentAt)}
            </span>
          </div>
          {s.unmatched.error && (
            <div style={{
              fontSize: 11.5, color: 'var(--text2)', marginTop: 6, lineHeight: 1.6,
              overflowWrap: 'anywhere',
            }}>
              {s.unmatched.error}
            </div>
          )}
          <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 6 }}>
            {t('pnotify.unmatched.hint', lang)}
          </div>
        </div>
      )}
      {s.sends.length > 0 && (
        <div style={{ fontSize: 11.5, lineHeight: 1.8 }}>
          <div style={{ color: 'var(--text3)', marginBottom: 4 }}>
            {s.okCount > 0 && <>{tf('pnotify.ok', lang, { n: s.okCount })}</>}
            {s.okCount > 0 && s.failCount > 0 && ' · '}
            {/* Teslimat arızası AYRI bir sinyal: kanal doğru eşleşti ama
                hedef kabul etmedi. Yönlendirme kusuruyla aynı kovaya
                koymak ikisinin de düzeltmesini gizlerdi. */}
            {s.failCount > 0 && <span className="err">{tf('pnotify.fail', lang, { n: s.failCount })}</span>}
          </div>
          {s.sends.map(n => (
            <div key={n.id} style={{ display: 'flex', gap: 8, alignItems: 'baseline' }}>
              {/* v0.10.929 (K5) — sistemin gönderimi normal sonuç (kullanıcı eylemi değil): nötr. */}
              <span className={n.ok ? 'badge b-gray' : 'badge b-err'} title={n.error || undefined}>
                {t(n.ok ? 'pnotify.sent' : 'pnotify.failed', lang)}
              </span>
              <span style={{ color: 'var(--text2)' }}>{n.channelName}</span>
              <span className="mono" style={{
                color: 'var(--text3)', fontSize: 11, overflow: 'hidden',
                textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: 260,
              }} title={n.target}>{n.target || '—'}</span>
              <span style={{ marginLeft: 'auto', color: 'var(--text3)', fontSize: 11, whiteSpace: 'nowrap' }}>
                {tsLong(n.sentAt)}
              </span>
            </div>
          ))}
        </div>
      )}
    </>
  );
}
