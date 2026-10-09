import { useEffect, useMemo, useRef, useState } from 'react';
import { mergeOpenHref } from '@/lib/openHref'; // v0.10.460
import { useNavigate } from 'react-router-dom';
import { DrawerSection } from '@/components/ui/Drawer';
import { CopilotExplain } from '@/components/CopilotExplain';
import { Button } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { aiSubjectSubtitle, aiSubjectTitle, formatAiParam, type AISubject } from '@/lib/aiSubject';
import { emitAiEvidence, emitAiFocus, scrollToAttr } from './aiEvents';
import { ChatBubble } from './ChatBubble';
import { ServiceChartsExplainBody } from './ServiceChartsExplainBody';
import { aiSubjectQuestion, buildExplainContext, drawerFollowups } from './drawerChat';
import { useChatThread } from './useChatThread';
import { useStickToBottom } from './stickToBottom';
import { ChatComposer } from './ChatComposer'; // v0.10.1145 — CoSRE penceresiyle AYNI composer
import { useComposerDraft } from './composerDraft'; // v0.10.1145 — özne başına taslak
import { useCopilotConfig } from './useCopilotEnabled';
import { useAuthUserId } from '@/components/AuthProvider';
import { useChatProfile } from './chatProfileStore'; // v0.10.1138 — kullanıcı başına seçim
import { ModelPicker } from './ModelPicker'; // v0.10.1138 — kompakt "model ▾" menüsü
import { capPageContext, traceChatWindow, traceContextToPage, type TraceAiContext } from '@/lib/traceAiContext';
import type { AiConversation, PageContext } from '@/lib/types';

// AIDrawerBody — v0.10.483 (operatör, üçüncü kez: "Explain trace ile CoSRE
// iki ayrı drawer olarak çalışıyor. Hepsi Explain trace gibi olsun"): ✨
// Explain GÖVDESİ artık kendi çekmecesini açmıyor — CoSRE çekmecesi
// (CopilotChat) `?ai=<kind>:<id>` gelince AYNI kabuğun içinde bu gövdeyi
// çizer (açıklama + kanıt + çekmece sohbeti). Tek kabuk, tek başlık, tek
// ✕. Eski AIDrawer.tsx yalnız CopilotChat'e delege eden ince bir sarmalayıcı.
// Gövde/sohbet mantığı v0.9.477-v0.10.460 ile bayt-bayt aynı; yalnız
// dosya değişti.

