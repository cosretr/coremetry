import { useEffect, useMemo, useRef, useState, useCallback } from 'react';
import { mergeOpenHref } from '@/lib/openHref'; // v0.10.460
import { Link, useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { useCopilotStarters } from '@/lib/queries/copilot'; // v0.10.702
import { api } from '@/lib/api';
import { pageContext } from '@/lib/pageContext';
import { takeMetricPanelAsk, usePageOverlay, withPageOverlay } from '@/lib/pageContextOverlay'; // v0.10.1435
import { hasPinnableContext, legacyFromPinned, pinLabelTR, readPin, writePin } from '@/lib/pinnedContext';
import type { ChatExplainAsk, ChatTurn, PageContext } from '@/lib/types';
import { Button } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { IconButton } from '@/components/ui/IconButton';
import { Drawer } from '@/components/ui/Drawer';
import { useConfirm } from '@/components/ui/ConfirmDialog';
import { useOpenCriticalCount } from '@/lib/queries';
import { useQuery } from '@tanstack/react-query';
import { useAuth } from '@/components/AuthProvider';
import { useUrlRange } from '@/lib/useUrlRange';
import { useUrlEnv } from '@/lib/useUrlEnv';
import { timeRangeToNs, tsRel } from '@/lib/utils';
import { contextStarter, serviceFromRoute } from '@/lib/chatContext';
import { syncChatParam } from '@/lib/chatUrl';
import { Empty, Spinner } from './Spinner';
import { ChatBubble } from './ai/ChatBubble';
import { TraceExplainNudge } from './ai/TraceExplainNudge';
import { useChatThread, type ChatSendOpts } from './ai/useChatThread';
import { FollowupChips } from './ai/FollowupChips'; // v0.10.1291 — kural-türevli takip çipleri
import { useStickToBottomState } from './ai/stickToBottom'; // v0.10.1137 — atBottom → "↓ En alta"
import { CosreSidebar } from './ai/CosreSidebar'; // v0.10.1137 — /cosre sol kenar çubuğu
import { readSidebarCollapsed, writeSidebarCollapsed } from './ai/chatHistoryGroups';
import { ChatComposer } from './ai/ChatComposer'; // v0.10.1145 — markdown bilen composer (araç çubuğu, kısayol, yapıştırma, önizleme)
import { useComposerDraft } from './ai/composerDraft'; // v0.10.1145 — konuşma başına taslak
import { useNameCompletion } from './ai/useNameCompletion'; // v0.10.687 — D4 ad tamamlama
import { NameCompletionPopup } from './ai/NameCompletionPopup';
import { applyCompletion, type CompletionItem } from './ai/chatCompletion';
import { ModelPicker } from './ai/ModelPicker'; // v0.10.1138 — kompakt "model ▾" menüsü
import { useChatProfile } from './ai/chatProfileStore'; // v0.10.1138 — kullanıcı başına seçim
import { useDeepThink } from './ai/deepThink'; // v0.10.1150 — "Derin düşün"
import { useCodeRead } from './ai/codeRead'; // v0.10.1461 — "Kod" hapı (bağımsız sohbet kod turu)
import { hasDrawerContext, parseChatInput, removeToken, scopeChips, scopeForRequest } from './ai/chatScope'; // v0.10.1138 — @/ kapsamı
import { ChatContextBar } from './ai/ChatContextBar'; // v0.10.1274 — aktif bağlam şeridi
import { applyContextExclusions, contextBarItems, shouldResetExclusions, type ChatRequestContext, type ContextKey } from './ai/chatContextScope';
import { answerVersion, canRegenerate, lastUserIndex } from './ai/chatRegenerate'; // v0.10.1138
import { cosreFullPageHref } from './ai/drawerFullPage'; // v0.10.1138 — "Tam sayfada aç"
import { isPlainLeftClick } from '@/lib/a11y';
import { toast } from '@/lib/toast'; // v0.10.1139 — tam sayfa kayıt hatası
import { useCopilotConfig } from './ai/useCopilotEnabled'; // v0.10.483
import { AI_PREFILL_EVENT, type AIPrefillDetail } from './ai/aiEvents'; // v0.10.1336 — tur: aç + doldur, gönderme
import { AI_DRAWER_WIDTH } from './ai/answerCard'; // v0.10.461
import { useAiSubject } from './ai/useAiSubject';
import { AI_CODE_PARAM, AI_SRC_PARAM, formatAiParam } from '@/lib/aiSubject';
import { activeExplainFrom, explainAskFor, explainAskSubject, explainContextLabel, explainFollowupContext, explainSubject, explainTurnSendOpts } from './ai/explainTurn'; // v0.10.1400; v0.10.1405; v0.10.1407 — AI_KINDS'in tümü
import { drawerFollowups } from './ai/drawerChat'; // v0.10.1400 — açıklama turunun takip çipleri
import { greetHello, greetErrors, greetHref, greetTooltip } from './ai/greeting';
import type { AiConversationSummary } from '@/lib/types';
import { traceChatWindow, traceContextToPage, useTraceAiContext, type TraceAiContext } from '@/lib/traceAiContext'; // v0.10.944; v0.10.1400 — açıklama turunun trace bağlamı
import { TraceContextStrip } from './ai/TraceContextStrip'; // v0.10.944
import { ChatLinkNewTabContext } from './ai/chatLinkTarget'; // v0.10.1125 — /cosre
import { ThemeToggle } from './ThemeToggle';
import { CosreCapabilities } from './ai/CosreCapabilities'; // v0.10.1128
import { tCosre } from './ai/capabilityHints';
import { ChatDashContext } from './ai/chatDash'; // v0.10.1443

// CopilotChat (v0.6.53, v0.9.163 interaktif) — global in-app AI assistant.
// Sağ-alt animasyonlu sparkline logo (operatör seçimi B) bir drawer açar;
// operatör telemetrisine grounded cevap veren gemma4 ile (lokal) sohbet eder.
// AppShell'de bir kez mount (CommandPalette gibi), her sayfada erişilir.
//
// v0.9.163: markalı sparkline logo + Türkçe quick-start/follow-up çipleri +
// hafif markdown (kalın/kod) + copy + streaming imleç. Konuşma efemer bileşen
// state'i; her send tüm geçmişi /api/copilot/chat'e postlar, SSE tüketir.
//
// v0.9.479: bu pencere FİLO GENELİ sorular için AYNEN kaldı. Tur state'i +
// gönderme döngüsü (useChatThread) ve balon çizimi (ChatBubble) ai/ altına
// çıkarıldı; AI çekmecesindeki özne-kapsamlı sohbet aynı çekirdeği kullanır.

// Türkçe quick-start (v0.9.163 — eskiden İngilizce'ydi, cevaplar Türkçe
// geliyor). v0.9.375 (operatör): SRE perspektifli katalog — her çip guided
// router'daki BİR intent'e eşlenir (problems / my_services / my_problems /
// slow_traces / log_errors / deploy_impact / service_health). Backend'in
// yanıtlayamadığı şekle çip koymuyoruz; pod/JVM intent'i gelince çipi de
// gelir (v0.9.376 adayı).
// Follow-up önerileri — cevaptan sonra sıradaki faydalı drill-down'lar.
const FOLLOWUPS = [
  'Açık problemlerin kök nedeni?',
  'Takımımın servisleri nasıl?',
  'En yavaş servisler?',
  // v0.9.652 (operatör: "son deploy etkisine gerek yok") — deploy
  // sorusu STATİK listeden çıktı. Rotaya bağlı follow-up'larda duruyor:
  // orada bir CEVABIN devamı, burada ise bağlamsız bir menü maddesiydi.
];

// v0.9.652 — BOŞ sohbetteki başlangıç çipleri (v0.9.579 "menü değil araç"
// gerekçesiyle üç çip: takımın servisleri / exception'ları / en hatalı
// servisleri). v0.10.1402 (operatör: "Alttaki takımımın servisleri
// exceptionları yukarıda olsun. Takım seçilebilsin.") — üçü gevşek çip
// satırından ÇIKTI, CosreCapabilities'in en üstteki Takımım kartına taşındı
// (takım seçici + aynı üç soru; capabilityHints TEAM_ASKS / teamCardQuestion).

// CoSRE markası — v0.10.498 (operatör: "direkt OpenTelemetry iconu olsun";
// teleskop mockup'ı reddedildi): resmî OpenTelemetry ikonu, repodaki
// /favicon.svg (CNCF artwork'ten birebir kopya, v0.9.495) — ikinci kopya
// yok, favicon güncellenince düğme de güncellenir. Eski gradient sparkline
// (v0.9.163, varyant B) ve .cm-ai-spark çizim animasyonu kaldırıldı.
function AiMark({ size = 26 }: { size?: number }) {
  return (
    <img src="/favicon.svg" width={size} height={size} alt="" aria-hidden="true" draggable={false}
      style={{ display: 'block', width: size, height: size }} />
  );
}

// v0.10.732 (operatör: "Kiosk modunda da Explain trace yapılabilsin") —
// `launcher={false}`: FAB, kritik-problem rozeti ve nudge baloncuğu
// çizilmez/pollanmaz; yalnız `?ai=` öznesiyle açılan çekmece kalır. Kiosk
// (kromsuz, salt-okunur pencere) çekmeceyi sayfa-yerel mount eder; kabuğun
// kiosk dalı hâlâ hiçbir krom bileşeni çizmez (appShellKiosk pinleri).
//
// v0.10.1125 (/cosre) — `variant="page"`: AYNI sohbet (aynı state, aynı
// başlık eylemleri, aynı gövde) çekmece yerine tam sayfa kabukta çizilir.
// Çatal yok: başlık ve gövde tek yerde kurulur, yalnız sarmalayıcı değişir.
// Sayfa kipinde pencere hep "açık" (geçmiş/karşılama/?chat= senkronu açık
// çekmece gibi işler), FAB çizilmez, iç linkler yeni sekmede açılır
// (chatLinkTarget) ve sunucunun `open` önerisi sohbeti terk ettirmez.
export function CopilotChat({ launcher: launcherProp = true, variant = 'drawer' }: { launcher?: boolean; variant?: 'drawer' | 'page' } = {}) {
  const isPage = variant === 'page';
  const launcher = launcherProp && !isPage;
  // v0.10.483 — config TEK kaynaktan (useCopilotConfig: modül cache'i, AIDrawer
  // ile aynı); eskiden CopilotChat kendi effect'iyle ikinci bir istek atıyordu.
  const cfg = useCopilotConfig(true);
  const enabled: boolean | null = cfg ? cfg.enabled : null;
  // v0.10.183 — model profili seçici (>1 profil); boş = sunucu varsayılanı.
  const profiles = useMemo(() => cfg?.profiles ?? [], [cfg]);
  const defaultProfile = cfg?.defaultProfile ?? '';
  // v0.10.461 — başlık meta şeridindeki model çipi (AIDrawer ile aynı anatomi).
  const model = cfg?.model ?? ''; // v0.10.483 — config tek kaynaktan (useCopilotConfig)
  // v0.10.1138 — seçim kullanıcı başına kalıcı (chatProfileStore); rozet etkin modeli gösterir.
  const { user } = useAuth();
  const { profile, setProfile, activeModel } = useChatProfile(user?.id, profiles, defaultProfile, model);
  const [deep, setDeep] = useDeepThink(user?.id); // v0.10.1150 — "Derin düşün" (kullanıcı başına, kalıcı)
  // v0.10.1461 — "Kod" hapı: yalnız DevOps bağlıyken (config.codeRead var) çizilir;
  // sunucu kod okuyamıyorsa (yerel profil yok) devre dışı + gerekçe, istek gövdesine gitmez.
  const [codePill, setCodePill] = useCodeRead(user?.id);
  const codeRead = cfg?.codeRead;
  const code = codePill && !!codeRead?.available;
  const [openState, setOpen] = useState(false);
  const open = isPage || openState; // v0.10.1125 — sayfa kipinde hep açık
  // v0.10.483 — ✨ Explain öznesi (`?ai=`). v0.10.1400/1405 — tek seferlik
  // niyet: aşağıdaki effect tüketir ve açıklamayı SOHBET TURU olarak gönderir.
  // v0.10.1407 (Tek CoSRE adım 4; operatör: "Trace ve exception üzerinden
  // çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE
  // bütünselliğine uymuyor.") — charts / log-spike da sohbet turu; çekmecenin
  // "özne kipi" (AIDrawerBody: ikinci thread + ikinci composer) SİLİNDİ. Tek
  // kabuk, tek thread: çekmece yalnız `open` ile açık.
  const [rawSubject, setSubject] = useAiSubject();
  const drawerOpen = open;
  const closeDrawer = () => { setSubject(null); setOpen(false); };
  const navigate = useNavigate(); // v0.10.434 (D7b) — "sayfasını aç" cevabı SPA içinde gezer
  // v0.9.169 — proaktif rozet: açık KRİTİK problem sayısı (chat kapalıyken
  // FAB'da kırmızı rozet). Yalnız copilot açıkken pollar; RQ tab gizliyken durur.
  const criticalOpen = useOpenCriticalCount({ enabled: enabled === true && launcher }).data ?? 0; // v0.10.732 — kiosk pollamaz
  // v0.9.528 — karşılama operatörü ADIYLA selamlar ve o anki durumu
  // söyler. İki kaynak da UCUZ: ad zaten AuthProvider'da (login'de
  // gelen /api/auth/me), P1 listesi YALNIZ pencere açıkken ve henüz
  // soru sorulmamışken çekilir — ev kuralı: aç-üzerine-getir, liste
  // prefetch'i yok, poll yok. (v0.10.1138 — `user` yukarıda, profil seçimiyle birlikte.)
  // v0.9.182 — Alternatif A: sayfa-içi tam-boy expand (operatör seçimi).
  // v0.10.1138 — son cevabın sürüm görünümü ("önceki cevap (1/2)"); yeni tur gelince sıfırlanır.
  const [versionSel, setVersionSel] = useState<{ turn: unknown; idx: number } | null>(null);
  const [opening, setOpening] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);

  // Context-awareness (v0.9.164) — bulunulan sayfanın servisi. Mesaj servis
  // adı taşımıyorsa backend guided router bunu varsayılan alır ("neden yavaş?"
  // servis sayfasında → o servis). Banner scope'u şeffaf gösterir.
  const loc = useLocation();
  // v0.10.539 (Faz 3.2) — sayfa bağlamı: URL'den saf serileştirici; rota
  // değişince yeniden hesaplanır (chat AppShell'de tek mount).
  // v0.10.1435 — sayfanın yayınladığı grafik seçimleri (brush + seçili seri;
  // lib/pageContextOverlay.ts) bağlama katılır: "bu tepe" soruları onları görür.
  const pageOverlay = usePageOverlay();
  const page = useMemo(() => withPageOverlay(pageContext(loc.pathname, loc.search), pageOverlay), [loc.pathname, loc.search, pageOverlay]);
  // v0.10.540 (Faz 3.2b) — PIN: operatör sayfa bağlamını sohbete sabitler;
  // sayfa değişse de özne kalır. Eski düz alanlar pinden türetilir (guided/
  // drawer kademeleri sunucu değişikliği olmadan pini izler); sunucu önsözde
  // pini ÖNCE yazar. Oturuma bağlı (sessionStorage), konuşma temizlenince düşer.
  const [pinned, setPinnedState] = useState<PageContext | null>(() => readPin());
  const setPinned = useCallback((ctx: PageContext | null) => { setPinnedState(ctx); writePin(ctx); }, []);
  const pinnedLegacy = useMemo(() => (pinned ? legacyFromPinned(pinned) : null), [pinned]);
  const [sp, setSp] = useSearchParams();
  // v0.9.653 — ekrandaki özneden türeyen başlangıç çipi. Saf çözümleyici
  // (lib/chatContext.ts); rota değişince kendiliğinden güncelleniyor.
  const ctxStarter = useMemo(
    () => contextStarter(loc.pathname, loc.search),
    [loc.pathname, loc.search]);
  // v0.9.1226 — saf çözümleyiciye taşındı (chatContext.serviceFromRoute):
  // yalnız /service|/pod değil, ?service= taşıyan tüm liste rotaları
  // bağlamı devrediyor; kapsam bandı + guided ctxService bedavaya doldu.
  const currentService = useMemo(
    () => serviceFromRoute(loc.pathname, loc.search),
    [loc.pathname, loc.search]);
  // v0.9.537 (operatör raporu: "ekrandaki trace'i anlamıyor") — trace
  // sayfasındayken adresteki ID bağlam olarak gider; "bu trace neden
  // yavaş" sunucuda guidedTraceByID'ye oturur. Mesaja yapıştırılan
  // açık 32-hex her zaman bundan güçlüdür.
  const currentTrace = useMemo(
    () => (loc.pathname === '/trace' ? (sp.get('id') || '') : ''),
    [loc.pathname, sp]);
  // Operation-awareness (v0.9.184) — servis sayfasında seçili ?op=. "bu
  // operasyonun durumu" gibi bir soru guided router'da RED'i o span-name'e
  // daraltır (resolveGuidedOperation'ın context fallback'i).
  const currentOp = useMemo(() => {
    if (loc.pathname === '/service' || loc.pathname === '/service/backtrace') {
      return sp.get('op') || '';
    }
    return '';
  }, [loc.pathname, sp]);

  // Tur state'i + gönderme döngüsü paylaşılan çekirdekte (v0.9.479).
  // Global pencere explain bağlamı GÖNDERMEZ — filo geneli sorular
  // sunucuda aynen guided/RAG/serbest döngü yollarına gider.
  // v0.9.529 — ekrandaki zaman aralığı. Ev kuralı: timeRangeToNs bare
  // JSX'te ASLA (v0.5.184 sonsuz refetch); burada useMemo içinde ve
  // yalnız pencere kimliğinden türüyor, `now()` okunmuyor.
  const [range] = useUrlRange();
  // v0.9.1259 — env devri: ekrandaki global env seçimi sohbete bağlam
  // olarak gider (rangeS aynası); kapsam bandı da gösterir.
  const [env] = useUrlEnv();
  const rangeS = useMemo(() => {
    const { from, to } = timeRangeToNs(range);
    const s = Math.round((to - from) / 1e9);
    return s > 0 ? s : undefined;
  }, [range]);
  // v0.10.33 — MUTLAK pencerenin BİTİŞ anı. rangeS pencereyi SÜREYE
  // çökertiyor ve sunucu onu şimdiye yeniden çapalıyordu: dün gece
  // 03:00-04:00'a zoom yapıp soru sorunca cevap aynı UZUNLUKTA ama
  // BUGÜNKÜ pencereden geliyordu. Sayılar gerçek olduğu için hata
  // sessizdi.
  //
  // ⚠ YALNIZ custom/zoom aralıkta gönderiliyor. Göreli aralıkta ("son 1
  // saat") çıpayı sabitlemek, uzun bir soruşturmada cevabı DONDURUR:
  // operatör yirmi dakika sonra "şimdi nasıl" diye sorduğunda hâlâ
  // yirmi dakika önceki pencereyi görürdü.
  const toMs = useMemo(
    () => (range.preset === 'custom' && range.toMs ? range.toMs : undefined),
    [range],
  );

  // persist: true — KALICILIK YALNIZ BURADA (v0.9.1139, Faz 4.1).
  // AI çekmecesindeki özne sohbeti efemer kalıyor; gerekçe
  // useChatThread'in dosya başında.
  // v0.10.1274 (CoSRE adım 5) — bağlam şeridi: gönderilecek kapsam composer'ın
  // üstünde görünür; ✕ ile çıkarılan öğe BU KONUŞMA için istekten düşer
  // (chatContextScope.ts: düz alan + sayfa ikizi). Env Topbar'ın global
  // seçicisinden (useUrlEnv, aynı URL/sticky kaynağı) — "Tüm ortamlar" = ''.
  const [ctxExcluded, setCtxExcluded] = useState<ReadonlySet<ContextKey>>(() => new Set());
  const screenReqCtx = useMemo<ChatRequestContext>(() => ({
    // v0.10.540 — pin varken eski alanlar pinden (boş alan = kapsamsız, ekrandan DEĞİL).
    service: pinnedLegacy ? (pinnedLegacy.service ?? '') : currentService,
    operation: pinnedLegacy ? (pinnedLegacy.operation ?? '') : currentOp,
    rangeS: pinnedLegacy ? pinnedLegacy.rangeS : rangeS,
    toMs: pinnedLegacy ? pinnedLegacy.toMs : toMs,
    trace: pinnedLegacy ? (pinnedLegacy.trace ?? '') : currentTrace,
    env: pinnedLegacy ? (pinnedLegacy.env ?? '') : env,
    page, // v0.10.539 — sayfa bağlamı protokolü (lib/pageContext, her turda)
    pinnedPage: pinned ?? undefined, // v0.10.540 — sabitlenmiş bağlam
  }), [pinnedLegacy, currentService, currentOp, rangeS, toMs, currentTrace, env, page, pinned]);
  const reqCtx = useMemo(() => applyContextExclusions(screenReqCtx, ctxExcluded), [screenReqCtx, ctxExcluded]);
  const baseCtxItems = useMemo(() => contextBarItems(reqCtx), [reqCtx]);
  // v0.10.1400 (Tek CoSRE) — açıklama turunun TAKİP bağlamı: konuşmadaki son
  // açıklama turu (explainTurn.activeExplainFrom) takip sorularına çekmece
  // özne kipindeki AYNI iki alanı ekler — context.explain (künye + metin +
  // kanıt) ve context.subject (`?ai=` kodeği; sunucu ham kanıtı yeniden
  // kurar) — trace türünde ayrıca trace kimliği + sayfa bağlamı + trace
  // penceresi (AIDrawerChat ile aynı kural). Gönderim ANINDA turlardan çözülür
  // (useChatThread resolveOpts). Bağlam şeridinde "✨ trace …" öğesi; ✕ o
  // açıklama turunu bağlamdan çıkarır (yeni açıklama turu yeniden bağlar).
  const [explainOff, setExplainOff] = useState('');
  const explainTraceCtxRef = useRef<TraceAiContext | null>(null);
  const resolveExplainOpts = (ts: readonly ChatTurn[]) => {
    const a = activeExplainFrom(ts);
    if (!a || a.key === explainOff) return {};
    const fc = explainFollowupContext(a);
    if (a.meta.kind !== 'trace') return fc;
    const live = explainTraceCtxRef.current?.traceId === a.meta.id ? explainTraceCtxRef.current : null;
    const win = live ? traceChatWindow(live, Date.now()) : null;
    const scoped = applyContextExclusions({
      trace: a.meta.id,
      page: live ? traceContextToPage(live) : { page: 'trace', path: '/trace', traceId: a.meta.id },
      ...(live?.env ? { env: live.env } : {}),
      ...(win ? { rangeS: win.rangeS, toMs: win.toMs } : {}),
    }, ctxExcluded);
    const out: Partial<ChatRequestContext> & { explain: string; subject: string } = { ...fc, trace: scoped.trace, page: scoped.page };
    if (live?.env && scoped.env) out.env = scoped.env;
    if (win && scoped.rangeS) { out.rangeS = scoped.rangeS; out.toMs = scoped.toMs; }
    return out;
  };
  const { turns, busy, send, retry, regenerate, editLast, stop, clear, load, flushSave, conversationId, last, showFollowups, codeLocked } =
    useChatThread({
      ...reqCtx,
      resolveOpts: resolveExplainOpts, // v0.10.1400
      profile: profile || undefined,
      deep, // v0.10.1150 — açıkken context.deep: true
      code, // v0.10.1461 — açıkken context.code: true
      // v0.10.1138 — sunucu seçili profili reddetti (rolüne kapalı / silinmiş) → varsayılana dön.
      onProfileRejected: () => setProfile(''),
      persist: true,
      onOpen: href => {
        const to = mergeOpenHref(href, window.location.pathname, window.location.search); // v0.10.434 (D7b); v0.10.460 aynı sayfa
        if (!to) return;
        // v0.10.1125 — /cosre'de başka sayfaya OTOMATİK gezinme yok: sohbet
        // penceresi terk edilmesin; hedef zaten cevabın link çiplerinde (yeni
        // sekme). Aynı sayfa (ör. ?ai= açıklama kipi) yine uygulanır.
        if (isPage && to.split('?')[0] !== window.location.pathname) return;
        // v0.10.483 — hedef ?ai= ise AYNI çekmece açıklama kipine geçer
        // (özne URL'den okunur); kapatmaya gerek yok, ikinci çekmece yok.
        navigate(to, { replace: true });
      },
    });
  // v0.10.1145 — composer taslağı konuşma başına sessionStorage'da (composerDraft):
  // konuşma değişince / yenilemede kaybolmaz, gönderimde silinir. (Konuşma
  // kimliği useChatThread'den geldiği için durum onun ALTINDA kurulur.)
  const draft = useComposerDraft('chat', conversationId);
  const dashCtx = useMemo(() => ({ conversationId }), [conversationId]); // v0.10.1443 — sohbetten dashboard
  // v0.10.1400 — etkin açıklama turu (✕ ile çıkarılmadıysa) + trace bağlamı.
  const activeExplainAll = useMemo(() => activeExplainFrom(turns), [turns]);
  const activeExplain = activeExplainAll && activeExplainAll.key !== explainOff ? activeExplainAll : null;
  const explainTraceId = activeExplain?.meta.kind === 'trace' ? activeExplain.meta.id : null;
  const liveTraceCtx = useTraceAiContext(explainTraceId);
  explainTraceCtxRef.current = liveTraceCtx;
  const ctxItems = useMemo(() => (activeExplain
    ? [{ key: 'explain' as const, label: explainContextLabel(activeExplain.meta),
        title: 'Takip soruları bu açıklamaya bağlanır (aynı kanıt) — ✕ ile bu konuşmadan çıkar' }, ...baseCtxItems]
    : baseCtxItems), [activeExplain, baseCtxItems]);
  // v0.10.1274 — çıkarmalar konuşmaya bağlı: başka konuşmaya geçince sıfır.
  const prevConvRef = useRef(conversationId);
  useEffect(() => {
    if (shouldResetExclusions(prevConvRef.current, conversationId)) { setCtxExcluded(new Set()); setExplainOff(''); }
    prevConvRef.current = conversationId;
  }, [conversationId]);
  const input = draft.value;
  const setInput = draft.setValue;
  // v0.10.687 — D4 ad tamamlama: imleç konumu + aday listesi (sunucu araması).
  const [caret, setCaret] = useState(0);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const completion = useNameCompletion(input, caret);
  // v0.10.1138 — tamamlamadan SEÇİLEN servis anmaları (biçimden bağımsız kapsam olur).
  const [chosen, setChosen] = useState<string[]>([]);
  const parsedInput = useMemo(() => parseChatInput(input, chosen), [input, chosen]);
  // v0.10.1139 — kapsam çipleri gönderilecek hâli gösterir: trace bağlamında
  // @-kapsamı düşer (useChatThread aynı kuralı uygular), açık /komut kalır.
  const chipTrace = pinnedLegacy ? (pinnedLegacy.trace ?? '') : currentTrace;
  const composerChips = useMemo(
    () => scopeChips(scopeForRequest(parsedInput, hasDrawerContext({ trace: chipTrace, page }))),
    [parsedInput, chipTrace, page]);
  // v0.10.540 — yeni konuşma pini de düşürür (pin thread'e bağlı).
  const clearAll = useCallback(() => { clear(); setPinned(null); }, [clear, setPinned]);
  // v0.10.1145 — operatörün "yeni konuşma"sı AÇIK geçiştir: yeni konuşmanın kayıtlı
  // taslağı yüklenir, ekrandaki taslak eski konuşmasında kalır (composerDraft.expect).
  const expectDraft = draft.expect;
  const startNew = useCallback(() => { expectDraft(null); clearAll(); }, [expectDraft, clearAll]);

  // v0.9.1258 — konuşma deep-link'i (?chat=<convId>): URL → state yarısı.
  // Ref sig-guard: aynı değer bir kez yüklenir; load zaten akış sürerken
  // (busyRef) kendini iptal ediyor — yarım cevabın üstüne basılmaz.
  const chatParamRef = useRef('');
  useEffect(() => {
    const want = sp.get('chat') ?? '';
    if (!want || chatParamRef.current === want) return;
    chatParamRef.current = want;
    if (want !== conversationId) {
      setOpen(true);
      expectDraft(want); // v0.10.1145 — o konuşmanın taslağı
      void load(want);
    }
  }, [sp, conversationId, load, expectDraft]);
  // state → URL yarısı: saf çekirdek karar verir (null = dokunma);
  // replace:true + prev kopyası — yabancı paramlar korunur.
  useEffect(() => {
    const next = syncChatParam(sp, conversationId, open);
    if (next) setSp(next, { replace: true });
  }, [sp, setSp, conversationId, open]);

  // ── Geçmiş (v0.9.1139) ──
  // Liste YALNIZ çekmece açılışında çekiliyor (ev kuralı: aç-üzerine
  // getir, prefetch yok, poll yok). Bu oturumda başlatılan bir konuşma
  // listede bir SONRAKİ açılışta görünür — bunun için ekstra istek
  // atmak, maliyet disiplininin karşılığını vermiyor.
  const confirm = useConfirm();
  const [showHistory, setShowHistory] = useState(false);
  const [threads, setThreads] = useState<AiConversationSummary[] | undefined>(undefined);
  const [histErr, setHistErr] = useState('');
  // v0.10.1137 — /cosre kenar çubuğu listeyi hep gösterir: yeni konuşma
  // saklanınca (sunucu kimlik basınca) liste bir kez yenilenir ve eski liste
  // yenilenirken EKRANDA kalır (yükleniyor göstergesi yalnız ilk açılışta).
  // Çekmecede davranış aynen: yalnız açılışta.
  const listKey = isPage ? (conversationId ?? '') : '';
  useEffect(() => {
    if (!open) return;
    let alive = true;
    if (!isPage) setThreads(undefined);
    setHistErr('');
    api.aiConversations()
      .then(t => { if (alive) setThreads(t ?? []); })
      .catch(e => {
        if (alive) { setThreads([]); setHistErr(e instanceof Error ? e.message : String(e)); }
      });
    return () => { alive = false; };
  }, [open, isPage, listKey]);
  // v0.10.1137 — kenar çubuğu: daraltma (localStorage) + telefonda çekmece.
  const [sideCollapsed, setSideCollapsed] = useState<boolean>(() => readSidebarCollapsed());
  const [sideMobile, setSideMobile] = useState(false);
  const toggleSide = () => {
    const narrow = typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      && window.matchMedia('(max-width: 640px)').matches;
    if (narrow) { setSideMobile(v => !v); return; }
    setSideCollapsed(v => { writeSidebarCollapsed(!v); return !v; });
  };

  const openThread = async (t: AiConversationSummary) => {
    try {
      // v0.10.1400 / v0.10.1407 — özneli ESKİ çekmece konuşması (ör. `trace:<id>`,
      // `charts:…`) artık özne kipine dönmez (o kip yok); turları sohbete normal yüklenir.
      setSubject(null);
      expectDraft(t.id); // v0.10.1145 — açık geçiş: o konuşmanın kayıtlı taslağı
      await load(t.id);
      setShowHistory(false);
    } catch (e) {
      setHistErr(e instanceof Error ? e.message : String(e));
    }
  };

  // v0.10.1167 — salt-okunur paylaşım bağlantısı panoya.
  const shareThread = async (t: AiConversationSummary) => {
    try {
      const r = await api.shareAiConversation(t.id);
      const url = `${window.location.origin}${r.path}`;
      try { await navigator.clipboard.writeText(url); toast.success('Salt-okunur bağlantı panoya kopyalandı'); }
      catch { toast.info(`Salt-okunur bağlantı: ${url}`, 10000); }
    } catch (e) {
      toast.error(`Paylaşılamadı: ${e instanceof Error ? e.message : String(e)}`);
    }
  };

  const removeThread = async (t: AiConversationSummary) => {
    if (!await confirm({
      title: 'Konuşma silinsin mi?',
      body: <><b>{t.title}</b> konuşması kalıcı olarak silinecek. Yalnız
        sohbet kaydı gider — telemetri verisine dokunulmaz.</>,
      confirmLabel: 'Konuşmayı sil',
      danger: true,
    })) return;
    try {
      await api.deleteAiConversation(t.id);
      setThreads(prev => (prev ?? []).filter(x => x.id !== t.id));
      // Ekrandaki konuşma silinen thread ise, kabuk artık ölü bir
      // kimliğe yazmaya devam etmemeli: yeni konuşma hâline dönüyoruz.
      if (conversationId === t.id) startNew();
    } catch (e) {
      setHistErr(e instanceof Error ? e.message : String(e));
    }
  };

  // Karşılamanın canlı yarısı (v0.9.528). `enabled` ÜÇ koşulu birden
  // taşır: copilot açık, pencere açık, ve henüz konuşma başlamamış —
  // yani sorgu SADECE karşılama gerçekten ekrandayken koşar. Kapalı
  // pencerede ya da sohbet ortasında tek istek bile gitmez.
  // v0.10.1255 (operatör: "P1 yerine son 1 saatteki exception / hata sayısı")
  // — filo hata (MV) + aktif exception grubu; yalnız karşılama ekrandayken.
  // v0.10.1278 (operatör 2026-10-17: sayı tüm hatalı span'lerdi) — hatalı
  // GİRİŞ isteği + açık/yeni exception grubu; global env (Topbar'la aynı
  // useUrlEnv kaynağı) sorguya VE anahtara girer.
  const greetQ = useQuery({
    queryKey: ['cosre-greeting', env],
    queryFn: () => api.cosreGreeting(env),
    enabled: enabled === true && open && turns.length === 0,
    staleTime: 60_000,
  });
  const greetLine = greetErrors(greetQ.data ?? undefined);
  const greetTitle = greetTooltip(greetQ.data ?? undefined);
  const greetAlert = !!greetQ.data && (greetQ.data.failedRequests1h > 0 || greetQ.data.openExceptionGroups1h > 0);


  // v0.10.650 — yalnız dipteyken yapış (stickToBottom.ts); soru gönderimi pin'ler.
  const { pin: pinBottom, atBottom } = useStickToBottomState(scrollRef, [turns, open]);
  // v0.10.702 — VERİ çipleri: takımın en kötü servisi + o servisin en çok
  // hata alan yolu (sunucu, 60 s). Yalnız çekmece açık + sohbet boşken.
  // v0.10.1382 — gruplu seçenekler (servis / operasyon / P1 / deploy) yetenek
  // satırlarının altında (CosreCapabilities); env yalnız P1'e uygulanır.
  // v0.10.1402 — `team`: sunucunun çözdüğü kullanıcı takımı (Takımım kartının varsayılanı).
  const starters = useCopilotStarters(drawerOpen && enabled === true && turns.length === 0, env);
  // v0.10.702 — şablon çipi: composer'ı doldurur, GÖNDERMEZ; imleç başta,
  // operatör yolu yazar ("/api/x hatalı trace'lerini getir" → endpoint_traces).
  // v0.10.1128 — genelleştirildi: yetenek satırları ve wiki çipi de aynı
  // "doldur + imleci koy + odakla, GÖNDERME" yolunu kullanır.
  const prefill = (text: string, at: number) => {
    setInput(text);
    setCaret(at);
    requestAnimationFrame(() => {
      const el = textareaRef.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(at, at);
    });
  };
  const prefillEndpoint = () => prefill(" hatalı trace'lerini getir", 0);
  // v0.10.1128 — wiki ipucu/çipi yalnız sohbet bu kullanıcıya wiki'den cevap
  // verebiliyorsa (/api/copilot/config `wiki`; yeni istek yok).
  const wikiOn = cfg?.wiki === true;

  // Explain→chat köprüsü (v0.9.165): satır-içi explain panelleri (çekmece
  // OLMAYAN yüzeyler, örn. AnomalyDetailDrawer) bir global event atar; chat
  // açılır + soruyu sorar. AI çekmecesi bu köprüyü KULLANMAZ — sohbeti kendi
  // içinde açar (v0.9.479, operatör raporu: iki üst üste yüzey + bağlamsız
  // cevap). send stabil kimlikli olduğu için listener doğrudan onu çağırır.
  useEffect(() => {
    const h = (e: Event) => {
      const q = (e as CustomEvent<{ question?: string }>).detail?.question;
      if (!q) return;
      setOpen(true);
      void send(q);
    };
    window.addEventListener('coremetry:ai-ask', h);
    return () => window.removeEventListener('coremetry:ai-ask', h);
  }, [send]);
  // v0.10.1400 (Tek CoSRE; operatör: "Explain trace'te çıkan loading ikonu
  // güzel ama chat'ten 'bu trace'i açıkla' deyince aynı yerde devam etmiyor,
  // sanki yeni drawer açılıyor.") — `?ai=trace|exception` TEK SEFERLİK bir
  // niyet: ✨ düğmesi, nudge ve derin linkler (/trace?id=X&ai=trace,
  // /problems?exc=…&ai=exception, /cosre?ai=…) sohbeti açar ve açıklamayı
  // SOHBET TURU olarak gönderir (aynı thread, aynı composer, aynı Durdur);
  // `?ai=` hemen düşer (konuşma kalıcılaşınca adres ?chat= taşır). Ref
  // muhafızı: StrictMode'un çift effect'i ve aynı niyetin yeniden render'ı
  // tek tur gönderir. Copilot yapılandırması gelene dek bekler.
  // v0.10.1405 — aynı yol göç eden TÜM türler için (?ai=span|problem|incident|
  // anomaly|service-health|runbook|log|endpoint); v0.10.1407 — charts /
  // log-spike da: AI_KINDS'in her türü bir sohbet turu (özne kipi silindi).
  const explainAskSentRef = useRef('');
  const rawSubjectKey = rawSubject ? formatAiParam(rawSubject) : '';
  useEffect(() => {
    if (!rawSubject) { explainAskSentRef.current = ''; return; }
    if (enabled !== true || explainAskSentRef.current === rawSubjectKey) return;
    explainAskSentRef.current = rawSubjectKey;
    // v0.10.1406 — ?aicode=1 (paylaşılan "Kodu da incele" linki): kod turu; ?ai= ile
    // BİRLİKTE tek seferlik tüketilir (setSubject(null) ikisini de siler — useAiSubject).
    // v0.10.1435 — metric_panel: panelin sorgusu ✨ anında bırakıldı (kodek konum taşır).
    const panel = rawSubject.kind === 'metric_panel' ? takeMetricPanelAsk(rawSubjectKey) : undefined;
    const ask = explainAskFor(rawSubject, sp.get(AI_SRC_PARAM) || 'ai-param', undefined, sp.get(AI_CODE_PARAM) === '1', panel);
    setOpen(true);
    setSubject(null);
    // v0.10.1405 — niyet (span / service-health / log: + subject kodeği) ya da endpoint'in sabit penceresi.
    if (ask) { pinBottom(); void send(ask.userText, explainTurnSendOpts(ask)); }
  }, [rawSubject, rawSubjectKey, enabled, sp, send, pinBottom, setSubject]);
  // v0.10.1336 — rehberli tur köprüsü (aiEvents AI_PREFILL_EVENT): ai-ask'ın
  // kardeşi ama GÖNDERMEZ — çekmeceyi açar, composer'ı doldurur, imleç sonda,
  // odak (prefill: belgelenmiş "doldur + imleç + odak" yolu). send'e dokunmaz.
  const prefillRef = useRef(prefill);
  prefillRef.current = prefill;
  useEffect(() => {
    const h = (e: Event) => {
      const q = (e as CustomEvent<AIPrefillDetail>).detail?.question;
      if (!q) return;
      setOpen(true);
      prefillRef.current(q, q.length);
    };
    window.addEventListener(AI_PREFILL_EVENT, h);
    return () => window.removeEventListener(AI_PREFILL_EVENT, h);
  }, []);

  if (!enabled) {
    if (!isPage) return null;
    // v0.10.1125 — /cosre: çekmecenin "kapalıyken hiç çizme" kararı sayfada
    // boş ekran olurdu (ev kuralı: boş panel yok). Bilinmiyorken yükleniyor,
    // kapalıyken açıklama + uygulamaya dönüş.
    return (
      <div className="cosre-page cosre-root cosre-root--page">
        <div className="cosre-page__state">
          {enabled === null ? <Spinner label="CoSRE yükleniyor…" /> : (
            <Empty icon="✨" title="CoSRE bu kurulumda kapalı">
              AI yapılandırılmamış ya da devre dışı — yönetici Ayarlar'dan etkinleştirebilir.{' '}
              <Link to="/">Coremetry'yi aç</Link>
            </Empty>
          )}
        </div>
      </div>
    );
  }

  const submit = (text: string) => {
    const picked = chosen;
    setInput(''); setChosen([]); pinBottom();
    void send(text, { chosen: picked });
  };
  // v0.10.1291 — takip çipi: composer seçimleri TAŞINMAZ; çipin kendi kapsamı
  // (followups.scope/command) gider, yoksa düz metin (eski çip davranışı).
  const askChip = (text: string, so?: ChatSendOpts) => {
    setInput(''); setChosen([]); pinBottom();
    void send(text, so);
  };
  // v0.10.1400 — bağlam çipi (trace / exception) açıklamayı sohbet turu olarak ister.
  // v0.10.1405 — niyetin öznesi kodekten (span / service-health / log) ya da {kind, id}'den.
  const askExplain = (a: ChatExplainAsk) => {
    const x = explainAskFor(explainAskSubject(a), a.src, undefined, undefined, a.panel); // v0.10.1435 — panel yükü korunur
    if (x) askChip(x.userText, explainTurnSendOpts(x));
  };
  const acceptCompletion = (item: CompletionItem | undefined) => {
    if (!completion.cq || !item) return;
    const r = applyCompletion(input, completion.cq, item);
    const svc = item.service;
    if (svc) setChosen(c => (c.includes(svc) ? c : [...c, svc]));
    setInput(r.text);
    completion.dismiss();
    requestAnimationFrame(() => {
      const el = textareaRef.current;
      if (!el) return;
      el.focus();
      el.setSelectionRange(r.caret, r.caret);
      setCaret(r.caret);
    });
  };


  // v0.10.1138 — son kullanıcı mesajı (✎) ve düzenleme gönderimi.
  const lastUserIdx = lastUserIndex(turns);
  const editLastAndPin = (text: string) => { pinBottom(); editLast(text); };
  // v0.10.1138 — "Tam sayfada aç ↗": href her zaman gerçek (yeni sekme / kopyala).
  // Düz tıkta: bekleyen kayıt hemen yazılır (kimlik sunucudan), çekmece kapanır,
  // /cosre?chat=<id> aynı sekmede açılır; boş konuşma → yalnız /cosre.
  const fullPageHref = cosreFullPageHref(conversationId);
  const openFullPage = (e: React.MouseEvent<HTMLAnchorElement>) => {
    if (!isPlainLeftClick(e)) return; // Ctrl/Cmd/orta tık: tarayıcı yeni sekmede açar
    e.preventDefault();
    if (busy || opening) return;
    setOpening(true);
    const go = (id: string | null) => {
      setOpening(false);
      closeDrawer();
      navigate(cosreFullPageHref(turns.length > 0 ? id : null));
    };
    if (turns.length === 0) { go(null); return; }
    // v0.10.1139 — kayıt başarısızsa GEZİNME YOK (sessiz kayıp olmasın): çekmece
    // açık kalır, operatör hatayı görür ve yeniden deneyebilir.
    void flushSave().then(go, (err: unknown) => {
      setOpening(false);
      toast.error(`Konuşma kaydedilemedi — tam sayfaya geçilmedi. ${err instanceof Error ? err.message : String(err)}`.trim());
    });
  };

  // v0.10.1125 — başlık + gövde TEK yerde; çekmece ve /cosre sayfası aynı düğümleri sarar.
  const headerNode = (
            <div className="cosre-head" style={{ display: 'flex', alignItems: 'center', gap: 12, flex: 1, minWidth: 0, flexWrap: isPage ? 'wrap' : undefined }}>
              {/* v0.9.1253 (operatör: "CoSRE yazısı biraz daha belirgin,
                  büyük font olabilir") — 13→16 + 700 + hafif harf aralığı;
                  marka adı çekmece başlığında artık ilk bakışta okunur.
                  AiMark de eşlik etsin diye 18→20. */}
              {/* v0.10.483 (operatör: "Geçmiş/Temizle butonları kaymış") — TEK
                  SATIR: marka · meta (kapsam ya da ✨ Explain öznesi, ellipsis)
                  · eylemler (v0.10.1141: model seçici composer'a taşındı). 461'in iki satırlı başlığı Drawer'ın
                  ✕ hizasını ve eylem düğmelerini kaydırıyordu. */}
              {/* v0.10.1395 (operatör: "En üstteki CoSRE yazısı ve telemetry ikonu çok
                  sıkışık duruyor") — marka tek grup, ikonla yazı arasında nefes payı. */}
              <span className="cosre-head__brand">
                <AiMark size={20} />
                <span className="cosre-head__title">CoSRE</span>
              </span>
              <span className="mono cosre-head__meta" style={{
                flex: 1, minWidth: 0,
                color: currentService ? 'var(--accent2)' : 'var(--text3)',
                overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
              }} title={currentService ? `Sorular ${currentService} servisine kapsanır` : undefined}>
                {pinned
                    ? ''
                    // v0.10.1295 (operatör 2026-10-17: "filo geneli yazmasına gerek yok") —
                    // servis yoksa etiket boş; yalnız env (varsa) kalır.
                    : [currentService ? `📍 ${currentService}` : '', env ? `env ${env}` : ''].filter(Boolean).join(' · ')}
              </span>
              {/* v0.10.540 — pin çipi / sabitle düğmesi. */}
              {pinned && (
                <Chip pill size="sm" active onRemove={() => setPinned(null)} removeLabel="Sabitlemeyi kaldır"
                  title={`Sabitlenmiş bağlam — sayfa değişse de sorular buna kapsanır: ${pinLabelTR(pinned)}`}
                  style={{ flexShrink: 1, minWidth: 0, maxWidth: 260 }}>
                  📌 {pinLabelTR(pinned)}
                </Chip>
              )}
              {!pinned && hasPinnableContext(page) && (
                <Button variant="ghost" size="sm" onClick={() => setPinned(page)}
                  title={`Bu sayfanın bağlamını sohbete sabitle: ${pinLabelTR(page)}`} aria-label="Sayfa bağlamını sabitle">📌</Button>
              )}
              {/* v0.9.1139 — konuşma arşivi. Menü değil bir BÖLÜM:
                  çekmece zaten sağ kenarda ve ikinci bir uçan katman
                  (dropdown) sohbetin üstüne binerdi. */}
              {/* v0.10.1138 — çekmeceden /cosre'ye: gerçek <a href> (Ctrl/Cmd/orta tık yeni
                  sekme); düz tıkta önce bekleyen kayıt yazılır, çekmece kapanır, aynı sekmede gezer. */}
              {!isPage && (
                <Link to={fullPageHref} className="cosre-page__home cm-fullpage" data-fullpage=""
                  aria-disabled={busy || opening || undefined}
                  title={busy ? 'Cevap bitince tam sayfada açılabilir' : 'Bu konuşmayı /cosre tam sayfasında aç'}
                  onClick={openFullPage}>Tam sayfada aç ↗</Link>
              )}
              {/* v0.10.1137 — /cosre'de geçmiş sol kenar çubuğunda; düğme yalnız çekmecede. */}
              {!isPage && (
                <Button variant="ghost" size="sm" className="cm-head-btn" onClick={() => setShowHistory(h => !h)}
                  aria-expanded={showHistory}
                  title="Konuşma geçmişi">🕘 Geçmiş</Button>
              )}
              {/* v0.10.1295 (operatör 2026-10-17: "en üstte iki tane tam ekran ve tam
                  sayfada aç çıkıyor") — "⤢ Tam ekran" (çekmeceyi 1100 px'e genişletme)
                  KALDIRILDI; tek yol "Tam sayfada aç ↗" (/cosre, geçmiş + kurallar). */}
              {turns.length > 0 && (
                <Button variant="secondary" size="sm" onClick={startNew}
                  title="Konuşmayı temizle ve yeni konuşma başlat">Temizle</Button>
              )}
              {/* v0.10.1125 — /cosre kromsuz: uygulamaya dönüş linki + tema düğmesi
                  (sidebar'daki ThemeToggle burada yok). Genişlet anlamsız: sayfa zaten tam boy. */}
              {isPage && (
                <Link to="/" className="cosre-page__home" title="Coremetry uygulamasını aç">Coremetry'yi aç</Link>
              )}
              {isPage && <ThemeToggle />}
            </div>
  );

  const bodyNode = (<>

          {/* v0.10.944 (CoSRE Faz A) — "Bağlam" şeridi: başlık satırının hemen
              altında, trace öznesinde. Başlığın TEK SATIR kararı (v0.10.483)
              korunur; şerit kendi satırında sarar. */}
          {/* v0.10.1400 — etkin açıklama turu bir trace ise ve sayfa onun canlı
              bağlamını yayınlıyorsa şerit (takip soruları bu kapsamla gider). */}
          {activeExplain?.meta.kind === 'trace' && liveTraceCtx && (
            <TraceContextStrip traceId={activeExplain.meta.id} ctx={liveTraceCtx} />
          )}

          {/* Geçmiş bölümü (v0.9.1139) — başlık + son etkinlik + mesaj
              sayısı. Satıra tıklamak konuşmayı YÜKLER; satır sonundaki
              quiet-destructive düğme onaydan sonra siler. Yükleniyor /
              hata / boş üç hâlin hepsi çizilir (ev kuralı: boş panel yok). */}
          {showHistory && !isPage && (
            <div style={{
              borderBottom: '1px solid var(--divider)', background: 'var(--bg1)',
              maxHeight: 240, overflowY: 'auto',
            }}>
              <div style={{
                display: 'flex', alignItems: 'center', gap: 8,
                padding: '6px 10px 6px 14px',
              }}>
                <span className="cm-hist__h">
                  Geçmiş
                </span>
                <span style={{ flex: 1 }} />
                <Button variant="secondary" size="xs"
                  onClick={() => { startNew(); setShowHistory(false); }}
                  title="Ekranı boşalt, yeni bir konuşma başlat">+ Yeni konuşma</Button>
              </div>
              {threads === undefined && (
                <div style={{ padding: '4px 14px 10px' }}>
                  <Spinner label="Konuşmalar yükleniyor…" />
                </div>
              )}
              {histErr && (
                <div style={{ padding: '0 14px 8px' }}>
                  <span className="badge b-err" title={histErr}>Geçmiş okunamadı</span>
                </div>
              )}
              {threads?.length === 0 && !histErr && (
                <Empty icon="🕘" compact title="Kayıtlı konuşma yok">
                  Bir soru sorduğunda konuşma otomatik saklanır.
                </Empty>
              )}
              {threads?.map(t => (
                <div key={t.id} style={{
                  display: 'flex', alignItems: 'center', gap: 6,
                  padding: '0 8px 0 14px',
                  background: t.id === conversationId ? 'var(--accent-soft)' : undefined,
                }}>
                  <Button variant="ghost" size="sm"
                    onClick={() => void openThread(t)}
                    title={t.title}
                    style={{
                      flex: 1, minWidth: 0, justifyContent: 'flex-start',
                      textAlign: 'left',
                    }}>
                    <span style={{
                      display: 'block', maxWidth: '100%',
                      overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                    }}>{t.title}</span>
                  </Button>
                  <span className="cm-hist__meta"
                    title={`${t.messages} mesaj`}>
                    {tsRel(t.updatedAt)} · {t.messages}
                  </span>
                  <Button variant="ghost" size="xs"
                    onClick={() => void shareThread(t)}
                    aria-label={`${t.title} konuşmasını paylaş`}
                    title="Salt-okunur bağlantıyı kopyala">🔗</Button>
                  <Button variant="ghost-danger" size="xs"
                    onClick={() => void removeThread(t)}
                    aria-label={`${t.title} konuşmasını sil`}
                    title="Konuşmayı sil">✕</Button>
                </div>
              ))}
            </div>
          )}

          {/* Messages */}
          {/* v0.10.1137 — okuma sütunu (/cosre'de ≤760px), role=log + aria-live:
              akan cevap ekran okuyucuya bitince okunur (aria-busy). */}
          <div className="cm-thread-wrap">
          <div ref={scrollRef} className="cm-thread" role="log" aria-live="polite" aria-busy={busy} aria-label="CoSRE sohbeti"
            style={{ flex: 1, overflowY: 'auto' }}>
            <div className="cm-thread__col">
            {turns.length === 0 && (
              <div className="cm-greet">
                {/* Karşılama (v0.9.528) — LLM çağrısı YOK; ad
                    /api/auth/me'den, durum açık P1'lerden. Durum satırı
                    yüklenirken BOŞ döner: "P1 yok" yanlış iddiası
                    operatörü yanıltırdı (greeting.ts). */}
                <div className="cm-greet__hello">
                  {greetHello(user?.firstName)}
                </div>
                {greetLine && (
                  <div className={`cm-greet__status${greetAlert ? ' is-alert' : ''}`} title={greetTitle}>
                    {greetAlert
                      ? <Link to={greetHref(env)} style={{ color: 'inherit' }}>{greetLine}</Link>
                      : greetLine}
                  </div>
                )}
                {/* v0.9.579 (operatör: "çıkar") — HAZIR SORU ÇİPLERİ
                    KALDIRILDI. Sekiz sabit soru bir MENÜYDÜ, bir araç
                    değil: operatörün kendi sorusu neredeyse hiçbir zaman
                    listedekilerden biri olmuyor ve liste, asistanın
                    yalnız onları anlayabildiği izlenimini veriyordu.
                    Aynı gerekçeyle /explore'un soru kartları da
                    kaldırılmıştı (v0.9.562).
                    Cevap SONRASI follow-up çipleri KALDI: onlar bir menü
                    değil, o cevaba bağlı sıradaki adım. */}
                <div className="cm-greet__ask">Sana nasıl yardımcı olabilirim?</div>
                {/* v0.10.1128 (operatör) — "neler yapabilirim" ipucu: yalnız
                    boş sohbette; tıklama örnek soruyu composer'a DOLDURUR. */}
                {/* v0.10.1382 — satır başlığı şablonu DOLDURUR; altındaki somut
                    seçenek çipi HEMEN GÖNDERİR (askChip). v0.10.1390 — servis ve
                    operasyon satırlarında çip yok, her zaman görünen arama kutusu;
                    seçim de askChip ile gönderir. */}
                {/* v0.10.1402 — en üstte Takımım kartı (takım seçici + üç çip). */}
                <CosreCapabilities wiki={wikiOn} team={starters.team} onPrefill={prefill} onAsk={askChip} />
                {/* v0.9.652 — başlangıç çipleri (operatör isteği). v0.10.1402 —
                    "Takımımın …" çipleri Takımım kartına taşındı; burada bağlam,
                    yol şablonu ve wiki çipi kalır. */}
                <div className="cm-greet__chips">
                  {/* v0.9.653 — EKRANDAKİ özne, statik çiplerden ÖNCE.
                      Operatör bir trace'e bakarken sohbeti açtığında
                      elindeki bağlam kaybolup ona takımının servisleri
                      soruluyordu. Vurgulu çizilir: tek anlamlı öneri o.
                      Otomatik açıklama DEĞİL — sohbeti açmak bir LLM
                      çağrısı tetiklememeli (gerekçe lib/chatContext.ts). */}
                  {ctxStarter && (
                    <Chip pill tone="accent" onClick={() => (ctxStarter.explainAsk ? askExplain(ctxStarter.explainAsk) : submit(ctxStarter.question))}>
                      ✨ {ctxStarter.chip}
                    </Chip>
                  )}
                  {/* v0.10.702 — şablon: composer'a doldurur, yol operatörden. */}
                  <Chip pill onClick={prefillEndpoint} title="Yolu yaz: /api/... hatalı trace'lerini getir">
                    ⌨ …/yol hatalı trace'leri
                  </Chip>
                  {/* v0.10.1128 — wiki şablon çipi: "@wiki " doldurur (v0.10.1378) + odaklar.
                      Dil: karşılamayla aynı (tCosre, sabit TR) — UI dili değil. */}
                  {wikiOn && (
                    <Chip pill data-chip="wiki" onClick={() => { const v = tCosre('cosre.chip.wiki.prompt'); prefill(v, v.length); }}>
                      📚 {tCosre('cosre.chip.wiki')}
                    </Chip>
                  )}
                </div>
              </div>
            )}
            {/* v0.10.1443 — sohbetten dashboard: konuşma kimliği panel açıklamasına + audit'e. */}
            <ChatDashContext.Provider value={dashCtx}>
            {turns.map((t, i) => {
              const isLast = i === turns.length - 1;
              const question = i > 0 && turns[i - 1].role === 'user' ? turns[i - 1].text : undefined; // v0.10.1443
              // v0.10.1138 — son cevap: ↻ yeniden üret + sürüm geçişi; son kullanıcı mesajı: ✎ düzenle.
              if (isLast && t.role === 'assistant' && !t.pending && t.alternatives?.length) {
                const v = answerVersion(t, versionSel && versionSel.turn === t ? versionSel.idx : null);
                return (
                  <ChatBubble key={i} turn={v.view} busy={busy} question={question}
                    shownFollowups={showFollowups ? last?.followups : undefined}
                    onRegenerate={canRegenerate(turns) ? regenerate : undefined}
                    versions={{ index: v.index, total: v.total, onSelect: idx => setVersionSel({ turn: t, idx }) }} />
                );
              }
              return (
                <ChatBubble key={i} turn={t} busy={busy} question={question}
                  shownFollowups={isLast && showFollowups ? last?.followups : undefined}
                  onRetry={isLast && t.error && !busy ? retry : undefined}
                  onRegenerate={isLast && canRegenerate(turns) ? regenerate : undefined}
                  onEdit={i === lastUserIdx && t.role === 'user' ? editLastAndPin : undefined} />
              );
            })}
            </ChatDashContext.Provider>
            </div>
          </div>
          {/* v0.10.1137 — kullanıcı yukarı kaydırdıysa yapışma bırakılır; düğme dibe indirir ve yeniden yapıştırır. */}
          {!atBottom && turns.length > 0 && (
            <Button variant="secondary" size="sm" className="cm-jump" onClick={pinBottom}
              title="En son mesaja in" aria-label="En alta in">↓ En alta</Button>
          )}
          </div>

          {/* Follow-up çipleri (v0.9.163; v0.9.411 konuya-duyarlı) —
              guided cevap kendi rotasından öneri getirirse onlar,
              yoksa statik drill-down listesi. */}
          {/* v0.10.1291 — kural-türevli followups (araç sonuçlarından, kapsamlı) önce;
              tık metni + yapısal kapsamı gönderir (FollowupChips). */}
          {showFollowups && (
            <div className="cm-followups">
              <FollowupChips followups={last.followups} suggestions={last.suggestions}
                fallback={last.explain ? drawerFollowups(explainSubject(last.explain)) : FOLLOWUPS /* v0.10.1400 — açıklama turunun özne çipleri */}
                exchangeId={last.exchangeId} onAsk={askChip} />
            </div>
          )}

          {/* Composer */}
          {/* v0.10.1137 — composer: gölgeli tek kutu (metin + gönder/durdur), okuma sütunuyla hizalı. */}
          <form className="cm-composer" data-tour="cosre-composer"
            onSubmit={e => { e.preventDefault(); submit(input); }}>
            {/* v0.10.664 — <textarea>: Enter gönderir, Shift+Enter yeni satır (stack
                trace / SQL yapıştırılabilir); akarken KİLİTLİ DEĞİL — gönderim
                mevcut akışı durdurup yeni soruyu gönderir (useChatThread). */}
            {/* v0.10.687 — D4: '@ad' ya da tireli token yazınca servis adı adayları
                girişin ÜSTÜNDE; ↑↓ gezinir, Enter/Tab ekler (göndermez), Esc kapatır.
                v0.10.1145 — ChatComposer: markdown araç çubuğu + kısayollar + akıllı
                yapıştırma + önizleme; açık popup'ın tuşları ÖNCE (onBeforeKey). */}
            <ChatComposer
              value={input}
              onChange={setInput}
              onCaret={setCaret}
              onSubmit={() => submit(input)}
              textareaRef={textareaRef}
              autoFocus
              placeholder="CoSRE'ye sor… (Shift+Enter: yeni satır · @ kapsam · / komutlar)"
              ariaLabel="CoSRE'ye mesaj"
              deep={deep} onDeepChange={setDeep} deepDisabled={busy}
              {...(codeRead || codeLocked ? { code: codePill, onCodeChange: setCodePill, codeDisabled: busy, codeReason: codeRead?.available ? undefined : (codeRead?.reason || 'kapalı'), codeLocked } : {})}
              comboboxProps={{
                'aria-autocomplete': 'list',
                'aria-controls': 'chat-complete',
                'aria-expanded': completion.open,
                'aria-activedescendant': completion.open ? `chat-complete-${completion.highlight}` : undefined,
              }}
              popup={completion.open && (
                <NameCompletionPopup id="chat-complete" items={completion.items} highlight={completion.highlight}
                  onPick={acceptCompletion} onHover={completion.setHighlight} />
              )}
              onBeforeKey={e => {
                if (!completion.open) return false;
                if (e.key === 'ArrowDown') { e.preventDefault(); completion.move(1); return true; }
                if (e.key === 'ArrowUp') { e.preventDefault(); completion.move(-1); return true; }
                if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); acceptCompletion(completion.items[completion.highlight]); return true; }
                if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); completion.dismiss(); return true; }
                return false;
              }}
              actions={<>
            {/* v0.10.23 — DURDUR. AbortController zaten kuruluydu ama
                hiçbir affordance'a bağlı değildi; yerel gemma4 tek GPU'da
                koştuğu için istenmeyen bir 5-turlu döngü, operatörün
                sıradaki meşru sorusunun önünü dakikalarca tıkıyordu.
                Akarken Gönder'in YERİNİ alıyor: iki düğme yan yana
                durursa hangisinin etkin olduğu belirsizleşir. */}
            {/* v0.10.1141 — model seçici composer'ın İÇİNDE (Claude gibi), Gönder'in
                solunda; menü yukarı açılır. Akarken devre dışı (model yalnız boştayken
                değişir). Tek profilde aynı yerde tıklanamaz etiket. */}
            <ModelPicker profiles={profiles} defaultProfile={defaultProfile} value={profile}
              onChange={setProfile} activeModel={activeModel} disabled={busy} />
            {busy ? (
              <Button variant="secondary" type="button" onClick={stop}
                title="Cevabı durdur — o ana kadar akan metin korunur">
                Durdur
              </Button>
            ) : (
              // v0.10.1254 (operatör: "Claude chat gibi profesyonel") — yuvarlak ↑ gönder.
              <Button variant="primary" type="submit" disabled={!input.trim()} className="cm-send"
                title="Gönder (Enter)">
                <span aria-hidden="true">↑</span><span className="sr-only">Gönder</span>
              </Button>
            )}
              </>} />
            {/* v0.10.1274 — aktif bağlam şeridi: sunucu bu kapsamı araçlara uygular; ✕ bu konuşmadan çıkarır. */}
            <ChatContextBar items={ctxItems}
              onRemove={k => (k === 'explain'
                ? setExplainOff(activeExplain?.key ?? '') // v0.10.1400 — bu açıklama turu bağlamdan çıkar
                : setCtxExcluded(prev => new Set(prev).add(k)))} />
            {/* v0.10.1138 — yapısal kapsam çipleri: gönderimde context.scope/command olarak gider. */}
            {composerChips.length > 0 && (
              <div className="cm-scope-chips" aria-label="Mesaj kapsamı">
                {composerChips.map(c => (
                  <Chip key={c.key} pill size="sm" active onRemove={() => setInput(v => removeToken(v, c.token))}
                    removeLabel={`${c.label} kapsamını kaldır`} title="Bu mesaj yapısal olarak buna kapsanır">{c.label}</Chip>
                ))}
              </div>
            )}
          </form>
  </>);

  if (isPage) {
    return (
      <ChatLinkNewTabContext.Provider value>
        {/* v0.10.1142 — .cosre-root: CoSRE tip ölçeği (--cosre-fs-*) burada tanımlı (globals.css). */}
        <div className="cosre-page cosre-root cosre-root--page">
          <header className="cosre-page__head">
            {/* v0.10.1137 — ☰: genişte kenar çubuğunu daraltır/açar, telefonda çekmeceyi açar. */}
            <IconButton variant="ghost" size="sm" className="cosre-page__menu" icon="☰"
              aria-label={sideCollapsed ? 'Konuşma listesini göster' : 'Konuşma listesini gizle'}
              aria-controls="cosre-side" aria-expanded={sideMobile || !sideCollapsed}
              onClick={toggleSide} />
            {headerNode}
          </header>
          <div className="cosre-page__main">
            <CosreSidebar threads={threads} error={histErr} activeId={conversationId}
              collapsed={sideCollapsed} mobileOpen={sideMobile}
              onNew={() => { startNew(); setSideMobile(false); }}
              onOpen={t => { setSideMobile(false); void openThread(t); }}
              onDelete={t => void removeThread(t)}
              onShare={t => void shareThread(t)}
              onCloseMobile={() => setSideMobile(false)} />
            <main className="cosre-page__body">
              {bodyNode}
            </main>
          </div>
        </div>
      </ChatLinkNewTabContext.Provider>
    );
  }

  return (
    <>
      {/* Launcher — markalı animasyonlu sparkline (varyant B). Yuvarlak FAB
          kendi anatomisi; shared <Button> atomu uygulanmaz (U1 batch-2 kararı).
          v0.10.924 — buton bütünlüğü Faz 2: kabuk glif-only IconButton (bare;
          aria-label sözleşmesi + odak halkası). 48px yuvarlak gradyan anatomisi
          atomda bir rung değil, satır-içi kalır; `bare` zemin boyamaz. */}
      {launcher && !drawerOpen && <TraceExplainNudge />}{/* v0.10.432 (D8) — FAB'ın üstündeki baloncuk */}
      {launcher && !drawerOpen && (
        // v0.10.926 — title kalır: ipucu kutusu FAB'ın KARDEŞİ olarak
        // `--z-tooltip` (30) ile çizilir, FAB ise `--z-fab` (80). Sağ kenardaki
        // çekmeceler (60/61), span paneli (40) ve üstteki nudge baloncuğu (80)
        // açıkken ipucu onların ALTINDA kalıp görünmez; yerel title üstte.
        <IconButton variant="bare" data-tour="cosre-fab"
          className={criticalOpen > 0 ? 'cm-ai-fab is-alert' : 'cm-ai-fab'}
          onClick={() => setOpen(true)}
          title={criticalOpen > 0 ? `CoSRE — ${criticalOpen} açık kritik problem` : "CoSRE'ye sor"}
          aria-label={criticalOpen > 0 ? `CoSRE, ${criticalOpen} açık kritik problem` : 'CoSRE'}
          style={{
            position: 'fixed', right: 18, bottom: 18, zIndex: 'var(--z-fab)',
            width: 48, height: 48, borderRadius: 24,
            background: 'linear-gradient(135deg, var(--accent-soft), var(--bg1))',
            border: '1px solid var(--accent2)',
            display: 'grid', placeItems: 'center',
            cursor: 'pointer', boxShadow: '0 2px 14px rgba(0,0,0,0.3)',
          }}
          icon={<>
            <AiMark size={26} />
            {criticalOpen > 0 && (
              <span aria-hidden="true" style={{
                position: 'absolute', top: -3, right: -3,
                minWidth: 18, height: 18, padding: '0 5px', boxSizing: 'border-box',
                borderRadius: 9, background: 'var(--err-solid)', color: 'var(--on-accent)',
                fontSize: 10, fontWeight: 700, lineHeight: '14px',
                display: 'grid', placeItems: 'center',
                border: '2px solid var(--bg1)',
              }}>{criticalOpen > 9 ? '9+' : criticalOpen}</span>
            )}
          </>} />
      )}

      {/* v0.9.654 (operatör: "CoSRE drawer gibi çıksa … Chat'ten devam et
          özelliği drawerdı") — sohbet artık PAYLAŞILAN Drawer primitifini
          kullanıyor: Explain çekmecesiyle (AIDrawer) aynı kenar, aynı
          genişlik, aynı Esc/✕ davranışı. Öncesi kendi sağ-alt yüzen
          paneliydi ve iki AI yüzeyi iki ayrı kabuk gibi duruyordu.

          backdrop=false BİLİNÇLİ: operatör sohbet açıkken tabloyu
          kaydırıyor, başka bir trace açıyor, sonra sorusunu yazıyor.
          Overlay bunu imkânsız kılardı — sohbet bir özneyi İNCELEMİYOR,
          ona EŞLİK ediyor. Explain'in modal davranışı DEĞİŞMEDİ.

          Genişlet kipi korundu: geniş sohbet için 620 → içerik alanı. */}
      {drawerOpen && (
        <Drawer
          onClose={closeDrawer}
          backdrop={false}
          closeOnEmptyClick
          width={AI_DRAWER_WIDTH}
          resizeKey="coremetry-cosre-drawer-w" // v0.10.1570 — sola sürüklenerek genişler
          bodyStyle={{ display: 'flex', flexDirection: 'column', padding: 0 }}
          header={headerNode}>
          <div className="cosre-root cosre-root--drawer" data-tour="cosre-drawer">{bodyNode}</div>
        </Drawer>
      )}
    </>
  );
}