// v0.10.944 (CoSRE Faz A) — iki yeni girdi, ikisi de kabuktan (CopilotChat):
//   traceCtx — trace öznesinin bağlamı (sayfanın canlı yayını ya da kayıtlı
//              anlık görüntü); sohbet her turda page/env/pencere olarak taşır;
//   resume   — geçmişten açılan ÖZNELİ konuşma: çekmece sohbeti onu
//              devralır (aynı kimliğe yazmaya devam eder), BİR KEZ
//              (onResumed kabuğa "tüketildi" der; yeniden mount eski
//              görüntüyü sunucudaki yeni turların üstüne basmasın).
export function AIDrawerBody({ subject, onClose, traceCtx, resume, onResumed }: {
  subject: AISubject;
  onClose: () => void;
  traceCtx?: TraceAiContext | null;
  resume?: AiConversation | null;
  onResumed?: (id: string) => void;
}) {
  const [spanIds, setSpanIds] = useState<string[]>([]);
  const [traceIds, setTraceIds] = useState<string[]>([]);
  // v0.9.479 — açıklamanın metni: çekmece-içi sohbetin BAĞLAMI.
  const [explainText, setExplainText] = useState('');
  // v0.10.944 — geçmişten açılan özneli konuşma sohbeti AÇIK tutar: kayıtlı
  // turlar yalnız AIDrawerChat'te çizilir ve o eskiden yalnız açıklama
  // BAŞARIYLA gelince mount ediliyordu — açıklama hata/boş dönerse konuşma
  // Geçmiş'ten hiç görüntülenemiyordu; "Yeniden sor" (onAnswer('')) da
  // devralınmış sohbeti söküp sessizce yeni bir konuşma kimliği açıyordu.
  // Bayrak YAPIŞKAN ve render'da türer: kabuk `resume`u tüketince null'lar
  // (ona bağlamak sohbeti devraldığı an söker); aynı özne açıkken (aynı
  // key, yeniden mount yok) gelen geçmiş satırında da çalışır. Açıklama
  // sonra gelirse `explain` memo'su güncellenir, sohbet yerinde kalır.
  const [resumed, setResumed] = useState(false);
  if (resume && !resumed) setResumed(true);

  // v0.9.1033 — `charts` öznesinin gövdesi AYRI: bu yüzey düz metin
  // değil, anlatım + YAPISAL sinyal tablosu + pivot linkleri döndürüyor
  // (onaylı ServiceCharts AI mockup'ı). CopilotExplain yalnız
  // `{explanation}` çizdiği için ona bir dal EKLENMEDİ — kanıt/anlatım
  // ayrımı bu bileşenin sözleşmesi. Sohbet bölümü ve `key` ile state
  // sıfırlama aynen paylaşılıyor: ikinci bir çekmece kabuğu YOK.
  if (subject.kind === 'charts') {
    return (
      <div style={{ paddingTop: 8 }}>
        <ServiceChartsExplainBody
          service={subject.id} fromNs={subject.fromNs} toNs={subject.toNs}
          scope={subject.scope} onAnswer={setExplainText} />
        {(explainText || resumed) && (
          <AIDrawerChat subject={subject} explainText={explainText} resumed={resumed}
            spanIds={[]} traceIds={[]} resume={resume} onResumed={onResumed} />
        )}
      </div>
    );
  }

  return (
    <div style={{ paddingTop: 8 }}>
      <CopilotExplain
        auto
        kind={subject.kind}
        id={subject.id}
        // v0.10.1065 — trace öznesinin seçili span'i artık açıklamaya gitmez
        // (v0.10.948 inceleme odağıydı; v0.10.1036'dan beri sunucu yok sayıyordu).
        // Seçili span takip sohbetinin bağlamında kalır (traceCtx → AIDrawerChat).
        spanId={subject.kind === 'span' ? subject.spanId : undefined}
        fromNs={subject.kind === 'service-health' ? subject.fromNs : undefined}
        toNs={subject.kind === 'service-health' ? subject.toNs : undefined}
        // v0.9.408 / v0.9.414 kanıt sözleşmesi: çekmece kanıtı sayfaya duyurur —
        // waterfall satırları ve exception örnek-trace satırları `.wf-evidence`
        // ile kutulanmaya devam eder (çekmece kapansa da kutular kalır).
        // v0.10.1033 (operatör: "Kanıt span'lere gerek yok") — span kimlikleri
        // artık çekmecede LİSTELENMEZ (ham hex satırlar, kimse okumuyordu); yalnız
        // waterfall'ı kutular ve takip sohbetinin bağlamına girer (spanIds →
        // AIDrawerChat → buildExplainContext). Kanıt trace'leri listesi aşağıda kalır.
        onEvidence={ids => { setSpanIds(ids); emitAiEvidence({ spanIds: ids }); }}
        onEvidenceTraces={ids => { setTraceIds(ids); emitAiEvidence({ traceIds: ids }); }}
        onAnswer={setExplainText}
      />

      {traceIds.length > 0 && (
        <div style={{ marginTop: 16 }}>
          <DrawerSection title={`Kanıt trace'leri (${traceIds.length})`}>
            {traceIds.map(id => (
              <EvidenceRow key={id} id={id}
                title="Örnek trace satırına git"
                onClick={() => {
                  onClose();
                  emitAiFocus({ traceId: id });
                  scrollToAttr('data-trace-id', id);
                }} />
            ))}
          </DrawerSection>
        </div>
      )}

      {/* Sohbet devamı (v0.9.479) — açıklama geldiyse ya da (v0.10.944)
          geçmişten devralınan konuşma varsa. Global CoSRE penceresi
          AÇILMAZ: cevap burada, aynı çekmecede sürer. */}
      {(explainText || resumed) && (
        <AIDrawerChat subject={subject} explainText={explainText} resumed={resumed}
          spanIds={spanIds} traceIds={traceIds}
          traceCtx={traceCtx} resume={resume} onResumed={onResumed} />
      )}
    </div>
  );
}

// AIDrawerChat — çekmece içindeki sohbet bölümü (v0.9.479, operatör
// raporu: "Chat'te devam et" global pencereyi çekmecenin ÜSTÜNE açıyordu
// ve sohbet ekrandaki açıklamayı bilmiyordu).
//
// İkinci bir chat implementasyonu YOK: tur state'i + gönderme döngüsü
// useChatThread, balon çizimi ChatBubble (adım çipleri, RAG kaynakları,
// derin linkler, kopyala, 👍/👎 hepsi bedelsiz gelir). Bu bileşen yalnız
// KABUK: aç/kapa, çipler, composer.
//
// Bağlam devri iki parça (drawerChat.ts):
//   seed  → öznenin doğal sorusu, konuşmanın ilk (çizilmeyen) turu;
//           sunucudaki takip-devralma bunu "önceki soru" sayar.
//   explain → `context.explain`; sunucu narration bloğuna katar ve
//           özneye oturmayan guided rotayı bastırır.
function AIDrawerChat({ subject, explainText, resumed = false, spanIds, traceIds, traceCtx, resume, onResumed }: {
  subject: AISubject;
  explainText: string;
  /** v0.10.944 — geçmişten devralınan konuşma: açıklama boşken de açık kalır. */
  resumed?: boolean;
  spanIds: string[];
  traceIds: string[];
  traceCtx?: TraceAiContext | null;
  resume?: AiConversation | null;
  onResumed?: (id: string) => void;
}) {
  // v0.10.82 (operatör isteği: "Chat'te devam et demesine gerek yok,
  // kullanıcı isterse hemen yazabilsin"). `open` kapısı kaldırıldı —
  // salt aşamalı-gösterimdi ve bir tık vergisiydi: composer'ın mount'u
  // BEDAVA (useChatThread yalnız send'de istek atar), yani kapının
  // koruduğu hiçbir maliyet yoktu.
  const endRef = useRef<HTMLDivElement>(null);

  const explain = useMemo(
    () => buildExplainContext({ subject, text: explainText, spanIds, traceIds }),
    [subject, explainText, spanIds, traceIds],
  );
  const seed = useMemo(
    () => [{ role: 'user' as const, text: aiSubjectQuestion(subject.kind, subject.id) }],
    [subject],
  );
  // v0.9.482 — öznenin KENDİSİ de tele gider: sunucu bundan ilgili
  // explain'in HAM KANITINI (trace span'leri + ilişkili loglar, exception
  // paketi) yeniden kurup anlatıma katar. Operatör raporu: "logda ne
  // yazıyor" gibi takipler açıklamanın metninde geçmediği için kör
  // cevaplanıyordu. `?ai=` kodeğinin AYNI biçimi — ikinci bir sözleşme yok.
  const subjectParam = useMemo(() => formatAiParam(subject), [subject]);
  // v0.10.1145 — taslak özne başına sessionStorage'da (çekmece kapanıp aynı
  // özne yeniden açılınca / yenilemede yazılan soru kaybolmaz; gönderimde silinir).
  const { value: input, setValue: setInput } = useComposerDraft('explain:' + subjectParam, null);
  // title (v0.10.55) — ÖZNEDEN türer, takip sorusunun lafından değil:
  // "Geçmiş" listesinde "Explain trace · a1b2c3d4…" gibi tanınabilir
  // dursun (gerekçe useChatThread.ts dosya başında).
  const persistTitle = useMemo(
    () => `${aiSubjectTitle(subject)} · ${aiSubjectSubtitle(subject)}`,
    [subject],
  );
  // service-health öznesinde sayfa bağlamı da geçer: guided router
  // servisi mesajda bulamazsa bunu varsayılan alır (v0.9.164 sözleşmesi).
  // v0.10.183 — model profili seçici (çoklu model dilim C): >1 profil varsa
  // görünür; boş = sunucu varsayılanı / yüzey eşlemesi. Çekmece ömrü kadar
  // yaşar (URL/kalıcılık yok — operatör: "kalıcı olmasın" sınıfı).
  // v0.10.1138 — seçim artık kullanıcı başına KALICI (CoSRE penceresiyle ortak,
  // chatProfileStore); liste yalnız rolüne açık profiller (sunucu süzer).
  const cfgP = useCopilotConfig(true);
  const userId = useAuthUserId();
  const cfgProfiles = useMemo(() => cfgP?.profiles ?? [], [cfgP]);
  const { profile, setProfile, activeModel } = useChatProfile(userId, cfgProfiles, cfgP?.defaultProfile, cfgP?.model);
  const navigate = useNavigate(); // v0.10.445 — "sayfasını aç" çekmece sohbetinde de gezer

  // v0.10.944 (CoSRE Faz A) — trace öznesinde HER TURDA bağlam: `trace`
  // (özne kimliği), `env` (odak span'in ortamı), `page` (PageContext:
  // traceId/spanId/servis/env/cluster/namespace/pod + trace penceresi mutlak
  // custom aralık) ve rangeS/toMs = trace penceresi ±5 dk. Sunucunun serbest
  // döngüsü `page`i zaten okuyor (agentctx.PreambleTR); çekmece kademesinin
  // kullanması Faz B. Bağlam henüz yoksa (sayfa dışı açılış) yalnız trace
  // kimliği gider — tahmin YOK. Diğer özneler bayt-bayt eski davranışta.
  const isTrace = subject.kind === 'trace';
  const page = useMemo<PageContext | undefined>(() => {
    if (!isTrace) return undefined;
    return traceCtx ? traceContextToPage(traceCtx) : { page: 'trace', path: '/trace', traceId: subject.id };
  }, [isTrace, traceCtx, subject.id]);
  // Bitiş şimdiye kırpılır (traceChatWindow); `now` yalnız bağlam değişince
  // okunur — render başına değil.
  const win = useMemo(() => (isTrace && traceCtx ? traceChatWindow(traceCtx, Date.now()) : null), [isTrace, traceCtx]);
  // Kalıcı anlık görüntü (≤2 KB): geçmişten açılınca şerit bunu gösterir.
  const persistContext = useMemo(() => (page ? capPageContext(page) ?? undefined : undefined), [page]);

  const { turns, busy, send, retry, stop, last, showFollowups, adopt } = useChatThread({
    explain, seed, subject: subjectParam,
    onOpen: href => { const to = mergeOpenHref(href, window.location.pathname, window.location.search); if (to) navigate(to, { replace: true }); }, // v0.10.460
    service: subject.kind === 'service-health' ? subject.id : undefined,
    trace: isTrace ? subject.id : undefined,
    env: isTrace ? traceCtx?.env : undefined,
    page,
    rangeS: win?.rangeS,
    toMs: win?.toMs,
    profile: profile || undefined,
    onProfileRejected: () => setProfile(''), // v0.10.1138 — 400/403 → varsayılana dön
    // persist (v0.10.55, operatör ürün kararı) — çekmece sohbeti artık
    // global CoSRE penceresiyle AYNI arşive yazılıyor; kapatılan çekmece
    // "🕘 Geçmiş"ten yeniden açılabilir (gerekçe useChatThread.ts).
    persist: true, title: persistTitle,
    persistContext,
  });

  // v0.10.944 — geçmişten açılan özneli konuşmayı devral (bir kez). Akış
  // sürerken adopt reddeder; o durumda tüketildi DENMEZ, bir sonraki
  // render yeniden dener.
  const adoptedRef = useRef('');
  useEffect(() => {
    if (!resume || adoptedRef.current === resume.id) return;
    if (adopt(resume)) {
      adoptedRef.current = resume.id;
      onResumed?.(resume.id);
    }
  }, [resume, adopt, onResumed, busy]);

  // v0.10.650 — yalnız dipteyken yapış; kaydırma kabı çekmecenin gövdesi (findScrollParent).
  const pinBottom = useStickToBottom(endRef, [turns]);

  const submit = (text: string) => { setInput(''); pinBottom(); void send(text); };

  // Bağlam kurulamadıysa (yalnız-boşluk cevap) sohbeti hiç açma —
  // bağlamsız sohbet operatör raporundaki hatanın ta kendisiydi.
  // v0.10.944 — devralınan konuşma istisna: bağlamı özne (`subject`, sunucu
  // ham kanıtı yeniden kurar) + kayıtlı turlar + trace'te page/env/pencere.
  if (!explain && !resumed) return null;

  // Çipler: sunucu rotadan öneri gönderdiyse onlar (v0.9.411), yoksa
  // özneye göre üretilen liste — global chat'in filo çipleri burada
  // konu dışı kalırdı.
  const chips = last?.suggestions?.length ? last.suggestions : drawerFollowups(subject);
  const showChips = turns.length === 0 || showFollowups;

  return (
    <div style={{ marginTop: 16 }}>
      <DrawerSection title="Sohbet">
        <div style={{ fontSize: 11, color: 'var(--text3)', marginBottom: 8, display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <span>{explain
            ? 'Bu sohbet yukarıdaki açıklamayı bilir — takip sorusu sorabilirsin.'
            // v0.10.944 — açıklama yokken "açıklamayı bilir" demek yalan olurdu.
            : 'Kayıtlı konuşma — açıklama henüz yok; takip sorusu özneyi ve önceki turları taşır.'}</span>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {turns.map((t, i) => <ChatBubble key={i} turn={t} onRetry={i === turns.length - 1 && t.error && !busy ? retry : undefined} />)}
          <div ref={endRef} />
        </div>

        {showChips && (
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginTop: turns.length ? 8 : 0 }}>
            {chips.map(q => (
              <Chip key={q} pill onClick={() => submit(q)} disabled={busy}>↳ {q}</Chip>
            ))}
          </div>
        )}

        <form className="cm-composer cm-composer--inline"
          onSubmit={e => { e.preventDefault(); submit(input); }}>
          {/* v0.10.664 — <textarea> (Enter gönder, Shift+Enter satır); akarken kilitli değil.
              v0.10.1145 — ChatComposer: CoSRE penceresiyle aynı araç çubuğu / kısayol /
              akıllı yapıştırma / önizleme (tek composer, iki kabuk). */}
          <ChatComposer
            value={input}
            onChange={setInput}
            onSubmit={() => submit(input)}
            autoFocus
            placeholder="Bu konuda sor… (Shift+Enter: yeni satır)"
            ariaLabel="Bu konuda CoSRE'ye sor"
            actions={<>
          {/* v0.10.1141 — model seçici composer'ın içinde, Gönder'in solunda (CopilotChat
              ile aynı yer); akarken devre dışı; tek profilde tıklanamaz etiket. */}
          <ModelPicker profiles={cfgProfiles} defaultProfile={cfgP?.defaultProfile} value={profile}
            onChange={setProfile} activeModel={activeModel} disabled={busy} />
          {/* v0.10.948 — DURDUR çekmecede de: trace takip soruları artık tam araç döngüsünü (≤5 tur/6 çağrı) koşuyor; akarken Gönder'in YERİNİ alır (CopilotChat v0.10.23 ile aynı). */}
          {busy ? (
            <Button variant="secondary" type="button" onClick={stop}
              title="Cevabı durdur — o ana kadar akan metin korunur">
              Durdur
            </Button>
          ) : (
            <Button variant="primary" type="submit" disabled={!input.trim()}>
              Gönder
            </Button>
          )}
            </>} />
        </form>
      </DrawerSection>
    </div>
  );
}

// Kanıt satırı — sayfadaki kutulanmış satırla AYNI görsel dil (.wf-evidence),
// böylece çekmecedeki liste ile exception'ın kutulu örnek-trace satırı aynı
// şeyi anlatır. v0.10.1033 — yalnız "Kanıt trace'leri" kullanır (span listesi
// kaldırıldı; span kanıtı waterfall'daki kutunun kendisi).
// v0.10.924 — buton bütünlüğü Faz 2: `div role=button` + elle Enter/Space →
// gerçek ghost Button (klavye yerleşik). Atom çocukları `.row` flex'ine
// sardığı için kırpma (ellipsis) id'nin kendi span'inde.
function EvidenceRow({ id, title, onClick }: { id: string; title: string; onClick: () => void }) {
  return (
    <Button variant="ghost" size="sm" className="wf-evidence mono"
      title={title}
      onClick={onClick}
      rightIcon={<span style={{ color: 'var(--text3)' }}>→</span>}
      style={{ display: 'block', width: '100%', marginBottom: 4 }}>
      <span style={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{id}</span>
    </Button>
  );
}
