package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/reqid"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// Guided chat mode (v0.8.397 — AI audit A3, Davis-CoPilot-style).
//
// The free agentic tool loop (copilot_chat.go: up to 5 rounds × 11
// JSON tool schemas) is unreliable on the 2B-class model that is the
// PRIMARY production target (qwen3.5-2b on vLLM): schema soup, wrong
// tool picks, empty answers. Guided mode inverts the control flow —
// a deterministic intent router recognises the highest-value question
// shapes (Turkish + English), the SERVER prefetches the relevant data
// with the existing bounded chstore/logstore reads, renders a compact
// Turkish evidence block, and the model makes exactly ONE tool-less
// narration call (the analyze-service pattern, copilot_aianalyze.go).
//
// Mode selection is config-free: the router runs first for EVERY
// provider — for these five shapes a deterministic prefetch beats
// tool-roulette even on frontier models. Unmatched questions fall
// through to the free tool loop UNCHANGED (frontier models keep full
// power; on small models unmatched questions may still flounder —
// accepted trade-off, documented in docs/ai-enhancement-audit.md §3).
//
// SSE contract is unchanged: the prefetches emit the same `step`
// events the tool loop emits (CopilotChat.tsx renders e.tool chips),
// then one `answer`, then `done`. The single Explain call self-records
// one ai_calls row under the "chat-guided" surface so the /ai page can
// track guided-path quality separately from the free loop.
//
// v0.8.398 (AI audit env-awareness slice): the router also extracts a
// deployment environment against the LIVE env list ("uat ortamındaki
// hatalar", "errors in the uat environment", bare "uat") and threads
// it into the bundles — problems via ProblemFilter.Env (service-
// scoped), slow traces via TraceFilter.Env (deploy_env conjunct);
// env-less data paths (service RED context, logs, deploys) state the
// limitation in the evidence instead of silently ignoring the ask.

// ─── Intent router (pure, table-tested) ─────────────────────────────

type guidedIntent string

const (
	guidedNone          guidedIntent = ""
	guidedProblems      guidedIntent = "problems"
	guidedServiceHealth guidedIntent = "service_health"
	guidedSlowTraces    guidedIntent = "slow_traces"
	guidedDeployImpact  guidedIntent = "deploy_impact"
	guidedLogErrors     guidedIntent = "log_errors"
	// guidedLogField (v0.10.433, CoSRE router boşlukları D5) — alan süzgeçli
	// log araması: "url.full alanında \"/x\" geçen loglar" (log_field_search.go).
	guidedLogField guidedIntent = "log_field"
	// guidedOpenPage (v0.10.434, D7b) — "X sayfasını aç": LLM'siz link + open.
	guidedOpenPage guidedIntent = "open_page"
	// v0.10.436 (D2) — "A'dan B'ye giden istekler" / "X servisinde içinde … geçen trace'ler".
	guidedPairRequests guidedIntent = "pair_requests"
	guidedTraceSearch  guidedIntent = "trace_search"
	// v0.10.437 (D6) — iki mutlak pencerenin RED kıyası.
	guidedWindowCompare guidedIntent = "window_compare"
	// v0.10.438 (D3) — çağrı periyodu (A→B ya da tek servis).
	guidedCallPeriod guidedIntent = "call_period"
	// v0.10.439 (D4) — A→B→C fan-out (örnek tabanlı).
	guidedFanout guidedIntent = "fanout"
	// guidedFamilyHealth (v0.9.192) — a family-of-services ask:
	// "mobile bff'lerde hangisinde hata var". No single service
	// resolves, but the message's name-fragments (mobile + bff) match
	// 2+ live services as bounded name segments → one MV read compares
	// the family's RED side by side.
	guidedFamilyHealth guidedIntent = "family_health"
	// v0.9.375 (operatör istegi) — "takımımın servisleri/problemleri":
	// login kullanıcının User.Team'i servis metadata'sının ownerTeam/
	// sreTeam'iyle eşlenir; kimlik CallMeta'dan (ctx) okunur.
	guidedMyServices guidedIntent = "my_services"
	guidedMyProblems guidedIntent = "my_problems"
	// guidedTeamServices (v0.9.1134, operatör istegi) — ADI GEÇEN bir
	// takımın servisleri, EN ÇOK HATA ALAN önce. my_services'in iyelik
	// olmayan ikizi: kimlik yerine mesajdaki takım adı özneyi verir.
	//
	// Asıl işi "hangi takım?" diyaloğunu kapatmak: takımı bilinmeyen
	// kullanıcıya canlı takım listesi ÇİP olarak sunulur, çipe tıklamak
	// ÇIPLAK bir takım adı gönderir ve o mesaj buraya yönlenir. Sunucuda
	// konuşma durumu YOK — akış yalnızca çıplak takım adının kendi
	// başına yönlenebilir olması sayesinde çalışıyor.
	guidedTeamServices guidedIntent = "team_services"
	// v0.9.650 (operatör: "Takımıma ait servislerin hataları
	// (Exceptions) neler?") — Problem ve Exception AYRI yüzeyler:
	// Problem bir alarm kuralının açtığı kayıt, Exception ise
	// span'lerden gruplanan ham hata. my_problems ikincisini
	// kapsamıyordu.
	guidedMyExceptions guidedIntent = "my_exceptions"
	// v0.10.1385 — ADI GEÇEN takımın teknik hataları ("platform takımının
	// teknik hataları", "@team:platform teknik hataları"): my_exceptions'ın
	// iyelik olmayan ikizi, aynı Exceptions kutusu okuması
	// (copilot_team_exceptions.go). Router-içi; sınıflandırıcı listesinde yok.
	guidedTeamExceptions guidedIntent = "team_exceptions"
	// v0.10.1252 — tek servisin teknik hataları (exception grupları).
	guidedServiceExceptions guidedIntent = "service_exceptions"
	// v0.10.1256 — hata sıralaması: en hatalı servisler (filo/takım) ve bir
	// servisin en hatalı operasyonları.
	guidedTopErrorServices guidedIntent = "top_error_services"
	guidedMyTopErrors      guidedIntent = "my_top_errors"
	// v0.10.1402 — ADI GEÇEN takımın en hatalı servisleri ("platform
	// takımının en hatalı servisleri?" — CoSRE başlangıcındaki Takımım
	// kartının çipi): my_top_errors'ın iyelik olmayan ikizi, aynı
	// TopErrorServices okuması. Router-içi; sınıflandırıcı listesinde yok.
	guidedTeamTopErrors guidedIntent = "team_top_errors"
	guidedTopErrorOps   guidedIntent = "top_error_operations"
	// v0.9.376 (operatör istegi, SRE perspektifi) — pod/JVM sağlığı:
	// servisliyse instance listesi + JVM heap; servissizse filo-geneli
	// heap doluluk sıralaması. Veri CH'den (OTel runtime metrikleri) —
	// Thanos şartı yok; restart sayıları KSM gerektirir ve kanıt bunu söyler.
	guidedPodHealth guidedIntent = "pod_health"
	// v0.9.416 (CoSRE fikir #2) — vardiya özeti: "dün gece neler oldu?"
	// Pencere içinde açılan/çözülen problemler + anomali olayları +
	// deploy'lar + yeni P1 exception grupları tek cevapta. Varsayılan
	// pencere 12h (vardiya), açık range her zaman kazanır.
	guidedShiftSummary guidedIntent = "shift_summary"
	// v0.9.420 (CoSRE fikir #5) — bağımlılık sağlığı: "hangi db yavaş?",
	// "kafka lag nasıl?". db_summary_5m / msg MV'lerinden top-N kırılım.
	// guidedRootCause (v0.9.514) — "neden X patladı / sebebi ne".
	// Bir SRE'nin sorduğu EN değerli soru bugüne kadar kendi intent'i
	// olmadan yaşıyordu: ikincil sinyaline savruluyordu ("neden yavaşladı"
	// → slow_traces, yani "yavaş mı" sorusuna çöküyordu) ya da kırılgan
	// serbest tool-loop'a düşüyordu. Kayıtlı kök-neden hipotezi (v0.8.394
	// worker + v0.9.510-512 derin soruşturma) zaten hazır bekliyordu.
	guidedRootCause       guidedIntent = "root_cause"
	guidedDBHealth        guidedIntent = "db_health"
	guidedMessagingHealth guidedIntent = "messaging_health"

	// guidedTraceByID — v0.9.537 (operator-reported: sohbete yapıştırılan
	// trace ID'si "yüklü dokümanlarda bu bilgi yok" cevabına düşüyordu —
	// hiçbir intent 32-hex'i tanımıyor, soru RAG doküman yoluna gidiyordu).
	guidedTraceByID guidedIntent = "trace_by_id"

	// guidedSelfMeta — v0.10.13 (operatör bildirimi: "sen hangi modelsin"
	// sorunca 'Yüklü dokümanlarda bu bilgi yok' diyor).
	//
	// Asistanın KENDİSİ hakkındaki soru ne telemetriye ne dokümana ait.
	// Hiçbir intent tanımadığı için RAG'a düşüyordu ve cevap orada
	// olmadığı için ölü dönüyordu — v0.9.537 (trace ID) ve v0.9.1142
	// (kuyruk birikmesi) ile AYNI sınıf, üçüncü kez.
	//
	// Cevap zaten biliniyor: model adı yapılandırmada (ActiveModel).
	guidedSelfMeta guidedIntent = "self_meta"

	// guidedSpanByID — v0.9.548. Çıplak 16-hex SPAN id'si. Trace'i
	// aranır, bulununca aynı trace kanıt paketi kullanılır.
	guidedSpanByID guidedIntent = "span_by_id"

	// guidedRequestID — v0.9.1142 (operatör istegi). Kurumun kendi
	// YAPILANDIRILMIŞ istek numarası: sabit genişlikli tek bir string ve
	// İÇİNDE işlemin tarihi+saati yazılı (internal/reqid).
	//
	// Neden kendi rotası: operatörün elinde olan kimlik bu — trace/span
	// id değil. Bugüne kadar böyle bir kimlik hiçbir intent'e uymuyordu,
	// yani soru ya RAG doküman yoluna ya kırılgan serbest tool-loop'a
	// düşüyordu (v0.9.537'nin trace-ID'siyle aynı kaza sınıfı).
	//
	// Zincir: kimlik → gömülü zaman → o pencerede log araması →
	// eşleşen kaydın trace_id'si → v0.9.537'nin trace kanıt paketi.
	// İkinci bir montaj YOK.
	guidedRequestID guidedIntent = "request_id"
	// guidedAskService (v0.10.429, D1) — servis adı ÇÖZÜLEMEDİ ya da
	// BELİRSİZ: sessizce none yerine "hangisini kastettin?" — adaylar
	// route.ServiceOptions, sorulan asıl niyet route.AskIntent (çipler o
	// niyetin tam kılavuz cümlesi olur). Router-içi; sınıflandırıcı
	// beyaz listesinde değil (parseIntentJSON kendisi üretir).
	guidedAskService guidedIntent = "ask_service"
	// v0.10.809 — ürün yol tarifi (copilot_howto.go); tek tık, otomatik geçiş yok.
	guidedHowTo guidedIntent = "how_to"
	// v0.10.819 — konu dışı: kibar sınır + ürün yol tarifi (copilot_offtopic.go);
	// sınıflandırıcı üretir, sunucu vetolar. Router regex'i YOK.
	guidedOffTopic guidedIntent = "off_topic"
)

type guidedRoute struct {
	Intent  guidedIntent
	Service string // extracted entity, "" = none/global
	// Env (v0.8.398 — AI audit env-awareness slice) is the deployment
	// environment extracted from the question against the LIVE env
	// list (ListEnvironments), "" = no env narrowing. Threaded into
	// the prefetch bundles: problems → ProblemFilter.Env (service-
	// scoped, env_members.go), slow_traces → TraceFilter.Env (direct
	// deploy_env conjunct). Logs/deploys carry no env path yet
	// (env-separation Phase 4 pending) — those bundles SAY so in the
	// evidence instead of silently ignoring the ask.
	Env string
	// Deep (v0.10.1276, CoSRE adım 7) — servis-sağlık rotasında DERİN tarama
	// istendi (hasSweepSignal: "tara", "kapsamlı", pod+operasyon+fonksiyon…):
	// guidedServiceHealthBundle service_health_sweep'i derin kipte koşar
	// (ilk 5 bulgu + code.* örneklemi). Yeni intent YOK — aynı rota.
	Deep bool
	// Family (v0.9.192) — the resolved service list for a
	// guidedFamilyHealth route ("mobile bff'ler"); nil otherwise.
	Family []string
	// TeamServices (v0.9.651) — takım-kapsamlı bir rotada ÇÖZÜLEN servis
	// listesi; Family emsali. Rotanın GİRDİSİ değil, paketin ÇIKTISI:
	// guidedMyTeamBundle dolduruyor, guidedSuggestions okuyor.
	//
	// Neden gerekli: takım servisleri listelendikten sonraki çipler
	// jenerikti ("Açık problemler?"), yani operatör bir servis SEÇİP
	// onun loglarına/trace'lerine inemiyordu. Servis-kapsamlı çipler
	// (svc + " hata logları?" vb.) ZATEN vardı — eksik olan tek halka,
	// çiplere hangi servislerin yazılacağıydı.
	TeamServices []string
	// Team (v0.9.1134) — guidedTeamServices rotasının ÖZNESİ: mesajda
	// adı geçen canlı takım (extractTeamEntity). TeamServices'in tersi
	// yönü — bu rotanın GİRDİSİ, çıktısı değil.
	Team string
	// TeamOptions (v0.9.1134) — "hangi takım?" diyaloğunda operatöre
	// SUNULACAK canlı takım adları (servis sayısına göre en büyükler).
	// Rotanın çıktısı: guidedMyTeamBundle takımsız kullanıcıda dolduruyor,
	// guidedSuggestions çipe çeviriyor. Çipin metni ÇIPLAK takım adıdır —
	// tıklandığında guidedTeamServices'e yönlenir, yani diyalog sunucuda
	// durum tutmadan kapanır.
	TeamOptions []string
	// TeamFollowups — v0.10.1383: takım özetinin (copilot_team_summary.go)
	// servis başına takip adımları; rotanın ÇIKTISI (bundle yazar,
	// guidedFollowups okur). JSON'a girmez.
	TeamFollowups []chatFollowup `json:"-"`
	// v0.10.1385 — takım exception rotası (copilot_team_exceptions.go):
	// TechErr router GİRDİSİ (soru "teknik hata" dedi → Oracle süzgeçli ikinci
	// link); ExcAxis / ExcOracle bundle ÇIKTISI (Exceptions sayfa linkinin
	// takım ekseni owner|sre|both; Oracle grubu bulundu). TeamFollowups bu
	// rotada grup başına takip çiplerini taşır.
	TechErr   bool   `json:"-"`
	ExcAxis   string `json:"-"`
	ExcOracle bool   `json:"-"`
	// TraceID (v0.9.537) — guidedTraceByID rotasının öznesi (32-hex).
	TraceID string
	// SpanID (v0.9.548) — guidedSpanByID rotasının öznesi (16-hex).
	SpanID string
	// RequestID (v0.9.1142) — guidedRequestID rotasının öznesi: kurumsal
	// yapılandırılmış istek numarası, ORİJİNAL harf kasasıyla (log araması
	// keyword alanlarında harfe duyarlı olabilir).
	RequestID string
	// ReqWindowFromMs / ReqWindowToMs (v0.9.1142) — kimliğin damgasından
	// türeyen arama penceresi, epoch MİLİSANİYE. Rotanın ÇIKTISI
	// (TeamServices emsali): bundle dolduruyor, guidedAnswerLinks /logs
	// derin linkini bundan kuruyor. ms çünkü /logs'un okuduğu tek mutlak
	// pencere token'ı `range=custom:<fromMs>-<toMs>` (logsUrl.ts) —
	// ns yazan çip ÖLÜ paramdı (v0.9.853 K3 dersi).
	ReqWindowFromMs int64
	ReqWindowToMs   int64
	// ServiceOptions / AskIntent (v0.10.429, D1) — guidedAskService rotası:
	// operatöre sunulacak canlı servis adayları ve çiplerin taşıyacağı
	// niyet. TeamOptions'ın servis ikizi; çip metni ÇIPLAK ad DEĞİL, tam
	// kılavuz cümle ("<svc> sağlığı nasıl?") — çıplak ad kapıdan geçmez.
	ServiceOptions []string
	AskIntent      guidedIntent
	// LogField / LogValue / LogContains (v0.10.433, D5) — alan süzgeçli log
	// araması; LogQuery bundle'ın KOŞTUĞU sorgu (backend'e göre şekil), link
	// aynı sorguyu taşır (ReqWindowFromMs deseni: rota alanı bundle çıktısı).
	LogField    string
	LogValue    string
	LogContains bool
	LogQuery    string
	// ExceptionTerm (v0.10.819, copilot_pasted_error.go) — yapıştırılan hata
	// metninden çıkarılan arama terimi; log_field cevabına Inbox exception
	// grubu bağlantısı ekler. Sınıflandırıcı üretmez.
	ExceptionTerm string
	// Page (v0.10.434, D7b) — open_page hedefi: overview|problems|logs|traces|endpoints.
	Page string
	// v0.10.436 (D2) — pair_requests: PairFrom servis, PairTo servis ya da
	// düğüm parçası (PairToKind "service"|"node"; bundle çözülen düğüm adını
	// yazar), PairMissing "from"|"to" (ask çipleri için); trace_search:
	// SearchText + SearchSQL (db.statement LIKE).
	PairFrom    string
	PairTo      string
	PairToKind  string
	PairMissing string
	SearchText  string
	SearchSQL   bool
	// v0.10.437 (D6) — window_compare: iki mutlak pencere; WindowText
	// mesajdaki tarih/saat parçaları (ask çipleri aynı pencereyi taşır).
	Windows    []absWindow
	WindowText string
	// v0.10.439 (D4) — fanout: üçüncü düğüm.
	FanoutTo     string
	FanoutToKind string
	// v0.10.463 (D1, find_entity.go) — liste sorusu / sorulan ifade (aday turu).
	FindList  bool
	FindQuery string
	// v0.10.465 (D2, family_traces.go) — yalnız hatalı trace'ler (yavaş değil).
	TraceErrorsOnly bool
	// TraceRecent — v0.10.1487 (family_traces.go): düz trace listesi isteği
	// (hata/yavaş sözcüğü yok) → süzgeçsiz, en yeni önce. TraceErrorsOnly kazanır.
	TraceRecent bool `json:",omitempty"`
	// v0.10.476 (F3-5, trace_nl_search.go) — değerin bulunduğu attribute anahtarları
	// (bundle yazar; link süzgeç çipine döner).
	SearchKeys []string
	// v0.10.1133 (trace_nl_search.go) — SearchText bir Oracle operasyon adıydı ve
	// bu fonksiyon kodlarına çevrildi (bundle yazar); link ve sohbet bağlamı
	// süzgeci SearchKeys[0] = kod(lar) ile kurar, ad span'lerde yok.
	OracleCodes []string
	// v0.10.688 (endpoint_traces.go) — endpoint_candidates turunun adayları
	// (bundle yazar; çipler ve trace cevabının başlığı okur).
	EndpointOptions []endpointCandidate
	// EnvSet — v0.10.1373: mesaj ≥2 canlı ortam andı (extractEnvSet); Env bu
	// durumda "" (tek ortama sessizce daraltma yok), liste özet çipinde söylenir.
	EnvSet []string `json:",omitempty"`
	// EnvInherited — v0.10.1468: Env sorudan değil EKRANDAN devralındı
	// (ctxEnv). Yolsuz giden çağrı özeti bu durumda MV'de (tüm ortamlar) kalır
	// ve süzgecin uygulanmadığını söyler; soruda anılan ortam ham okumaya gider.
	EnvInherited bool `json:",omitempty"`
	// v0.10.479 (F4-2) — namespace_services: yalnız pod listesi ("bunun pod'ları").
	FindPods bool
	// DirectAnswer — netleştirme sorusu (hangi servis / hangi takım): aday
	// listesi, soru ve çipler anlatımdan ÖNCE tamamen belli; metni sunucu
	// kurar, anlatım çağrısı yapılmaz (modelin ekleyebileceği tek şey
	// uydurma bir ad olurdu).
	DirectAnswer string
	// AnswerTail — v0.10.1397: anlatımın ARKASINA ("Kaynak:" künyesinden önce)
	// sunucunun eklediği deterministik bölüm; demet yazar, cevap kurulumu okur.
	// Model onu düşüremez/değiştiremez (takım servis listesi + son exception
	// grupları + "hangi servis?" sorusu, copilot_team_overview.go). JSON'a girmez.
	AnswerTail string `json:"-"`
	// ExcSampleLinks — v0.10.1486: service_exceptions demetinin örnek trace
	// çipleri (/trace?id=…&span=…; copilot_service_exc_samples.go). JSON'a girmez.
	ExcSampleLinks []guidedAnswerLink `json:"-"`
	// v0.10.1386 — metric_chart (copilot_metric_chart.go). GİRDİ (JSON'a girer:
	// LastRoute "son 24 saate genişlet" takibinde klonlanır): ChartTemplate
	// şablon kimliği, ChartGroupBy kırılım (""|env|pod), ChartEnvAsked ortam
	// SORUDA anıldı (ekrandan devralınmadı → çözülemezse grafik durur).
	// ÇIKTI (demet yazar; link + takip çipleri okur, JSON'a girmez): ChartHref
	// PromQL editörü linki, ChartEnvOptions diğer canlı ortamlar, ChartRangeS pencere.
	ChartTemplate   string   `json:",omitempty"`
	ChartGroupBy    string   `json:",omitempty"`
	ChartEnvAsked   bool     `json:",omitempty"`
	ChartHref       string   `json:"-"`
	ChartEnvOptions []string `json:"-"`
	ChartRangeS     int64    `json:"-"`
	// v0.10.1416 — R2 (copilot_metric_chart_promql.go): ChartPromQL yapıştırılan
	// sorgu (GİRDİ, JSON'a girer — "son 24 saate genişlet" klonu aynı sorguyu
	// doğrulayıp koşar); ChartPromQLExtra mesajdaki diğer aday sorgu sayısı
	// (router yazar, kanıt notu okur; JSON'a girmez).
	ChartPromQL      string `json:",omitempty"`
	ChartPromQLExtra int    `json:"-"`
	// v0.10.1388 — service_topology / trace_topology (copilot_service_topology.go).
	// GİRDİ (JSON'a girer, LastRoute takibinde klonlanır): TopoHops 1|2,
	// TopoErrorsOnly yalnız hatalı kenarlar / hatalı örnek trace, TopoTraceID
	// açık trace kimliği, TopoOperation sorudaki operasyon anması (span adı /
	// "POST /yol" / Oracle kodu). ÇIKTI (demet yazar; çip + link okur):
	// TopoNeighbours ⚠ komşular, TopoHasErrEdge hatalı kenar var, TopoTraceOut
	// çizilen trace.
	TopoHops       int      `json:",omitempty"`
	TopoErrorsOnly bool     `json:",omitempty"`
	TopoTraceID    string   `json:",omitempty"`
	TopoOperation  string   `json:",omitempty"`
	TopoNeighbours []string `json:"-"`
	TopoHasErrEdge bool     `json:"-"`
	TopoTraceOut   string   `json:"-"`
	// v0.10.1431 — resource_investigation (copilot_resource_investigation.go).
	// GİRDİ (JSON'a girer, LastRoute takibinde klonlanır): ResTarget pod/servis/
	// workload adı, ResKind pod|service|workload, ResFamily cpu|memory|restarts|all,
	// ResCluster sorudaki (ya da çözülen) Thanos cluster adı. ÇIKTI (demet yazar;
	// çip + link okur): netleştirme adayları, BULUNAMADI, Pod sayfası kimlikleri,
	// ilk grafiğin editör linki, JVM metriği var (drill çipi).
	ResTarget         string   `json:",omitempty"`
	ResKind           string   `json:",omitempty"`
	ResFamily         string   `json:",omitempty"`
	ResCluster        string   `json:",omitempty"`
	ResPodOptions     []string `json:"-"`
	ResClusterOptions []string `json:"-"`
	ResNotFound       bool     `json:"-"`
	ResClusterID      string   `json:"-"`
	ResNamespace      string   `json:"-"`
	ResHref           string   `json:"-"`
	ResJVM            bool     `json:"-"`
	// v0.10.1432 — R2 drill isteği (GİRDİ, JSON): "jvm" (JVM sözcüğü başka
	// kaynak sözcüğüyle birlikte → aile all + JVM drill'i) | "slow"
	// (yavaş/latency sorusu) | "". Drill yine JVM metriği VARSA koşar.
	ResDrillAsk string `json:",omitempty"`
	// v0.10.1454 — outbound_call (copilot_outbound_call.go). GİRDİ (JSON'a
	// girer: LastRoute "son 6 saate genişlet" takibinde klonlanır, gizli alan
	// düşerdi): anılan yol, tek dış host, ölçülen çağıranlar, çağıransız tarama
	// beyanı, sonda kaynağı (mv|raw). ÇIKTI (demet yazar; çip + link okur):
	// örnek trace'ler, cevabın penceresi.
	OutboundPath    string   `json:",omitempty"`
	OutboundPeer    string   `json:",omitempty"`
	OutboundCallers []string `json:",omitempty"`
	OutboundScan    bool     `json:",omitempty"`
	OutboundSrc     string   `json:",omitempty"`
	OutboundTraces  []string `json:"-"`
	OutboundRangeS  int64    `json:"-"`
	OutboundFromMs  int64    `json:"-"`
	OutboundToMs    int64    `json:"-"`
	// v0.10.1455 — R2 (yolsuz hedef, copilot_outbound_call_route.go): anılan
	// sabit kök jetonu ("mainframe", "esbprod"), host tek adaya inmediyse
	// "hangi dış host?" adayları (≤5) ve terimin hiçbir canlı hostta
	// geçmediği. OutboundProbeS (çıktı değil, tur içi): ön-getirme sondasının
	// MV penceresi — ölçüm varsayılan 1 sa'te boş dönerse o pencereye genişler.
	OutboundTerm    string   `json:",omitempty"`
	OutboundOptions []string `json:",omitempty"`
	OutboundMiss    bool     `json:",omitempty"`
	OutboundProbeS  int64    `json:"-"`
}

// normalizeGuidedMsg lowercases for matching. Go's ToLower maps the
// Turkish dotted capital İ to "i"+U+0307 (combining dot above); we
// strip the combining dot so "İstek" matches keyword "istek".
func normalizeGuidedMsg(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "̇", "")
}

// guidedTokens splits a normalized message into word tokens. The
// charset keeps service-name characters ([a-z0-9._-]) AND Turkish
// letters together so both "mobile-bff-uat" and "loglarında" survive
// as single tokens. Apostrophes are boundaries, which conveniently
// detaches Turkish possessive suffixes ("checkout-service'in" →
// "checkout-service", "in").
func guidedTokens(msg string) []string {
	return strings.FieldsFunc(msg, func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return false
		case r == '.' || r == '_' || r == '-':
			return false
		case r == 'ç' || r == 'ğ' || r == 'ı' || r == 'ö' || r == 'ş' || r == 'ü':
			return false
		}
		return true
	})
}

// tokenHasPrefix reports whether any token starts with any of the
// given stems. Prefix (not equality) matching absorbs Turkish
// agglutination ("hata" matches "hatalar", "hataları", "hatası").
func tokenHasPrefix(tokens []string, stems ...string) bool {
	for _, t := range tokens {
		for _, s := range stems {
			if strings.HasPrefix(t, s) {
				return true
			}
		}
	}
	return false
}

func hasSlowTraceSignal(msg string) bool {
	// v0.9.570 — KELİME SINIRINDA eşleşme. Sınırsız Contains,
	// "bu trace neden yavaş?" sorusunu "en yavaş" kalıbına
	// çarptırıp (ned|EN YAVAŞ) filo geneli liste döndürüyordu.
	// "neden/niye yavaş" AÇIK kalıp olarak listede: bağlamsız sorulduğunda
	// "en yavaş trace'ler" makul bir cevaptır ve bu sözleşme testlerle
	// pinli. Sınırlı eşleşme sayesinde artık kazayla değil BİLEREK
	// eşleşiyor — ve ekranda bir trace varsa aşağıdaki işaret-zamiri
	// kapısı onu geçersiz kılıyor.
	for _, p := range []string{
		"en yavaş", "slowest", "slow trace", "yavaş trace", "en uzun",
		"neden yavaş", "niye yavaş",
	} {
		if containsPhrase(msg, p) {
			return true
		}
	}
	return false
}

func hasDeploySignal(tokens []string) bool {
	return tokenHasPrefix(tokens, "deploy", "rollout", "sürüm", "release")
}

// hasLogSignal: token-bounded so "login" / "catalog" / "topology"
// never trigger the logs intent. Covers the common Turkish case
// suffixes (loglar, loglarında, logunda, logda, logta).
func hasLogSignal(tokens []string) bool {
	for _, t := range tokens {
		if t == "log" || t == "logs" ||
			strings.HasPrefix(t, "loglar") || strings.HasPrefix(t, "logu") ||
			strings.HasPrefix(t, "logd") || strings.HasPrefix(t, "logt") {
			return true
		}
	}
	return false
}

func hasErrorSignal(tokens []string) bool {
	return tokenHasPrefix(tokens, "hata", "error", "exception", "fail", "başarısız", "5xx", "500")
}

// hasExceptionWord — YALNIZ "exception/istisna" kelimesi.
//
// hasErrorSignal "exception"ı da kapsıyor ama çok geniş: "hata" da ona
// giriyor. Takım sorusunu Problem'lerden Exception'lara çevirmek için
// operatörün AÇIKÇA exception demesi gerekiyor — "takımımın hataları"
// bugünkü davranışta (açık problemler) kalsın, "takımımın
// exception'ları" ham hata gruplarına gitsin.
func hasExceptionWord(tokens []string) bool {
	return tokenHasPrefix(tokens, "exception", "istisna")
}

func hasProblemSignal(tokens []string) bool {
	return tokenHasPrefix(tokens, "problem", "sorun", "alarm", "alert", "incident", "arıza", "wrong")
}

func hasHealthSignal(tokens []string) bool {
	return hasHealthDomainSignal(tokens) || tokenHasPrefix(tokens, "durum", "nasıl", "iyi", "analiz", "analy", "incele")
}

// hasHowToShape — v0.10.1225, SAF: nasıl-yapılır sorusu ("nasıl kurabilirim",
// "nasıl yapılır", "how to install"). Telemetri sağlığı değil, bilgi isteği.
func hasHowToShape(tokens []string) bool {
	if tokenHasPrefix(tokens, "kurulum", "kurmak", "install", "setup") {
		return true
	}
	if !tokenHasPrefix(tokens, "nasıl", "how") {
		return false
	}
	for _, t := range tokens {
		for _, suf := range []string{"bilirim", "bilir", "ilir", "ılır", "ulur", "ülür", "malıyım", "meliyim", "abilir", "ebilir"} {
			if len(t) > len(suf)+1 && strings.HasSuffix(t, suf) {
				return true
			}
		}
		if t == "to" {
			return true
		}
	}
	return false
}

// hasHealthDomainSignal (v0.10.819) — hasHealthSignal'ın ALAN yarısı: generic
// "durum/nasıl/iyi" dışarıda ("Amerikan başkanı nasıl seçilir" sağlık sinyali
// değildir). hasHealthSignal bayt-bayt aynı davranır; off_topic vetosu bunu okur.
func hasHealthDomainSignal(tokens []string) bool {
	return tokenHasPrefix(tokens, "sağl", "health", "yavaş", "slow",
		"gecikme", "latency", "performan", "p99", "p95")
}

// hasShiftSignal (v0.9.416) — vardiya-özeti şekilleri: "vardiya",
// "overnight"/"shift", "neler oldu / ne oldu" kalıpları ve "gece".
// "gece" tek başına yeter çünkü switch'te shift, daha SPESİFİK
// sinyallerden (slow-trace/deploy/log/pod) SONRA gelir — "dün gece
// en yavaş trace'ler" slowTraces'ta kalır.
func hasShiftSignal(msg string, toks []string) bool {
	if tokenHasPrefix(toks, "vardiya", "overnight", "shift", "gece") {
		return true
	}
	return strings.Contains(msg, "neler oldu") || strings.Contains(msg, "ne oldu") ||
		strings.Contains(msg, "what happened")
}

// hasDBSignal (v0.9.420) — veritabanı-şekilli sorular. "db"/"sql"
// tam-token ("dbConnection"/"sqlite" gibi ad parçaları tetiklemesin);
// sistem adları prefix ("postgresql", "postgres'te").
func hasDBSignal(toks []string) bool {
	for _, t := range toks {
		if t == "db" || t == "sql" {
			return true
		}
	}
	return tokenHasPrefix(toks, "database", "veritaban", "postgres", "mysql",
		"oracle", "mongo", "redis", "cassandra", "mssql")
}

// hasMessagingSignal (v0.9.420) — mesajlaşma-şekilli sorular. "mq"
// tam-token; "kuyruk"/"queue" BİLEREK YOK — operatör dilinde "kuyruk"
// iş listesi demek (CLAUDE.md), messaging'e devretmek yanlış yönlendirir.
func hasMessagingSignal(toks []string) bool {
	for _, t := range toks {
		if t == "mq" {
			return true
		}
	}
	if tokenHasPrefix(toks, "kafka", "rabbit", "topic", "artemis",
		"activemq", "ibmmq", "nats", "messaging", "jms", "mesajla") {
		return true
	}
	// v0.9.590 — "kuyruk" TEK BAŞINA hâlâ yok (yukarıdaki gerekçe
	// geçerli), ama BİRİKME sözcüğüyle birlikte belirsizlik kalmıyor:
	// operatörün iş listesinde "birikme/lag/gecikme" olmaz, Kafka'da
	// olur. Bu yüzden kapı bir BİRLEŞİM — iki taraf da gerekli.
	//
	// Boşluk gerçekti: "kuyrukta birikme var mı" HİÇBİR intent'e
	// düşmüyordu, yani serbest/RAG yoluna savruluyor ve orada
	// "yüklü dokümanlarda bu bilgi yok" cevabını alıyordu — elimizde
	// tam da o soruyu cevaplayan MV varken.
	return tokenHasPrefix(toks, "kuyru", "queue") &&
		tokenHasPrefix(toks, "birik", "lag", "gecik", "bekleyen", "tüket", "tuket")
}

// hasGuidedSignal is the cheap precheck the handler runs BEFORE
// fetching the live service list — a message with no guided keyword
// at all skips the catalogue read and goes straight to the tool loop.
// guidedTraceIDRe — v0.9.537. 32-hex = W3C trace ID; operatörün en
// doğal hamlesi ID'yi sohbete yapıştırmak. 16-hex BİLİNÇLİ dışarıda:
// o span ID'sidir ve tek başına yapıştırıldığında hangi trace'e ait
// olduğu ancak spans taramasıyla bulunur — ayrı iş.
var guidedTraceIDRe = regexp.MustCompile(`\b[0-9a-f]{32}\b`)

func extractTraceID(msg string) string {
	return guidedTraceIDRe.FindString(msg)
}

// guidedSpanIDRe — v0.9.548. 16-hex = W3C span ID. \b sınırları
// sayesinde 32-hex trace ID'nin İÇİNDEKİ 16'lık dizileri YAKALAMAZ;
// yine de çağrı sırası önemli — trace ID her zaman önce denenir.
var guidedSpanIDRe = regexp.MustCompile(`\b[0-9a-f]{16}\b`)

func extractSpanID(msg string) string {
	return guidedSpanIDRe.FindString(msg)
}

func hasGuidedSignal(msg string) bool {
	toks := guidedTokens(msg)
	return hasSlowTraceSignal(msg) || hasDeploySignal(toks) ||
		hasLogSignal(toks) || hasErrorSignal(toks) ||
		hasProblemSignal(toks) || hasHealthSignal(toks) ||
		hasTeamSelfSignal(toks) || hasPodSignal(toks) ||
		hasShiftSignal(msg, toks) || hasDBSignal(toks) || hasMessagingSignal(toks) ||
		hasWhySignal(toks) || hasPeriodSignal(toks) || // v0.10.438 (D3)
		hasEndpointRequestSignal(toks) || // v0.10.688 — "X isteklerini getir"
		hasServiceReportSignal(toks) || // v0.10.1337 — "X için rapor çıkar"
		hasMetricChartSignal(toks) || // v0.10.1386 — "X heap grafiği", "kafka lag trendi"
		hasResourceSignal(toks) || // v0.10.1431 — "X cpu kullanımı nasıl", "<pod> neden restart ediyor"
		looksLikePromQL(msg) || // v0.10.1416 — yapıştırılan PromQL ("sum(rate(x_total[5m])) çiz")
		hasTopologySignal(toks) || // v0.10.1388 — "X topolojisi", "kimler çağırıyor"
		hasOutboundSignal(msg, toks) || // v0.10.1455 — "<svc> mainframe cevap sürelerinde artış var mı" (RAG'a düşüyordu)
		// v0.9.537 — açık trace ID'si ya da "trace" kökü (prefix:
		// Türkçe ekli hâlleri de yakalar — "tracei", "trace'in";
		// ekrandaki trace'e bağlamdan gitmek için, ctxTrace çözümü
		// dispatch'te).
		extractTraceID(msg) != "" || extractSpanID(msg) != "" ||
		tokenHasPrefix(toks, "trace") || tokenHasPrefix(toks, "span") ||
		// v0.9.1142 — yapıştırılan kurumsal request kimliği hiçbir guided
		// KELİME taşımıyor ("şu isteğe ne oldu: ABCD…"), o yüzden sinyal
		// listesine kendisi giriyor; aksi hâlde sıfır-maliyet kapısı
		// mesajı serbest döngüye bırakırdı.
		hasStructuredRequestID(msg)
}

// hasStructuredRequestID — mesaj yapılandırılmış bir kurumsal istek
// numarası taşıyor mu (internal/reqid). Tespit harfe duyarsız, o yüzden
// normalize edilmiş metinle çalışır; ARAMADA kullanılacak token router'da
// `raw`dan alınır.
func hasStructuredRequestID(msg string) bool {
	if _, ok := reqid.FindToken(msg); ok {
		return true
	}
	// v0.9.1144 — gevşek eş de sinyaldir: parse edilemeyen kimlik-benzeri
	// token'ın RAG'a/serbest döngüye düşmemesi router'daki gevşek dalla
	// AYNI koşula bağlı; sinyal kapısı daha dar kalırsa dal hiç çalışmaz.
	_, ok := reqid.FindLooseToken(msg)
	return ok
}

// hasWhySignal (v0.9.514) — NEDENSELLİK soran şekiller. Türkçe ekler
// için prefix ("neden", "nedeni", "nedenini", "sebep", "sebebi"),
// İngilizce için tam token.
//
// "kaynak" BİLEREK dışarıda: Türkçede "kaynak kullanımı" (resource
// usage) çok daha sık ve o soru doygunluk yolu, nedensellik değil.
func hasWhySignal(tokens []string) bool {
	// "sebeb" AYRI stem: Türkçe ünsüz yumuşaması sebep→sebebi, yani
	// "sebep" prefix'i çekimli hali yakalamaz.
	if tokenHasPrefix(tokens, "neden", "niye", "niçin", "sebep", "sebeb") {
		return true
	}
	for _, t := range tokens {
		switch t {
		case "why", "cause", "reason", "rootcause":
			return true
		}
	}
	return false
}

// hasPodSignal (v0.9.376) — pod/JVM şekilli sorular. "gc" tam-token
// ("gcp" prefix tuzağı); diğerleri prefix (podlar, jvm'de, heap'i).
func hasPodSignal(tokens []string) bool {
	if tokenHasPrefix(tokens, "pod", "jvm", "heap") {
		return true
	}
	for _, t := range tokens {
		if t == "gc" {
			return true
		}
	}
	return false
}

// hasSweepSignal (v0.10.1276, CoSRE adım 7) — servisin TAMAMINI tarama isteği
// (operatör: "Servisin tüm podlarını operationlarını fonksiyonlarını da
// tarasın genel sağlığı için sorunlu olanların loglarına baksın…"). SAF.
//
//   - açık tarama sözcüğü: tara* (taraf* HARİÇ — "tarafında" tarama değil),
//     scan*, sweep*, kapsamlı*, detaylı*;
//   - YA DA ≥2 varlık ailesi: {pod}, {operasyon/operation/endpoint},
//     {fonksiyon/function};
//   - YA DA "genel" + sağlık sinyali ("genel sağlığı", "genel durumu nasıl").
//
// Router bu dalı why/slow/log/pod dallarından ÖNCE dener: operatörün cümlesi
// "podlarını" (pod) ve "loglarına" (log) da taşıyor — sırası tersse soru
// pod_health'e düşer ve operasyon/fonksiyon taraması sessizce kaybolur.
func hasSweepSignal(tokens []string) bool {
	for _, t := range tokens {
		if strings.HasPrefix(t, "tara") && !strings.HasPrefix(t, "taraf") {
			return true
		}
	}
	if tokenHasPrefix(tokens, "scan", "sweep", "kapsamlı", "detaylı") {
		return true
	}
	fam := 0
	if tokenHasPrefix(tokens, "pod") {
		fam++
	}
	if tokenHasPrefix(tokens, "operasyon", "operation", "endpoint") {
		fam++
	}
	if tokenHasPrefix(tokens, "fonksiyon", "function") {
		fam++
	}
	if fam >= 2 {
		return true
	}
	for _, t := range tokens {
		if strings.HasPrefix(t, "genel") && (hasHealthDomainSignal(tokens) || tokenHasPrefix(tokens, "durum")) {
			return true
		}
	}
	return false
}

// hasTeamSelfSignal (v0.9.375) — "kendi takımım" şekilli sorular.
// Türkçe iyelik ekleri prefix'le emilir ("takımımın", "servislerimin");
// İngilizce "my" TEK BAŞINA yeterli değildir (my + team/services/
// problems ister) — "mysql" gibi adlar prefix tuzağına düşmesin diye
// "my" tam-token eşlenir.
func hasTeamSelfSignal(tokens []string) bool {
	if tokenHasPrefix(tokens, "takımım", "ekibim", "servislerim", "problemlerim") {
		return true
	}
	my := false
	benim := false
	for _, t := range tokens {
		switch t {
		case "my":
			my = true
		case "benim", "bizim":
			benim = true
		}
	}
	if my && tokenHasPrefix(tokens, "team", "service", "problem") {
		return true
	}
	// v0.9.650 — Türkçe iyelik SİMETRİSİ. Üstteki önek listesi yalnız
	// "takımım" ekini tanıyordu; "benim takımın", "bizim takımda",
	// "benim ekibin" gibi çok doğal kalıplar DÜŞÜYOR ve soru sessizce
	// FİLO GENELİNE gidiyordu — operatör "takımımın" dediğini sanırken
	// tüm kurumun problemlerini görüyordu.
	//
	// İngilizce dalı ("my" + team/service/problem) bu işi zaten yapıyor;
	// Türkçesinde karşılığı yoktu. Regresyon testiyle bulundu.
	return benim && tokenHasPrefix(tokens, "takım", "ekip", "servis", "problem", "exception")
}

// leadingTeamPossessive (v0.10.1569) — operatör-bildirimi: "takımının
// servisleri nasıl?" wiki'ye gidiyordu. Önek listesi yalnız "takımım"ı
// tanıyor; "takımının / takımın / takımınızın / ekibinin" konuşma dilinde
// "(benim) takımın" anlamında da yazılıyor. Yalnız İLK sözcükse sayılır:
// "foo takımının servisleri" gibi adı (çözülemeyen de olsa) önde olan soru
// adı geçen takımın sorusudur, kendi takıma kaymaz. Çağıran ayrıca adı geçen
// takım ve servis yokluğunu şart koşar.
func leadingTeamPossessive(tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	// Kesin biçimler, önek DEĞİL: "takımına" (yönelme) da "takımın" ile başlar.
	switch tokens[0] {
	case "takımın", "takımının", "takımınız", "takımınızın",
		"ekibin", "ekibinin", "ekibiniz", "ekibinizin":
		return true
	}
	return false
}

// guidedStopwords are message tokens that must never be treated as a
// service-name candidate in the unique-prefix fallback.
var guidedStopwords = map[string]bool{
	"servis": true, "servisi": true, "servisin": true, "service": true, "services": true,
	"trace": true, "traces": true, "log": true, "logs": true, "error": true, "errors": true,
	"deploy": true, "deploys": true, "deployment": true, "release": true, "rollout": true,
	"slow": true, "slowest": true, "health": true, "healthy": true, "latency": true,
	"son": true, "last": true, "the": true, "for": true, "and": true, "show": true,
	"what": true, "olan": true, "neden": true, "most": true, "hour": true, "hours": true,
	"minute": true, "minutes": true, "day": true, "days": true, "with": true, "problem": true,
	"problems": true, "alert": true, "alerts": true, "incident": true, "how": true,
}

// asciiNameToken reports whether the token could be a service name
// (chstore's serviceTokenRe convention: ascii-only, so Turkish words
// with diacritics never collide with a service).
func asciiNameToken(t string) bool {
	for _, r := range t {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

func isNameChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '.' || b == '_' || b == '-'
}

// indexBounded finds sub inside msg with word-ish boundaries: the
// characters on either side must not be service-name characters, so
// "mobile-bff" never matches inside "mobile-bff-uat".
func indexBounded(msg, sub string) int {
	for start := 0; ; {
		i := strings.Index(msg[start:], sub)
		if i < 0 {
			return -1
		}
		i += start
		leftOK := i == 0 || !isNameChar(msg[i-1])
		r := i + len(sub)
		rightOK := r >= len(msg) || !isNameChar(msg[r])
		if leftOK && rightOK {
			return i
		}
		start = i + 1
	}
}

// extractServiceFullName (v0.10.819, extractServiceEntity'den ayrıldı; davranış
// aynı) — YALNIZ sınırlı tam-ad geçişi, önek tahmini yok. Yapıştırılan hata
// metni için: "not"/"file"/"load" gibi İngilizce sözcükler bir servis önekine
// çözülmemeli (copilot_pasted_error.go).
func extractServiceFullName(msg string, services []string) string {
	best := ""
	for _, svc := range services {
		ls := strings.ToLower(svc)
		if len(ls) < 3 || len(ls) <= len(best) {
			continue
		}
		if indexBounded(msg, ls) >= 0 {
			best = svc
		}
	}
	return best
}

// extractServiceEntity matches the message against the LIVE service
// list (never a guess): first the longest bounded full-name substring
// (so the suffixed "mobile-bff-uat" beats its "mobile-bff" prefix
// sibling), then a unique-prefix token fallback so "checkout servisi"
// resolves to "checkout-service" when exactly one service starts with
// that token. Ambiguous prefixes (2+ matches) return "" — deterministic
// beats clever.
func extractServiceEntity(msg string, services, envs []string) string {
	if best := extractServiceFullName(msg, services); best != "" {
		return best
	}
	// v0.8.398 — a token that names a LIVE deployment environment is
	// never a service-PREFIX candidate: "uat ortamındaki hatalar" must
	// not resolve to a "uat-gateway" service. The bounded full-name
	// pass above still wins when the operator literally types the
	// service name ("uat-gateway hataları").
	// v0.10.1380 — öneksiz kısa ortam biçimi de ortam jetonudur ("eu-a",
	// canlı "prod-eu-a"); envNameTokens tam adları da içerir.
	envTok := envNameTokens(envs)
	for _, t := range guidedTokens(msg) {
		if len(t) < 3 || guidedStopwords[t] || envTok[t] || !asciiNameToken(t) {
			continue
		}
		match, n := "", 0
		for _, svc := range services {
			ls := strings.ToLower(svc)
			if !strings.HasPrefix(ls, t) {
				continue
			}
			if len(ls) > len(t) && ls[len(t)] != '-' && ls[len(t)] != '_' && ls[len(t)] != '.' {
				continue // "check" must not claim "checkout-service"
			}
			n++
			match = svc
			if n > 1 {
				break
			}
		}
		// v0.10.1251 (operatör: "hep bff-asset-management çıkıyor ama
		// mobile-overview-bff, mobile-crm-bff gibi çok daha fazla bff var") —
		// tek ÖNEK eşleşmesi, kelime başka servislerde de TAM AD PARÇASI
		// olarak geçiyorsa servis değil AİLEDİR: çözülmez, aile / aday yolu
		// devralır ("bff'ler nasıl" → bff ailesi, "bff sorunu" → adaylar).
		if n == 1 && serviceSegmentHits(t, services) < 2 {
			return match
		}
	}
	return ""
}

// serviceSegmentHits — SAF: tokenın tam ad parçası (-/_/. ile ayrılmış)
// olarak geçtiği servis sayısı (2'de durur).
func serviceSegmentHits(tok string, services []string) int {
	n := 0
	for _, svc := range services {
		for _, seg := range strings.FieldsFunc(strings.ToLower(svc), func(r rune) bool { return r == '-' || r == '_' || r == '.' }) {
			if seg == tok {
				n++
				break
			}
		}
		if n >= 2 {
			return n
		}
	}
	return n
}

// extractEnvEntity matches the message against the LIVE environment
// list (ListEnvironments — never a guess), the env twin of
// extractServiceEntity's bounded full-name pass (v0.8.398). Bounded
// matching gives both asks for free: the bare name ("uat hataları")
// and the phrased forms ("uat ortamında/ortamı", "uat environment",
// "env uat") all contain the standalone env token, while an
// env-suffixed SERVICE name ("mobile-bff-uat") never leaks an env —
// '-' is a name char, so the inner "uat" fails the boundary check.
// Longest match wins ("preprod" beats "prod"; "prod" can't match
// inside "preprod" anyway). Unknown env words return "" — the bundle
// then runs env-less, deterministic beats clever. No prefix fallback:
// env names are short, exact vocabulary.
func extractEnvEntity(msg string, envs []string) string {
	best := ""
	for _, env := range envs {
		le := strings.ToLower(env)
		if len(le) < 2 || len(le) <= len(best) {
			continue
		}
		if indexBounded(msg, le) >= 0 {
			best = env
		}
	}
	if best != "" {
		return best
	}
	// v0.10.1380 — tam ad yoksa öneksiz/harf duyarsız kısa biçim ("kxzone" →
	// "prod-Kxzone"): YALNIZ tek canlı değere çözülüyorsa; belirsizse ("eu-a" →
	// prod-eu-a + dr-eu-a) "" — tahmin yok, küme extractEnvSet'te görünür.
	return envShortResolve(findEnvMentions(msg, envs))
}

// envSetMax — v0.10.1373: extractEnvSet tavanı.
const envSetMax = 6

// extractEnvSet — SAF (v0.10.1373, operator-reported: "hem eu-a, eu-b hem tr-c
// kümelerinde çağrılıyor" sorusunda extractEnvEntity eşit uzunluktaki adlardan
// liste sırasındakini — en yoğun ortamı — seçip diğerlerini SESSİZCE atıyordu).
// Mesajda SINIRLI (indexBounded) geçen canlı ortam adları, mesajdaki sırayla,
// tekil, ≤envSetMax. "eu-a-svc" içindeki "eu-a" sızmaz ('-' ad karakteri);
// parantez/virgül sınırdır. 2'den az ad → nil (tek ortam extractEnvEntity'nin
// işi). msg normalizeGuidedMsg'den geçmiş olmalı.
func extractEnvSet(msg string, envs []string) []string {
	// v0.10.1380 — anmalar findEnvMentions'tan: tam ad + öneksiz kısa biçim
	// (harf duyarsız, sınırlı); kısa biçim isabeti o biçimi paylaşan TÜM canlı
	// değerleri getirir ("eu-a" → prod-eu-a, dr-eu-a). Tam adlarla bayt bayt
	// eski: mesaj sırası, harf-katlamalı tekil (ilk yazım), <2 → nil, ≤envSetMax.
	out := envMentionValues(findEnvMentions(msg, envs))
	if len(out) < 2 {
		return nil
	}
	if len(out) > envSetMax {
		out = out[:envSetMax]
	}
	return out
}

// ─── Takım varlığı (v0.9.1134) ──────────────────────────────────────

// extractTeamEntity — mesajdaki takım adını CANLI takım kataloğuna karşı
// çözer (asla tahmin değil; teamCatalogue = service_metadata'nın boş
// olmayan ownerTeam+sreTeam değerleri). extractEnvEntity'nin ikizi:
// sınırlı (bounded) tam-ad eşleşmesi, en UZUN kazanır ("SY-Dijital
// Altyapı", "dijitalsy" alt-adını gölgeler).
//
// KATLAMA: iki taraf da chstore.NormTeamName'den geçer — takım adları
// Türkçe yazılıyor ve iki I tuzağı burada da geçerli ("Bankacılık" vs
// "BANKACILIK"). Katlama i/ı'yı tek forma indirdiği için hem katalog adı
// hem mesaj ASCII'ye iner; sınır denetimi (indexBounded) o yüzden çalışır.
//
// ÇIPLAK takım adı da eşleşir ("orionsy") — "hangi takım?" sorusuna
// verilen cevap turu tam olarak bu şekildedir ve akışın tek dayanağıdır.
//
// UZUNLUK TABANI 2 (v0.9.1246, operatör: gerçek takım adları "SY"/"UG"
// gibi 2 harfli KISA kodlar). v0.9.1134'te taban 3'tü ve gerekçesi "iki
// harflik bir ad rastgele metnin içinde eşleşir" idi — ölçüldüğünde bu
// gerekçe indexBounded'ı yok sayıyordu: eşleşme zaten SINIRLI, yani
// "sy" ancak kendi başına bir token olarak yakalanır ("sy takımının
// exception'ları" ✓, "kaysysteam" ✗). Taban 3 kalsaydı operatörün
// GERÇEK takımı hiçbir soruda çözülemezdi — sessiz ve kalıcı bir
// çıkmaz. Stopword kapısı yerinde duruyor ve adlar CANLI katalogdan
// geliyor, yani yüzey katalogla sınırlı.
//
// Tek karakter hâlâ dışarıda: tek harflik bir token noktalama/kısaltma
// gürültüsüyle karışır ve katalogda böyle bir ad görülmedi.
func extractTeamEntity(msg string, teams []string) string {
	folded := chstore.NormTeamName(msg)
	best := ""
	bestLen := 0
	for _, t := range teams {
		ft := chstore.NormTeamName(t)
		if utf8.RuneCountInString(ft) < 2 || guidedStopwords[ft] || len(ft) <= bestLen {
			continue
		}
		if indexBounded(folded, ft) >= 0 {
			best, bestLen = t, len(ft)
		}
	}
	if best != "" {
		return best
	}
	// v0.10.429 (D1) — takım KODU tireli bir ifadenin parçası olabilir
	// ("SY-XYZ takımı" ↔ katalog "SY", ya da katalog "SY-XYZ" ↔ mesaj
	// "sy xyz"): '-' ad karakteri olduğundan sınırlı arama kaçırıyordu.
	// Jeton eşi (tire/boşluk/noktalama ayırıcı), yalnız TEK takım eşleşirse.
	toks := nameTokens(folded)
	match, n := "", 0
	for _, t := range teams {
		ft := chstore.NormTeamName(t)
		if utf8.RuneCountInString(ft) < 2 || guidedStopwords[ft] {
			continue
		}
		fts := nameTokens(ft)
		// Yalnız ÇOK-JETONLU takım adı ("sy-xyz" ↔ "sy xyz"): tek jetonlu ad
		// tireli daha uzun bir adın içinde eşleşemez ("orionsy" ↛
		// "orionsy-legacy" — sınır kuralı, copilot_team_services_test).
		if len(fts) < 2 {
			continue
		}
		// takımın tüm jetonları mesajda ardışık geçmeli
		for i := 0; i+len(fts) <= len(toks); i++ {
			ok := true
			for j := range fts {
				if toks[i+j] != fts[j] {
					ok = false
					break
				}
			}
			if ok {
				n++
				match = t
				break
			}
		}
		if n > 1 {
			return ""
		}
	}
	if n == 1 {
		return match
	}
	return ""
}

// isBareTeamAsk — mesaj SADECE takım adından mı oluşuyor ("orionsy",
// "Orionsy?"). "hangi takım?" çipine tıklayan operatörün ürettiği tur
// budur; router'da takım dalının tek başına açılmasını haklı kılan sinyal.
// Ad çıkarıldıktan sonra kalanın harf/rakam taşımaması yeterli — noktalama
// ve boşluk serbest.
func isBareTeamAsk(msg, team string) bool {
	if team == "" {
		return false
	}
	folded := chstore.NormTeamName(msg)
	ft := chstore.NormTeamName(team)
	i := indexBounded(folded, ft)
	if i < 0 {
		return false
	}
	rest := folded[:i] + folded[i+len(ft):]
	for _, r := range rest {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// hasTeamWord — "takım/ekip/team" kökü. İyelik hâlleri de bu öneke girer
// ("takımımın") ama router'da hasTeamSelfSignal HER ZAMAN önce gelir, yani
// iyelik soruları buraya hiç düşmez.
func hasTeamWord(tokens []string) bool {
	return tokenHasPrefix(tokens, "takım", "takim", "ekip", "ekib", "team")
}

// hasServiceListWord — "servis/service" kökü; adı geçen takımın SERVİS
// listesini isteyen şekiller ("orionsy servisleri").
func hasServiceListWord(tokens []string) bool {
	return tokenHasPrefix(tokens, "servis", "service")
}

// mayNameTeam — copilotChatGuided'ın katalog okumasını ÇIPLAK takım adı
// için de göze alıp almayacağı ucuz kapı (saf).
//
// Neden gerekli: hasGuidedSignal'da takım adı diye bir sinyal YOKTUR ve
// olamaz (adlar canlı katalogdan gelir). "hangi takım?" çipine basan
// operatörün mesajı ("orionsy") hiçbir guided kelimesi taşımaz, yani
// hızlı-çıkış onu serbest tool döngüsüne atardı — çipin vaadi tam orada
// kırılırdı.
//
// Kapı kısa VE ad-şekilli mesajlarla sınırlı; ödediği bedel üç adet
// 60sn-cache'li katalog okumasıdır. Uzun cümleler zaten guided sinyaliyle
// gelir, taşımıyorsa serbest döngü doğru yerdir.
func mayNameTeam(norm string) bool {
	toks := guidedTokens(norm)
	// v0.10.429 (D1) — takım/servis kökü taşıyan biraz daha uzun cümleler
	// de kapıdan geçer ("SY-XYZ takımına ait servisleri listeler misin" 6
	// jeton / 45 rune); sinyalsiz kısa mesaj sınırı 40 rune + 5 jeton kalır.
	maxRunes, maxToks := 40, 5
	if hasTeamWord(toks) || hasServiceListWord(toks) {
		maxRunes, maxToks = 80, 8
	}
	if utf8.RuneCountInString(strings.TrimSpace(norm)) > maxRunes {
		return false
	}
	if len(toks) == 0 || len(toks) > maxToks {
		return false
	}
	for _, t := range toks {
		// v0.9.1246 — taban 3 → 2. "SY" / "UG" gibi 2 harfli takım
		// KODLARI gerçek (operatör), ve "hangi takım?" çipine basınca
		// mesaj tam olarak o çıplak koddan ibaret oluyor: taban 3'te
		// kapı kapalı kalır, çip serbest tool döngüsüne düşer ve
		// diyalog vaadini kırardı. Bedeli, sinyalsiz kısa mesajlarda
		// üç adet 60sn-cache'li katalog okuması.
		if utf8.RuneCountInString(t) >= 2 {
			return true
		}
	}
	return false
}

// teamCatalogue (v0.9.1134) — CANLI takım kataloğu: service_metadata'nın
// boş olmayan ownerTeam + sreTeam değerleri, SERVİS SAYISINA göre azalan
// (eşitlikte ada göre artan — deterministik sıra, map iterasyonu sızmaz).
//
// Sıra iki işi birden yapıyor: "hangi takım?" çipleri en büyük takımlarla
// başlar (operatörün takımı büyük olasılıkla ilk 8'de), extractTeamEntity
// ise sıradan bağımsız (en uzun ad kazanır).
//
// Tekilleştirme CanonTeam üzerinden — alias tablosu ve Türkçe katlama
// dahil, yani "orionSY"/"Orionsy" TEK takımdır (v0.8.330'un
// /services tarafında yaptığı işin sunucu ikizi). Gösterilen yazımı
// betterTeamDisplay seçer (deterministik).
//
// v0.9.1244 — KATALOG mcptools.TeamCatalogue'a taşındı (list_teams tool'u
// aynı sayımı ve aynı sırayı vermek zorunda; mcptools api'yi import
// edemez, döngü). Buradaki tek çağıran guidedTeamNames artık
// mcptools.ReadTeamCatalogue'u okuyor — api'de ikinci bir uygulama YOK.
// Sıra/tekilleştirme/determinizm testleri de implementasyonun yanına
// taşındı: mcptools/team_ownership_test.go.

// extractServiceEntities (v0.9.422, CoSRE fikir #7) — mesajda sınırlı
// (bounded) tam-ad olarak geçen TÜM canlı servisler. extractServiceEntity
// en uzun TEK eşleşmeyi döner; kıyas soruları ("checkout-service ile
// payment-service p99 kıyasla") iki adı da ister. Ad-içi gölgeleme yok:
// "mobile-bff", "mobile-bff-uat" içinde boundary'ye takılır.
func extractServiceEntities(msg string, services []string) []string {
	var out []string
	for _, svc := range services {
		ls := strings.ToLower(svc)
		if len(ls) < 3 {
			continue
		}
		if indexBounded(msg, ls) >= 0 {
			out = append(out, svc)
		}
	}
	return out
}

// hasCompareSignal (v0.9.422) — açık karşılaştırma kalıpları.
func hasCompareSignal(toks []string) bool {
	for _, t := range toks {
		if t == "vs" || t == "versus" {
			return true
		}
	}
	return tokenHasPrefix(toks, "kıyas", "kiyas", "karşılaştır", "karsilastir", "compare", "fark")
}

// routeGuidedIntent is THE router: normalized keyword matching over
// the five shapes, most-specific first. Pure — table-tested in
// copilot_guided_test.go with Turkish + English variants.
// teams (v0.9.1134) — canlı takım kataloğu (teamCatalogue); nil = takım
// farkındalığı kapalı, o hâlde davranış bayt-bayt eskisidir.
func routeGuidedIntent(raw string, services, envs, teams []string, ctxService string) guidedRoute {
	return routeGuidedIntentOpts(raw, services, envs, teams, ctxService, false)
}

// routeGuidedIntentOpts — v0.10.1133: routeGuidedIntent + oracleOps bayrağı
// (etkin Oracle kaynağı var mı; çağıran söyler, router SAF kalır). false iken
// rota bayt-bayt eskisi: Oracle operasyon adı kuralı hiç denenmez.
func routeGuidedIntentOpts(raw string, services, envs, teams []string, ctxService string, oracleOps bool) guidedRoute {
	return routeGuidedIntentExt(raw, services, envs, teams, ctxService, oracleOps, nil)
}

// routeGuidedIntentExt — v0.10.1455: routeGuidedIntentOpts + canlı dış host
// kataloğu (outbound_call R2; çağıran okur, router SAF kalır). nil = yalnız
// sabit hedef kökleri (esb/mainframe/…); diğer rotalar bayt-bayt aynı.
func routeGuidedIntentExt(raw string, services, envs, teams []string, ctxService string, oracleOps bool, extHosts []outboundHostEntry) (route guidedRoute) {
	msg := normalizeGuidedMsg(raw)
	toks := guidedTokens(msg)
	// v0.9.537 — açık 32-hex trace ID'si EN GÜÇLÜ sinyaldir ve her şeyi
	// ezer: operatör somut bir öznenin adresini vermiş. Eskiden hiçbir
	// intent tanımıyordu, soru RAG doküman yoluna düşüp "yüklü
	// dokümanlarda bu bilgi yok" cevabı alıyordu (operator-reported).
	if id := extractTraceID(msg); id != "" {
		// v0.10.1388 — "<traceId> topolojisi": o trace'in servis grafiği.
		if hasTopologySignal(toks) {
			return guidedRoute{Intent: guidedTraceTopology, TopoTraceID: id, TopoErrorsOnly: hasErrorSignal(toks)}
		}
		return guidedRoute{Intent: guidedTraceByID, TraceID: id}
	}
	// v0.10.13 — asistanın KENDİSİ hakkındaki soru. Somut bir ID'den
	// SONRA bakılıyor: operatör bir trace yapıştırdıysa niyeti odur,
	// cümlede "sen" geçse bile. Ama telemetri sınıflandırıcılarından
	// ÖNCE: "hangi modelsin" hiçbir servisi adlandırmıyor ve aşağıdaki
	// hiçbir kapıya uymadığı için RAG'a savruluyordu.
	if isSelfMetaQuestion(toks) {
		return guidedRoute{Intent: guidedSelfMeta}
	}
	// v0.9.1142 — YAPILANDIRILMIŞ kurumsal request kimliği. SIRA: açık bir
	// trace ID'si (yukarıda) hâlâ en doğrudan çapa — o varsa log araması
	// yapmadan trace'e gidilir. Bu kontrol 16-hex span'den ÖNCE, çünkü
	// kimlik daha spesifik bir sinyal: uzunluk + sabit ofsetlerde geçerli
	// bir takvim tarihi taşıyor, yani şeklin kendisi doğrulanıyor.
	//
	// `raw` üzerinden (normalize edilmiş msg DEĞİL): harf kasası aramada
	// kullanılacak, ES keyword alanlarında eşleşme harfe duyarlı olabilir.
	// Hex çakışması yok — kimlik en az 47 karakter, \b sınırlı 32/16-hex
	// örüntüleri o uzunlukta bir alnum bloğunun içinde eşleşemez.
	if tok, ok := reqid.FindToken(raw); ok {
		return guidedRoute{Intent: guidedRequestID, RequestID: tok}
	}
	// v0.10.1416 — YAPIŞTIRILAN PromQL (copilot_metric_chart_promql.go): yapıştırılan
	// hata metninden ve 16-hex span kontrolünden ÖNCE (sorgudaki
	// exception="…Exception" etiket değeri hata metni sanılmasın); sorgu
	// metric_chart'a ChartPromQL olarak iner. Ortam yalnız sorgu DIŞI metinden.
	if r, ok := routePastedPromQL(raw, envs); ok {
		return r
	}
	// v0.10.819 — YAPIŞTIRILAN hata/stack trace metni (copilot_pasted_error.go):
	// 16-hex span kontrolünden ÖNCE — .NET assembly kimliğindeki PublicKeyToken
	// 16-hex'tir ve span sanılıp "Span … BULUNAMADI" deniyordu; 32-hex trace ve
	// yapılandırılmış istek kimliği (yukarıda) yine kazanır.
	if r, ok := routePastedError(raw, msg, toks, services); ok {
		return r
	}
	// v0.9.548 — 16-hex SPAN id'si. SIRA önemli: 32-hex önce denendi,
	// yani bir trace ID'nin içindeki 16'lık dizi buraya düşemez.
	if id := extractSpanID(msg); id != "" {
		return guidedRoute{Intent: guidedSpanByID, SpanID: id}
	}
	// v0.9.1144 — kimliğe BENZEYEN ama şablona uymayan token. Operatör
	// prod raporu: AltKod'u alfanümerik gerçek kimlik ilk şablonda parse
	// olmayınca mesaj RAG doküman katmanına düşüp "yüklü dokümanlarda bu
	// bilgi yok" diyordu (aynı hastalığın v0.9.537'deki trace-ID hâli).
	// Şablon yine sürpriz yaparsa davranış dürüst kalsın: bundle "biçim
	// çözülemedi" der, köprü linki yine çıkar — doküman QA'sına düşmez.
	// SIRA: çözümlenebilir sinyallerin (trace/kimlik/span) HEPSİNDEN
	// sonra — mesajta hem span id hem bozuk blob varsa span kazanır.
	if tok, ok := reqid.FindLooseToken(raw); ok {
		return guidedRoute{Intent: guidedRequestID, RequestID: tok}
	}
	svc := extractServiceEntity(msg, services, envs)
	env := extractEnvEntity(msg, envs)
	// v0.10.1373 — ≥2 ortam anıldıysa tek ortama daraltma yok (en yoğun ortam
	// sessizce kazanıyordu); küme listesi rotaya yazılır (dönen her rotaya).
	if envSet := extractEnvSet(msg, envs); len(envSet) >= 2 {
		env = ""
		defer func() { route.EnvSet = envSet }()
	}
	team := extractTeamEntity(msg, teams)
	// v0.10.1455 — giden çağrı R2 (copilot_outbound_call_route.go): hedef
	// sözcüğü (canlı dış host / esb·mainframe… / "dış servis") + ölçü ipucu
	// topolojiden ve metrik grafiğinden ÖNCE ("ESB gecikme grafiği" sunucu
	// p99'u değil, "dış bağımlılıklarım yavaş mı" topoloji değil). Oracle /
	// kuyruk / log / pod / kaynak şekli ve yol anması kendi yollarında.
	if r, ok := routeOutboundCall(raw, msg, toks, svc, env, services, envs, extHosts, ctxService); ok {
		return r
	}
	// v0.10.1388 — topoloji (copilot_service_topology.go): AÇIK topoloji
	// sözcüğü (topoloji/upstream/bağımlılık/servis haritası) metrik grafiğinden
	// de önce kazanır ("topolojisini çiz, hatalılar" 5xx grafiği değil);
	// zayıf komşuluk fiili (çağıran/komşu/kime bağlı) grafikten SONRA denenir.
	topoStrong := hasStrongTopologySignal(toks)
	if topoStrong {
		if r, ok := routeTopologyAsk(raw, msg, toks, svc, env, services, envs, ctxService); ok {
			return r
		}
	}
	// v0.10.1386 — metrik grafiği (copilot_metric_chart.go): grafik fiili +
	// katalog metriği. Fan-out/periyot/çift/endpoint dallarından ÖNCE ("istek
	// hızı grafiği" endpoint aday turuna, "kafka lag trendi" servis-adayı
	// sorusuna savrulmasın); fiilsiz soru (heap/db "nasıl") bugünkü yolunda.
	if r, ok := routeMetricChart(raw, msg, toks, svc, env, services, envs, ctxService); ok {
		return r
	}
	// v0.10.1431 — kaynak incelemesi (copilot_resource_investigation.go): fiilsiz
	// cpu/bellek/restart/kaynak sorusu + pod/servis/workload hedefi. Grafik fiili
	// yukarıda metric_chart'ta kalır (1386 D2); heap/gc/jvm/thread/havuz v0.10.1432
	// (R2) ile buraya taşındı — pod_health envanter ("pod'ları nasıl") ve hedefsiz
	// filo heap sıralaması için kalır.
	if r, ok := routeResourceInvestigation(raw, msg, toks, svc, env, services, envs, ctxService); ok {
		return r
	}
	// v0.10.1388 — zayıf topoloji sinyali: fan-out/periyot/çift/endpoint'ten ÖNCE.
	if !topoStrong {
		if r, ok := routeTopologyAsk(raw, msg, toks, svc, env, services, envs, ctxService); ok {
			return r
		}
	}
	// v0.10.439 (D4) — fan-out: A→B→C; periyot ve çift dallarından ÖNCE
	// (cümle istek/giden sözcüğü de taşır).
	if hasFanoutSignal(toks) && hasPairRequestSignal(toks) {
		if aFrag, bFrag, cFrag, ok := splitFanoutFragments(raw); ok {
			aOpts, bOpts, cOpts := resolvePairSide(aFrag, services, envs), resolvePairSide(bFrag, services, envs), resolvePairSide(cFrag, services, envs)
			switch {
			case len(aOpts) == 1 && len(bOpts) == 1 && len(cOpts) == 1:
				return guidedRoute{Intent: guidedFanout, Service: aOpts[0], Env: env, PairFrom: aOpts[0], PairTo: bOpts[0], PairToKind: "service", FanoutTo: cOpts[0], FanoutToKind: "service"}
			case len(aOpts) == 1 && len(bOpts) == 1 && len(cOpts) == 0 && cFrag != "":
				return guidedRoute{Intent: guidedFanout, Service: aOpts[0], Env: env, PairFrom: aOpts[0], PairTo: bOpts[0], PairToKind: "service", FanoutTo: cFrag, FanoutToKind: "node"}
			case len(aOpts) > 1:
				return guidedRoute{Intent: guidedAskService, AskIntent: guidedFanout, ServiceOptions: aOpts, PairTo: bFrag, FanoutTo: cFrag, PairMissing: "from", Env: env}
			}
		}
	}
	// v0.10.438 (D3) — periyot sorusu: çift varsa A→B, yoksa tek servis;
	// hiçbiri yoksa sor. D2 çift dalından ÖNCE (aynı cümle istek sözcüğü
	// de taşır).
	// v0.10.443 — kapı: hata/log/deploy/problem şekilleri kendi yollarında
	// kalır ("cron loglarında hata var mı" log_errors, "düzenli olarak hata
	// alıyoruz" sağlık, "her gün 09:00'da deploy" deploy); özne yoksa yalnız
	// istek/çağrı sözcüğü varken sorulur, aksi hâlde düşer (eskiden koşulsuz
	// dönüp her şeyi yutuyordu).
	if hasPeriodSignal(toks) && !hasErrorSignal(toks) && !hasLogSignal(toks) && !hasDeploySignal(toks) && !hasProblemSignal(toks) {
		if fromFrag, toFrag, ok := splitPairFragments(raw); ok {
			fromOpts, toOpts := resolvePairSide(fromFrag, services, envs), resolvePairSide(toFrag, services, envs)
			switch {
			case len(fromOpts) == 1 && len(toOpts) == 1:
				return guidedRoute{Intent: guidedCallPeriod, Service: fromOpts[0], Env: env, PairFrom: fromOpts[0], PairTo: toOpts[0], PairToKind: "service"}
			case len(fromOpts) == 1 && len(toOpts) == 0 && toFrag != "":
				return guidedRoute{Intent: guidedCallPeriod, Service: fromOpts[0], Env: env, PairFrom: fromOpts[0], PairTo: toFrag, PairToKind: "node"}
			case len(fromOpts) > 1:
				return guidedRoute{Intent: guidedAskService, AskIntent: guidedCallPeriod, ServiceOptions: fromOpts, Env: env}
			}
		}
		if svc != "" {
			return guidedRoute{Intent: guidedCallPeriod, Service: svc, Env: env}
		}
		if opts := serviceCandidates(msg, services, envs, guidedServiceAskMax); len(opts) == 1 {
			return guidedRoute{Intent: guidedCallPeriod, Service: opts[0], Env: env}
		} else if len(opts) > 1 {
			return guidedRoute{Intent: guidedAskService, AskIntent: guidedCallPeriod, ServiceOptions: opts, Env: env}
		}
		if ctxService != "" {
			return guidedRoute{Intent: guidedCallPeriod, Service: ctxService, Env: env}
		}
		if hasPairRequestSignal(toks) {
			return guidedRoute{Intent: guidedAskService, AskIntent: guidedCallPeriod, Env: env}
		}
	}
	// v0.10.436 (D2a) — "A'dan B'ye giden istekler": çift, aile/kıyas
	// dallarından ÖNCE (iki ad + istek sözcüğü aileye düşmesin). Kaynak
	// servis şart; hedef servis ya da dış düğüm parçası.
	if hasPairRequestSignal(toks) {
		if fromFrag, toFrag, ok := splitPairFragments(raw); ok {
			fromOpts := resolvePairSide(fromFrag, services, envs)
			toOpts := resolvePairSide(toFrag, services, envs)
			switch {
			case len(fromOpts) == 1 && len(toOpts) == 1:
				return guidedRoute{Intent: guidedPairRequests, Service: fromOpts[0], Env: env, PairFrom: fromOpts[0], PairTo: toOpts[0], PairToKind: "service"}
			case len(fromOpts) == 1 && len(toOpts) == 0 && toFrag != "":
				return guidedRoute{Intent: guidedPairRequests, Service: fromOpts[0], Env: env, PairFrom: fromOpts[0], PairTo: toFrag, PairToKind: "node"}
			case len(fromOpts) > 1:
				other := toFrag
				if len(toOpts) == 1 {
					other = toOpts[0]
				}
				return guidedRoute{Intent: guidedAskService, AskIntent: guidedPairRequests, ServiceOptions: fromOpts, PairTo: other, PairMissing: "from", Env: env}
			case len(fromOpts) == 1 && len(toOpts) > 1:
				return guidedRoute{Intent: guidedAskService, AskIntent: guidedPairRequests, ServiceOptions: toOpts, PairFrom: fromOpts[0], PairMissing: "to", Env: env}
			}
		}
	}
	// v0.10.688 — "X isteklerini getir": yolunda X geçen endpoint adayları (SOR);
	// "/yol trace'lerini getir" (aday çipi) → doğrudan trace listesi (endpoint_traces.go).
	if q, confirmed, ok := extractEndpointRequest(raw, msg, toks, services, envs, svc); ok {
		r := guidedRoute{Intent: guidedEndpointCandidates, SearchText: q, Env: env, TraceErrorsOnly: hasErrorSignal(toks)}
		if confirmed {
			r.Intent = guidedEndpointTraces
		}
		return r
	}
	// v0.10.436 (D2b) — "X servisinde içinde <parça> geçen trace'ler".
	if frag, isSQL, ok := extractTraceSearch(raw, toks); ok {
		tsvc := svc
		if tsvc == "" {
			cleaned := strings.NewReplacer(strings.ToLower(frag), " ").Replace(msg)
			switch opts := serviceCandidates(cleaned, services, envs, guidedServiceAskMax); {
			case len(opts) == 1:
				tsvc = opts[0]
			case len(opts) > 1:
				return guidedRoute{Intent: guidedAskService, AskIntent: guidedTraceSearch, ServiceOptions: opts, SearchText: frag, SearchSQL: isSQL, Env: env}
			}
		}
		return guidedRoute{Intent: guidedTraceSearch, Service: tsvc, Env: env, SearchText: frag, SearchSQL: isSQL}
	}
	// v0.10.1133 — "<ORACLE_OPERASYON_ADI> operasyonuna ait trace'leri getir":
	// trace_search; bundle adı fonksiyon koduna çevirir (trace_nl_search.go).
	// Servis/ortam adıyla çakışan büyük harfli kelime operasyon sayılmaz
	// ("PAYMENT_SERVICE operasyonları" aile trace'lerine kalır).
	if oracleOps {
		if op, ok := extractOracleOpTraceRequest(raw, toks, append(append([]string{svc, env}, services...), envs...)); ok {
			return guidedRoute{Intent: guidedTraceSearch, Service: svc, Env: env, SearchText: op, TraceErrorsOnly: hasErrorSignal(toks)}
		}
	}
	// v0.10.465 (D2) — "… hatalı/yavaş trace'ler(i getir)": trace kökü + hata/yavaş
	// → aile/çoklu/tek servis trace LİSTESİ (family_traces.go). Kıyas ve aile
	// sağlık dallarından ÖNCE: trace kökü olmayan "mobile bff hataları" oraya kalır.
	if r, ok := routeFamilyTraces(msg, toks, svc, env, services, envs, ctxService); ok {
		return r
	}
	// v0.9.422 (CoSRE fikir #7) — çoklu tam-ad kıyası: soru 2+ canlı
	// servisi ADIYLA anıyor ve sağlık/hata/kıyas şekliyse familyHealth
	// yan-yana RED karşılaştırması zaten işi yapar. Tek-ad çözümü
	// (en uzun) bu durumda yanlış daraltmaydı.
	if multi := extractServiceEntities(msg, services); len(multi) >= 2 &&
		(hasHealthSignal(toks) || hasErrorSignal(toks) || hasCompareSignal(toks)) {
		return guidedRoute{Intent: guidedFamilyHealth, Env: env, Family: multi}
	}
	// Family resolution (v0.9.192 — operator-reported: "mobile
	// bff'lerde hangisinde hata var" servis bulamıyordu): tek servis
	// çözülmediyse ve soru sağlık/hata/problem şekilliyse, mesajın
	// ad-parçaları (mobile + bff) 2+ canlı servisi aile olarak
	// yakalayabilir. Açık tek-servis eşleşmesi HER ZAMAN aileden önce
	// gelir (svc != "" iken hiç denenmez).
	var family []string
	if svc == "" && (hasHealthSignal(toks) || hasErrorSignal(toks) || hasProblemSignal(toks)) {
		family = extractServiceFamily(msg, services, envs)
	}
	// Context-awareness (v0.9.164): mesaj bir servis ADI taşımıyorsa ve
	// frontend geçerli (katalogda olan) bir sayfa-servisi geçirmişse onu
	// varsayılan al — "neden yavaş?" checkout sayfasında → checkout. Şeffaf:
	// banner scope'u söyler. Mesajda açık servis varsa ELLEMEZ (kullanıcı
	// başka servisi kastediyorsa context ezmez). Aile yakalandıysa da
	// ELLEMEZ — "mobile bff'ler" sorusu checkout sayfasında checkout'a
	// daralmamalı.
	svcFromCtx := false
	if svc == "" && len(family) == 0 && ctxService != "" {
		for _, s := range services {
			if s == ctxService {
				svc = ctxService
				svcFromCtx = true
				break
			}
		}
	}
	// v0.10.429 (D1) — servis-şekilli soru, çözülmemiş ad: mesajın
	// jetonları canlı adların parçalarına oturuyorsa TEK aday doğrudan
	// yönlenir ("mobile commercial bff" → mobile-commercial-bff-prod),
	// 2+ aday "hangisini kastettin?" olur (sessiz none yerine). Takım
	// çözüldüyse takım dalı önce (aşağıda) — buraya girmez. Aile (2+
	// parça-eşi) yakalandıysa ve soru sağlık/hata şekliyse aile rotası
	// (v0.9.192, yan yana RED) korunur; ama neden/yavaş/deploy/log gibi
	// TEK servis isteyen şekillerde aile eskiden sessizce düşüp soru filo
	// geneline çöküyordu ("login neden yavaş" → servissiz slow_traces) —
	// şimdi aile üyeleri aday olarak sorulur.
	//
	// v0.10.433 (D5) — alan süzgeçli log araması ÖNCE çıkarılır: "message
	// alanında 'x' geçen loglar" cümlesindeki "message" jetonu aday üreticide
	// bir servisin parçasına oturup soruyu sessizce o servise daraltabilirdi;
	// alan sorgusu varken aday aranmaz (servis yalnız açık eşleşmeyle).
	lfField, lfValue, lfContains, lfOK := extractLogFieldQuery(raw, toks)
	if lfOK {
		// Alan adı ve tırnaklı değer servis eşleştiricisinden GİZLENİR:
		// "message alanında 'timeout' geçen loglar" cümlesindeki "message"
		// önek eşleşmesiyle message-broker'a çözülüyordu.
		cleaned := strings.NewReplacer(strings.ToLower(lfField), " ", strings.ToLower(lfValue), " ").Replace(msg)
		svc = extractServiceEntity(cleaned, services, envs)
	}
	if svc == "" && team == "" && !lfOK {
		// v0.10.1337 — servis raporu da sağlık gibi: aile yakalandıysa yan yana
		// RED (family_health) korunur, aile üyeleri aday olarak sorulmaz.
		if ask := serviceIntentFor(msg, toks); ask != guidedNone && (len(family) == 0 || (ask != guidedServiceHealth && ask != guidedServiceReport)) {
			opts := family
			if len(opts) == 0 {
				opts = serviceCandidates(msg, services, envs, guidedServiceAskMax)
			}
			switch {
			case len(opts) == 1:
				svc = opts[0]
			case len(opts) > 1:
				if len(opts) > guidedServiceAskMax {
					opts = opts[:guidedServiceAskMax]
				}
				return guidedRoute{Intent: guidedAskService, AskIntent: ask, ServiceOptions: opts, Env: env}
			case ask == guidedRootCause && (hasHealthSignal(toks) || hasSlowTraceSignal(msg)) &&
				!hasErrorSignal(toks) && !hasProblemSignal(toks) && !hasMessagingSignal(toks) && !hasDBSignal(toks) && !hasPodSignal(toks):
				// v0.10.434 (D7a) — "yavaşlığın sebebi ne?" / "neden yavaş?":
				// özne yok, aday yok, sayfa bağlamı yok. Eskiden filo geneli
				// en-yavaş listesine çöküyordu (nedensellik sessizce düşüyordu);
				// şimdi sorar, adayları bundle açık problemlerden doldurur.
				// "neden hata alıyoruz" filo-geneli problems yolunda kalır.
				return guidedRoute{Intent: guidedAskService, AskIntent: guidedRootCause, Env: env}
			case ask == guidedServiceReport && serviceReportAskable(msg, toks):
				// v0.10.1337 — "servisi analiz et, rapor çıkar" öznesiz: rapor tek
				// servisin; sor (adayları bundle açık problemlerden doldurur).
				return guidedRoute{Intent: guidedAskService, AskIntent: guidedServiceReport, Env: env}
			}
		}
	}
	switch {
	// v0.9.375 — iyelik takım sinyali HER ŞEYDEN önce: "takımımın açık
	// problemleri" generic problem yolundan önce yakalanmalı, yoksa tüm
	// filonun problemleri döner ve "takımımın" kelimesi sessizce düşer.
	// v0.10.1569 — ya da cümle iyelikle BAŞLIYOR ("takımının servisleri nasıl?",
	// "ekibin problemleri") ve adı geçen takım / servis yok: kendi takımı.
	case hasTeamSelfSignal(toks) || (team == "" && svc == "" && leadingTeamPossessive(toks)):
		// v0.9.650 — exception kelimesi problem/hata sinyalinden ÖNCE:
		// "takımımın exception'ları" ikisini birden taşıyor ve problems
		// dalı kazanırsa soru sessizce "açık problemler"e çöker.
		// v0.10.1385 (operatör: "Ekibimin teknik hataları var mı …") — "teknik
		// hata" ve "takımımda hata var mı" varlık sorusu da Exceptions kutusuna
		// (hasTeamExceptionAsk). "takımımın hataları neler" problemlerde kalır.
		if hasExceptionWord(toks) || (hasTeamExceptionAsk(toks) && !hasTopErrorSignal(toks)) {
			return guidedRoute{Intent: guidedMyExceptions, Env: env, TechErr: hasTechErrorWord(toks)}
		}
		if hasTopErrorSignal(toks) { // v0.10.1256 — "takımımın en hatalı servisleri"
			return guidedRoute{Intent: guidedMyTopErrors, Env: env}
		}
		if hasProblemSignal(toks) || hasErrorSignal(toks) {
			return guidedRoute{Intent: guidedMyProblems, Env: env}
		}
		return guidedRoute{Intent: guidedMyServices, Env: env}
	// v0.9.1134 — ADI GEÇEN takım, iyelik dallarından HEMEN SONRA ve
	// servis-adı dallarından ÖNCE.
	//
	// Neden buraya: (1) iyelik ("takımımın servisleri") kimlikten çözülür
	// ve mesajda ad taşımaz — o dal önce kalmalı, yoksa "benim takımım
	// orionsy" gibi bir cümlede ad, kimliği ezerdi. (2) Servis
	// dallarından önce, çünkü bir takım SERVİSLE AYNI ADI taşıyabilir
	// ("payments" hem takım hem servis) ve o durumda çıplak ad
	// gölgelenirse "hangi takım?" diyaloğu ÇALIŞMAZ: operatörün tıkladığı
	// çip sessizce servis sağlığına giderdi. Aynı gerekçe hasTeamSelfSignal
	// için v0.9.375'te de verilmişti (kapsam kelimesi sessizce düşmesin).
	//
	// Gölgeleme bedeli bilerek KAPI ile sınırlandı — takım dalı yalnız üç
	// hâlde açılır:
	//   a) mesaj SADECE takım adı (çip turu),
	//   b) takım/ekip kelimesi de var ("payments takımının servisleri"),
	//   c) servis çözülmedi VE liste/sağlık/hata şekli var
	//      ("orionsy servisleri nasıl").
	// (c)'deki `svc == ""` şartı sayesinde "payments neden yavaş" gibi
	// SERVİS soruları takıma kaçmaz; ada ek bir sinyal gerekmesi de
	// "payments hataları" sorusunu servis tarafında bırakır.
	case team != "" && (isBareTeamAsk(msg, team) || hasTeamWord(toks) ||
		(svc == "" && (hasServiceListWord(toks) || hasHealthSignal(toks) || hasErrorSignal(toks)))):
		// v0.10.1385 — adı geçen takımın teknik hataları / exception'ları →
		// Exceptions kutusu (team_exceptions); düz "hataları" takım servislerinde.
		if hasExceptionWord(toks) || hasServiceExceptionSignal(toks) {
			return guidedRoute{Intent: guidedTeamExceptions, Team: team, Env: env, TechErr: hasTechErrorWord(toks)}
		}
		// v0.10.1402 — "platform takımının en hatalı servisleri": iyelik
		// dalındaki my_top_errors'ın adı geçen takım hâli (team_services
		// aynı cevabı verseydi Takımım kartının iki çipi tek cevaba düşerdi).
		if hasTopErrorSignal(toks) {
			return guidedRoute{Intent: guidedTeamTopErrors, Team: team, Env: env}
		}
		return guidedRoute{Intent: guidedTeamServices, Team: team, Env: env}
	// v0.10.433 (D5) — alan süzgeçli log araması, "yavaş"/"hata" gibi
	// içerik sözcükleri taşıyabilen tırnaklı değerlerden ÖNCE: "message
	// alanında 'slow' geçen loglar" slow_traces'a kaçmasın.
	// v0.10.434 (D7b) — "sayfasını aç": sayfa türü + özne; overview özne
	// ister (yoksa copilotChatGuided önceki turdan devralır ya da sorar),
	// problems/logs/traces/endpoints filo geneli de açılabilir.
	case hasOpenPageSignal(toks):
		return guidedRoute{Intent: guidedOpenPage, Service: svc, Env: env, Page: openPageKind(toks)}
	case lfOK:
		return guidedRoute{Intent: guidedLogField, Service: svc, Env: env, LogField: lfField, LogValue: lfValue, LogContains: lfContains}
	// v0.9.514 — kök-neden, spesifik sinyallerden ÖNCE. "neden checkout
	// yavaşladı" hem why hem slow sinyali taşır; slow yolu kazanırsa soru
	// "yavaş mı"ya çöker ve NEDENSELLİK sessizce düşer. Servis şart:
	// hipotez bir anchor'a bağlıdır, servissiz "neden hata alıyoruz"
	// aşağıdaki problems yoluna düşsün (filo geneli zaten o).
	// v0.10.1276 (CoSRE adım 7) — servis TARAMASI, why/slow/log/pod dallarından
	// ÖNCE: "servisin tüm podlarını operationlarını fonksiyonlarını tarasın …
	// loglarına baksın" hem pod hem log sinyali taşır; o dallar kazanırsa
	// operasyon/fonksiyon taraması sessizce düşer. Servis şart (tarama tek
	// servisin); yeni intent yok — servis sağlığı, derin kip.
	case svc != "" && hasSweepSignal(toks):
		return guidedRoute{Intent: guidedServiceHealth, Service: svc, Env: env, Deep: true}
	// v0.10.1337 — servis RAPORU ("X servisini analiz et", "X için rapor çıkar"):
	// taramadan SONRA (tarama sözcüğü kazanır), neden dalından ÖNCE. Log /
	// deploy / pod / db / messaging sinyali taşıyan analiz isteği kendi
	// spesifik yolunda kalır ("X loglarını analiz et" bugünkü rotası).
	case svc != "" && serviceReportShape(toks): // hasServiceReportSignal && !log/deploy/pod/db/messaging
		return guidedRoute{Intent: guidedServiceReport, Service: svc, Env: env}
	case svc != "" && hasWhySignal(toks):
		return guidedRoute{Intent: guidedRootCause, Service: svc, Env: env}
	case hasSlowTraceSignal(msg):
		return guidedRoute{Intent: guidedSlowTraces, Service: svc, Env: env}
	case hasDeploySignal(toks):
		return guidedRoute{Intent: guidedDeployImpact, Service: svc, Env: env}
	case hasLogSignal(toks) && hasErrorSignal(toks):
		return guidedRoute{Intent: guidedLogErrors, Service: svc, Env: env}
	case hasPodSignal(toks):
		// v0.9.376 — servisli soru o servisin pod'ları, servissiz soru
		// filo-geneli JVM heap sıralaması (ikisi de bundle'da).
		return guidedRoute{Intent: guidedPodHealth, Service: svc, Env: env}
	// v0.9.420 — bağımlılık sağlığı. Messaging DB'den önce ("kafka db'ye
	// yazamıyor" → messaging tarafı daha spesifik ipucu). Servis çözülse
	// bile bağımlılık sinyali kazanır ("payments db hataları" → DB
	// kırılımı; bundle servis notunu düşer).
	case hasMessagingSignal(toks):
		return guidedRoute{Intent: guidedMessagingHealth, Service: svc, Env: env}
	case hasDBSignal(toks):
		return guidedRoute{Intent: guidedDBHealth, Service: svc, Env: env}
	// v0.9.416 — vardiya özeti: spesifik sinyallerden (slow/deploy/log/
	// pod) SONRA, problems'tan ÖNCE — "dün gece problem var mıydı" özet
	// cevabı hak eder (problemler zaten bundle'ın ilk bloğu).
	case hasShiftSignal(msg, toks):
		return guidedRoute{Intent: guidedShiftSummary, Service: svc, Env: env}
	case len(family) >= 2:
		return guidedRoute{Intent: guidedFamilyHealth, Env: env, Family: family}
	case hasProblemSignal(toks):
		return guidedRoute{Intent: guidedProblems, Service: svc, Env: env}
	// v0.10.1225 — operatör: servis sayfasında "Github copilot nasıl
	// kurabilirim" sayfa servisinin sağlık cevabına düşüyordu (genel "nasıl"
	// + ekran servisi). Servis yalnız ekrandan geliyorsa ve soru nasıl-yapılır
	// şeklindeyse genel sağlık jetonu yetmez — soru ajana/wiki'ye gider.
	// v0.10.1252 (operatör: "problem yerine exceptions / teknik hatalar") —
	// servisli exception sorusu sağlığa değil exception gruplarına.
	case svc != "" && hasServiceExceptionSignal(toks):
		return guidedRoute{Intent: guidedServiceExceptions, Service: svc, Env: env}
	// v0.10.1256 — "X en hatalı operasyonları" / "en hatalı servisler".
	case svc != "" && hasTopErrorSignal(toks) && hasOperationWord(toks):
		return guidedRoute{Intent: guidedTopErrorOps, Service: svc, Env: env}
	case svc == "" && len(family) == 0 && hasTopErrorSignal(toks) && hasServiceWord(toks):
		return guidedRoute{Intent: guidedTopErrorServices, Env: env}
	case svc != "" && (hasHealthDomainSignal(toks) || hasErrorSignal(toks) ||
		(hasHealthSignal(toks) && !(svcFromCtx && hasHowToShape(toks)))):
		return guidedRoute{Intent: guidedServiceHealth, Service: svc, Env: env}
	case hasErrorSignal(toks):
		return guidedRoute{Intent: guidedProblems, Env: env}
	}
	// v0.10.463 (D1) — hiçbir niyet tanımadı: ad-şekilli mesaj varlık kademesine
	// (find_entity.go). Yalnız burada, tüm sinyal dallarından SONRA.
	// v0.10.470 (F2-3) — "X namespace'indeki servisler" / "namespace'leri listele"
	// (namespace_guided.go); varlık kademesinden ÖNCE (namespace sözcüğü
	// ad-şekil kapısından geçmezdi).
	if r, ok := routeNamespaceAsk(toks, env); ok {
		return r
	}
	if r, ok := routeFindEntity(msg, toks, svc, env, services, envs); ok {
		return r
	}
	return guidedRoute{}
}

// extractServiceFamily resolves a family-of-services ask against the
// LIVE service list (v0.9.192). Fragments = message tokens that occur
// inside ≥1 service name as a BOUNDED segment ("mobile", "bff" inside
// "mobile-home-bff-prod"; separators -_.). The family = services
// containing ALL fragments. <2 matches → nil (single-service paths
// already handle 1; zero fragments = not a name-shaped ask). >40 →
// nil: a lone generic fragment ("prod") must not claim the fleet.
func extractServiceFamily(msg string, services, envs []string) []string {
	envTok := map[string]bool{}
	for _, e := range envs {
		envTok[strings.ToLower(e)] = true
	}
	var frags []string
	for _, t := range guidedTokens(msg) {
		if len(t) < 3 || guidedStopwords[t] || envTok[t] || !asciiNameToken(t) {
			continue
		}
		for _, svc := range services {
			if segmentInName(strings.ToLower(svc), t) {
				frags = append(frags, t)
				break
			}
		}
	}
	if len(frags) == 0 {
		return nil
	}
	var fam []string
	for _, svc := range services {
		ls := strings.ToLower(svc)
		all := true
		for _, f := range frags {
			if !segmentInName(ls, f) {
				all = false
				break
			}
		}
		if all {
			fam = append(fam, svc)
		}
	}
	if len(fam) < 2 || len(fam) > 40 {
		return nil
	}
	return fam
}

// segmentInName reports whether t occurs in name as a whole segment —
// bounded by -_. separators or the name's ends ("bff" matches
// "mobile-bff-prod", never "rebuff-svc").
func segmentInName(name, t string) bool {
	for start := 0; ; {
		i := strings.Index(name[start:], t)
		if i < 0 {
			return false
		}
		i += start
		leftOK := i == 0 || name[i-1] == '-' || name[i-1] == '_' || name[i-1] == '.'
		r := i + len(t)
		rightOK := r == len(name) || name[r] == '-' || name[r] == '_' || name[r] == '.'
		if leftOK && rightOK {
			return true
		}
		start = i + 1
	}
}

// guidedRangeRe extracts "son 2 saat" / "last 30 minutes" style
// windows. Longer unit spellings come first in the alternation so
// "minutes" isn't half-eaten by "min".
var guidedRangeRe = regexp.MustCompile(`(\d+)\s*(gün|gun|days|day|saat|hours|hour|hrs|hr|dakika|dk|minutes|minute|mins|min)`)

// guidedRangeS derives the lookback window (seconds) from the
// question. Default 1800 (30m, the chat tools' default); bare unit
// words ("son bir saat", "today") map to 1h/1d. Clamped to
// [300, 86400] so a typo can't trigger a week-wide scan.
// guidedRangeRungs — ekrandan gelen aralığın oturtulacağı basamaklar.
// frontend'in PRESET_SECONDS kümesiyle aynı (1m … 30d).
var guidedRangeRungs = []int64{
	60, 300, 900, 1800, 3600, 10800, 21600, 43200,
	86400, 172800, 259200, 604800, 1209600, 2592000,
}

// snapRangeS — EKRANDAN gelen aralığı sabit basamaklara oturtur.
//
// Neden gerekli (v0.9.529): ekrandaki aralık MUTLAK (custom from/to)
// olabilir ve o hâlde keyfi bir saniye sayısı üretir — "6 saat 3 dakika
// 17 saniye". Bu değer guided prefetch'lerin sunucu cache anahtarlarına
// giriyor; sınırsız kardinalite, her sorunun kendi cache satırını
// yazması demek. Aynı sınıf v0.8.270'te ES tarafında yakalanmıştı.
//
// YUKARI oturtulur: pencere operatörün gördüğünü KAPSAMALI. Aşağı
// oturtmak "6 saate bakıyorum ama cevap 3 saatlik" demek olurdu ve
// eksik rapor, biraz fazla okumadan kötüdür.
func snapRangeS(v int64) int64 {
	if v <= 0 {
		return 0
	}
	for _, r := range guidedRangeRungs {
		if v <= r {
			return r
		}
	}
	return guidedRangeRungs[len(guidedRangeRungs)-1] // 30 günde tavan
}

func guidedRangeS(raw string) int64 {
	v, _ := guidedRangeSExplicit(normalizeGuidedMsg(raw))
	return v
}

// guidedRangeSExplicit (v0.9.410) — guidedRangeS'in çekirdeği; ikinci
// dönüş, mesajın AÇIK bir pencere içerip içermediğini söyler (takip
// devralması "peki payments?" için önceki sorunun penceresini ancak
// mevcut soru açık pencere TAŞIMIYORSA korumalı). msg normalize
// edilmiş olmalı.
func guidedRangeSExplicit(msg string) (int64, bool) {
	explicit := true
	rangeS := int64(1800)
	if m := guidedRangeRe.FindStringSubmatch(msg); m != nil {
		n := int64(0)
		_, _ = fmt.Sscanf(m[1], "%d", &n) // regex zaten \d+ garanti eder
		switch unit := m[2]; {
		// "dk"/"dakika" also start with 'd' — day units must be
		// matched by full stem, never a bare 'd' prefix (this exact
		// branch is pinned by TestGuidedRangeS, the unit-mixing rule).
		case strings.HasPrefix(unit, "gün") || strings.HasPrefix(unit, "gun") || strings.HasPrefix(unit, "day"):
			rangeS = n * 86400
		case strings.HasPrefix(unit, "saat") || strings.HasPrefix(unit, "hour") || strings.HasPrefix(unit, "hr"):
			rangeS = n * 3600
		default: // dakika | dk | minute | min
			rangeS = n * 60
		}
	} else if strings.Contains(msg, "saat") || strings.Contains(msg, "hour") {
		rangeS = 3600
	} else if strings.Contains(msg, "gün") || strings.Contains(msg, "day") ||
		strings.Contains(msg, "bugün") || strings.Contains(msg, "today") {
		rangeS = 86400
	} else {
		explicit = false
	}
	if rangeS < 300 {
		rangeS = 300
	}
	if rangeS > 86400 {
		rangeS = 86400
	}
	return rangeS, explicit
}

// fmtAgoTR renders "how long ago" in compact Turkish units. EVERY
// unit branch is exercised by TestFmtAgoTR (the Nh/Nd unit-mixing
// rule). Negative deltas (clock skew) clamp to 0.
func fmtAgoTR(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	switch {
	case seconds < 60:
		return fmt.Sprintf("%dsn", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%ddk", seconds/60)
	case seconds < 86400:
		h, m := seconds/3600, (seconds%3600)/60
		if m == 0 {
			return fmt.Sprintf("%dsa", h)
		}
		return fmt.Sprintf("%dsa %ddk", h, m)
	default:
		d, h := seconds/86400, (seconds%86400)/3600
		if h == 0 {
			return fmt.Sprintf("%dgün", d)
		}
		return fmt.Sprintf("%dgün %dsa", d, h)
	}
}

// ─── Entry point (called from copilotChat before the tool loop) ─────

// copilotChatGuided tries the guided path for the last user message.
// Returns handled=false when the router doesn't match or a primary
// prefetch fails — the caller then runs the free tool loop unchanged.
// handled=true means the exchange is complete (answer or error
// emitted); ok mirrors the `done` event's success flag.
// explain (v0.9.479) — AI çekmecesinden gelen "ekrandaki açıklama"
// bağlamı; boşken bu fonksiyonun davranışı bayt-bayt eskisidir.
// ctxRangeS (v0.9.529) — operatörün EKRANDAKİ zaman aralığı, saniye.
// 0 = bilgi yok (eski istemci); o hâlde davranış bayt-bayt eskisidir.
// guidedInheritsScreenTrace — v0.10.944: ekrandaki trace kimliği "bu trace"
// sorusuna trace_by_id rotası olarak devredilsin mi. Çekmecede Explain
// bağlamı VARSA hayır: çekmece zaten o trace'in açıklamasını ve kanıtını
// taşıyor (drawer katmanı cevaplar). Devralınan TraceID, drawerSuppressesGuided'ın
// YAPIŞTIRILMIŞ kimlik muafiyetine takılıp soruyu çekmeceden koparıyor ve
// açıklama-biçimli soruya "Explain açılıyor" hazır cevabı döndürüyordu
// (v0.10.944 çekmece bağlamı her turda `trace` göndermeye başlayınca).
// Soruya YAPIŞTIRILAN kimlik bu kapıdan geçmez; muafiyeti aynen durur.
func guidedInheritsScreenTrace(ctxTrace, explain string) bool {
	return ctxTrace != "" && strings.TrimSpace(explain) == ""
}

// ctxTrace (v0.9.537) — ekrandaki trace ID'si (/trace?id=), "" = yok.
func (s *Server) copilotChatGuided(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, ctxService, ctxOperation, explain string, ctxRangeS int64, ctxTrace, ctxEnv string, anchorTo time.Time, tzOffsetMin int, tzName string) (handled, ok bool) {
	question := strings.TrimSpace(lastUserText(msgs))
	if question == "" {
		return false, false
	}
	norm := normalizeGuidedMsg(question)
	// v0.9.410 — takip soruları ("peki payments?", "son 24 saatte?")
	// guided sinyal taşımasa da katalog okumasını hak eder; devralma
	// applyFollowUpContext'te (copilot_followup.go).
	prior := priorUserTexts(msgs)
	followCue := len(prior) > 0 && isFollowUpCue(norm)
	// v0.9.1134 — ÇIPLAK takım adı hiçbir guided kelime taşımaz ("hangi
	// takım?" çipinin gönderdiği tur), o yüzden kısa + ad-şekilli mesajlar
	// da katalog okumasını hak eder (mayNameTeam; üç okuma da 60sn cache'li).
	// v0.10.434 (D7c) — sözlük, sinyal kapısından ÖNCE: "p95 ne demek" sağlık
	// sinyali taşır (ask_service'e savrulurdu), "requestid nedir" hiçbir
	// sinyal taşımaz (RAG'a düşerdi). LLM yok, exchangeId yok (glossary.go).
	if _, entry, ok := glossaryLookup(norm); ok {
		emit("answer", glossaryAnswer(entry))
		return true, true
	}
	// v0.10.437 (D6) — mutlak tarih/saat şekli de katalog okumasını hak
	// eder ("08/08/2026 04-08 ile 08-09 arası servis süreleri" başka
	// hiçbir sinyal taşımıyor).
	absShape := looksLikeAbsoluteWindow(question)
	// v0.10.463 (D1) — bulma fiili taşıyan uzunca cümle de katalog okumasını
	// hak eder ("mobile bff'yi bulabilir misin lütfen" 6 jeton, mayNameTeam 5'te
	// keser).
	findCue := hasFindSignal(guidedTokens(norm))
	affirm := isAffirmative(norm) // v0.10.688 — çıplak "evet" bağlamdaki aday turunu onaylar
	// v0.10.819 — yapıştırılan hata metni ("Caused by: java.lang…") hata/log
	// sözcüğü taşımayabilir; şekli katalog okumasını hak eder (copilot_pasted_error.go).
	pasted := looksLikePastedError(question)
	// v0.10.1327 — hedef sorusu ("nereye giderken timeout almış") guided
	// kelime taşımayabilir; önceki tur gecikme/hata andıysa katalog okumasını
	// hak eder (geçmişsiz ya da konusu başka geçmişli soru bugünkü yolunda).
	peerCue := len(prior) > 0 && hasPeerSignal(guidedTokens(norm)) && prevTurnMentionsLatencyFailure(msgs, chatLastIntent(ctx))
	// v0.10.1454 — ön-getirmenin dış-yol sondası HIT verdi (yol bir client
	// span'inin url.path'i): rota outbound_call, sinyal kapısından ÖNCE (mesaj
	// "grafikleri ortalamayı göster" guided sinyali taşımayabilir).
	if hit := outboundSlotFrom(ctx).get(); hit != nil {
		return s.guidedOutboundRoute(ctx, emit, *hit, question, norm, msgs, explain, ctxService, ctxOperation, anchorTo, chatLocationNamed(tzName, tzOffsetMin))
	}
	if !hasGuidedSignal(norm) && !followCue && !mayNameTeam(norm) && !absShape && !findCue && !peerCue && !affirm && !pasted {
		return false, false // zero-cost fast path: no catalogue read
	}
	svcNames, envNames := s.guidedServiceNames(ctx), s.guidedEnvNames(ctx)
	teamNames := s.guidedTeamNames(ctx)
	route := routeGuidedIntentExt(question, svcNames, envNames, teamNames, ctxService, s.oracleOpsEnabled(), s.guidedOutboundHosts(ctx, norm))
	// v0.10.479 (F4-2, G10) — TAKİP MUTASYONU: "son 1 saate genişlet" / "sadece
	// hatalı olanlar" / "bunun pod'larını göster" / "aynı filtreyle loglara bak"
	// → aktif bağlamın son rotası klonlanır, alan değişir, aynı dispatch; router'ın
	// bunları yeni soru sanmasından ÖNCE (chat_followups.go).
	if st := chatContextFromCtx(ctx); st != nil && st.ctx.LastRoute != nil {
		// v0.10.688 — ONAY: son tur endpoint adaylarıysa "evet/tamam/hepsi/olur/ok/getir"
		// aynı sorguyla trace listesine gider (tur-arası durum yok; LastRoute yeter).
		if affirm {
			if er, ok := endpointAffirmativeRoute(st.ctx); ok {
				rs := st.ctx.RangeS
				if rs <= 0 {
					rs = endpointWindowS
				}
				emitGuidedContextStep(emit, "onay: endpoint trace'leri (bağlam)")
				handled, ok = s.runGuidedRoute(ctx, emit, er, rs, question, msgs, explain, ctxService, ctxOperation, "", anchorTo, chatLocationNamed(tzName, tzOffsetMin))
				if handled {
					s.noteChatContextRoute(ctx, er, rs, false)
				}
				return handled, ok
			}
		}
		explicitEntity := extractServiceEntity(norm, svcNames, envNames) != "" || hasNamespaceWord(guidedTokens(norm))
		if m, ok := detectContextMutation(norm, guidedTokens(norm), st.ctx, explicitEntity); ok {
			if mr, mrange, next, ok := applyContextMutation(st.ctx, m); ok {
				st.ctx, st.dirty = next, true
				emitGuidedContextStep(emit, "takip: "+m.Label+" (bağlam)")
				handled, ok = s.runGuidedRoute(ctx, emit, mr, mrange, question, msgs, explain, ctxService, ctxOperation, "", anchorTo, chatLocationNamed(tzName, tzOffsetMin))
				if handled {
					s.noteChatContextRoute(ctx, mr, mrange, m.Kind == "window")
				}
				return handled, ok
			}
		}
	}
	// v0.9.537 — "bu trace" / "ekrandaki trace" İD'siz sorulduğunda
	// ekrandaki trace'e otur: operatör /trace sayfasında, adres zaten
	// özneyi taşıyor. Mesajdaki AÇIK 32-hex her zaman kazanır
	// (routeGuidedIntent onu en üstte yakalar); burası yalnız hiçbir
	// rota çıkmadığında ve mesaj "trace" kökü taşıdığında devreye girer.
	// v0.9.570 — İŞARET ZAMİRİ KAPISI: operatör "BU trace" derken
	// ekrandaki şeyi soruyor ve bu, başka her niyeti geçersiz kılar.
	//
	// Öncesinde "bu trace neden yavaş?" filo geneli en yavaş 10 trace
	// listesi döndürüyordu: hasSlowTraceSignal sınırsız Contains ile
	// "ned|EN YAVAŞ" yakalıyor, slow_traces dalı ctxTrace
	// devralmasından ÖNCE geliyordu. Sınır eşleşmesi (v0.9.570) o
	// kazayı bitirdi ama "neden yavaş" artık AÇIK kalıp — yani bu kapı
	// olmadan aynı soru yine listeye giderdi.
	//
	// Ayrım şu: "en yavaş trace'ler" bir LİSTE sorusu, "bu trace neden
	// yavaş?" EKRANDAKİ şeyi soruyor. İşaret zamiri o ayrımı taşıyan
	// tek sinyal.
	inheritTrace := guidedInheritsScreenTrace(ctxTrace, explain) // v0.10.944
	if tr, ok := topologyInheritTrace(route, ctxTrace); ok && inheritTrace && hasDemonstrativeTrace(norm) {
		// v0.10.1388 — "bu trace'in topolojisi": ekrandaki trace'in grafiği (trace_by_id değil).
		route = tr
		emitGuidedContextStep(emit, "bağlam: ekrandaki trace ("+ctxTrace+")")
	} else if inheritTrace && hasDemonstrativeTrace(norm) {
		route = guidedRoute{Intent: guidedTraceByID, TraceID: ctxTrace}
		emitGuidedContextStep(emit, "bağlam: ekrandaki trace ("+ctxTrace+")")
	} else if route.Intent == guidedNone && inheritTrace && tokenHasPrefix(guidedTokens(norm), "trace") {
		route = guidedRoute{Intent: guidedTraceByID, TraceID: ctxTrace}
		emitGuidedContextStep(emit, "bağlam: ekrandaki trace ("+ctxTrace+")")
	}
	// v0.9.529 — soru AÇIK bir pencere taşımıyorsa ekrandaki aralığı
	// kullan. Sabit 30dk, operatör 6 saatlik pencereye bakarken "hata
	// oranı ne" diye sorduğunda BAŞKA bir pencerenin cevabını veriyordu
	// ve fark görünmüyordu. Öncelik sırası bilinçli: sorunun açık
	// penceresi > ekran > 30dk varsayılanı. Soru her zaman ekrandan
	// güçlü — "son 24 saatte" yazan operatör ekranını değiştirmek
	// zorunda kalmamalı.
	// v0.10.478 (F4-1) — sohbet bağlamı VARSAYILAN: ekran servisi yoksa bağlamın
	// servisi; açık (set_context / "son 1 saate genişlet") pencere ekranı ezer.
	if st := chatContextFromCtx(ctx); st != nil {
		if ctxService == "" {
			ctxService = st.ctx.Service
		}
		if st.ctx.RangeExplicit && st.ctx.RangeS > 0 {
			ctxRangeS = st.ctx.RangeS
		}
	}
	rangeS, explicitRange := guidedRangeSExplicit(norm)
	if !explicitRange && ctxRangeS > 0 {
		rangeS = snapRangeS(ctxRangeS)
	}
	// v0.9.1259 — env devri (ctxRangeS deseninin aynası, v0.9.529
	// gerekçesi): soru açık env taşımıyorsa ekrandaki seçim kullanılır.
	// Öncelik bilinçli: sorunun açık env'i > ekran > (boş = tümü).
	// Çip şeffaflığı: bundle'lar env'i zaten arg yankısında gösteriyor
	// (withEnvArg) — devralınan env de oradan görünür.
	if route.Env == "" && ctxEnv != "" && len(route.EnvSet) < 2 { // v0.10.1373 — çok ortamlı soru ekran env'ini devralmaz
		route.Env = ctxEnv
		route.EnvInherited = true // v0.10.1468
	}
	if len(route.EnvSet) >= 2 { // v0.10.1373 — özet çipi: hangi ortamlar anıldı, süzgeç neden yok
		emitGuidedContextStep(emit, envSetNoteTR(route.EnvSet))
	}
	// v0.10.1380 — kısa biçimle anılan ortamın çözüldüğü canlı değer(ler)
	// görünür: "ortam adı çözüldü: eu-a → prod-eu-a". Tam adla anmada çip yok.
	if note := envAliasNoteTR(findEnvMentions(norm, envNames), append([]string{route.Env}, route.EnvSet...)); note != "" {
		emitGuidedContextStep(emit, note)
	}
	followBase := "" // devralınan temel mesaj (operasyon çözümü için)
	if followCue {
		if nr, nrange, base, changed := applyFollowUpContext(route, question, prior, svcNames, envNames, teamNames); changed {
			route, rangeS, followBase = nr, nrange, base
			scope := string(route.Intent)
			if route.Service != "" {
				scope = route.Service
			}
			emitGuidedContextStep(emit, "bağlam: "+scope+" (önceki sorudan)")
		}
	}
	// v0.10.1327 — hedef sorusu + önceki turda gecikme/hata → en yavaş trace
	// kırılımı (chat_trace_drill.go); geçmişsiz soru bugünkü rotasında kalır.
	if peerCue {
		if dr, ok := s.guidedTraceDrillOverride(ctx, route, norm, msgs, prior, ctxService, svcNames, envNames); ok {
			route = dr
		}
	}
	// v0.10.437 (D6) — mutlak pencere(ler): tek pencere çıpayı ve uzunluğu
	// ezer (hangi rota olursa olsun), iki pencere window_compare olur.
	// Rota none olsa da geçerli ("… arası servis süreleri" sinyalsiz).
	// v0.10.819 — yapıştırılan hata metnindeki log damgaları (iki tarih) rotayı
	// window_compare/ask_service'e ÇEVİRMEZ (inceleme 2026-09-19).
	if route.ChartPromQL != "" { // v0.10.1416 — yapıştırılan sorgu göreli pencereyle (sorgu içi damga pencere değil)
		absShape = false
	}
	if absShape && !(pasted && route.Intent == guidedLogField) {
		loc := chatLocationNamed(tzName, tzOffsetMin)
		if wins, ok := extractAbsoluteWindows(question, time.Now(), loc); ok {
			var label string
			route, anchorTo, rangeS, label = applyAbsoluteWindows(route, wins, ctxService, anchorTo, rangeS, loc, absWindowText(question))
			if label != "" {
				emitGuidedContextStep(emit, label)
			}
			if len(wins) == 1 && route.Intent == guidedNone && route.Service == "" && ctxService != "" {
				route = guidedRoute{Intent: guidedServiceHealth, Service: ctxService, Env: route.Env}
			}
		}
	}
	// v0.10.434 (D7b) — "sayfasını aç" öznesiz: takip ipucu olmasa da
	// önceki turun servisi devralınır; o da yoksa runGuidedRoute sorar.
	if route.Intent == guidedOpenPage && route.Service == "" && route.Page == "overview" {
		if p := newestPriorService(prior, svcNames, envNames); p != "" {
			route.Service = p
			emitGuidedContextStep(emit, "bağlam: "+p+" (önceki sorudan)")
		}
	}
	if route.Intent == guidedNone {
		// v0.10.470 (F2-3, G8) — kısa ad-şekilli mesaj serbest döngüye düşmeden
		// katalog indeksine sorulur (namespace_guided.go entityScanRoute); aday
		// yoksa handler false döner ve eski yol aynen sürer.
		fb, ok := entityScanRoute(norm)
		if !ok {
			return false, false
		}
		route = fb
	}
	// v0.9.479 — çekmece bağlamı varken SOMUT ÖZNEYE oturmayan rota
	// guided'a bırakılmaz: filo geneli prefetch ekrandaki konuyu
	// ıskalıyordu (operatör raporu — ekranda exception dururken filo
	// JVM GC anlatıldı). Explain-grounded yol devralır
	// (copilot_drawer.go). explain boşken bu daima false.
	if drawerSuppressesGuided(explain, route, question) {
		return false, false
	}
	// v0.9.416 — vardiya özeti varsayılanı 12h (guidedRangeS'in 30dk'sı
	// bir vardiyayı anlatamaz); soru açık pencere taşıyorsa o kazanır.
	if route.Intent == guidedShiftSummary {
		if _, explicit := guidedRangeSExplicit(norm); !explicit {
			rangeS = 12 * 3600
		}
	}
	// v0.10.1337 — servis raporu varsayılanı son 1 saat (operatör: "son 1
	// saatteki veya 3 saatteki teknik hata"); açık pencere kazanır.
	if route.Intent == guidedServiceReport {
		if _, explicit := guidedRangeSExplicit(norm); !explicit {
			rangeS = serviceReportDefaultRangeS
		}
	}
	// v0.10.1385 — takım teknik hataları varsayılanı son 24 saat ("en son"
	// grupları getirmek için; Oracle akışı durgun); açık pencere kazanır.
	if route.Intent == guidedMyExceptions || route.Intent == guidedTeamExceptions {
		if _, explicit := guidedRangeSExplicit(norm); !explicit {
			rangeS = teamExcDefaultRangeS
		}
	}
	// v0.10.1388 — topoloji varsayılanı son 1 saat (topology_edges_5m kovaları); açık pencere kazanır.
	if route.Intent == guidedServiceTopology || route.Intent == guidedTraceTopology {
		if _, explicit := guidedRangeSExplicit(norm); !explicit {
			rangeS = topoChatDefaultRangeS
		}
	}
	// v0.10.1455 — giden çağrı (yolsuz, R2) varsayılanı son 1 saat; açık pencere kazanır.
	if route.Intent == guidedOutboundCall {
		if _, explicit := guidedRangeSExplicit(norm); !explicit {
			rangeS = outboundDefaultRangeS
		}
	}
	// v0.10.1386 — metrik grafiği varsayılanı son 1 saat (D1; açık pencere 5 dk..24 sa kazanır).
	if route.Intent == guidedMetricChart {
		if route.ChartPromQL != "" { // v0.10.1416 — pencere sorgunun DIŞINDAKİ metinden ([1h] / "…_per_hour" sayılmaz)
			rangeS, explicitRange = pastedPromQLRangeS(question)
		} else if _, explicit := guidedRangeSExplicit(norm); !explicit {
			rangeS = metricChartDefaultRangeS
		}
	}
	// v0.10.1431 — kaynak incelemesi varsayılanı son 1 saat (açık pencere 5 dk..24 sa kazanır).
	if route.Intent == guidedResourceInvestigation {
		if _, explicit := guidedRangeSExplicit(norm); !explicit {
			rangeS = resDefaultRangeS
		}
	}
	// v0.10.688 — endpoint rotaları varsayılan son 1 saat (operatör kararı); açık
	// pencere ve "son 6 saate genişlet" kipi kazanır.
	if route.Intent == guidedEndpointCandidates || route.Intent == guidedEndpointTraces {
		if _, explicit := guidedRangeSExplicit(norm); !explicit {
			rangeS = endpointWindowS
		}
	}
	// v0.10.33 — ÇIPA. Eskiden koşulsuz `time.Now()`du: operatör dün gece
	// 03:00-04:00'a zoom yapıp soru sorduğunda, sohbet aynı UZUNLUKTA ama
	// BUGÜNKÜ pencereyi cevaplıyordu. Sayılar gerçek olduğu için hata
	// sessiz kalıyordu. anchorTo göreli aralıkta zaten `now()`dur
	// (chat_anchor.go); mutlak seçimde operatörün penceresidir.
	handled, ok = s.runGuidedRoute(ctx, emit, route, rangeS, question, msgs, explain, ctxService, ctxOperation, followBase, anchorTo, chatLocationNamed(tzName, tzOffsetMin))
	if handled {
		s.noteChatContextRoute(ctx, route, rangeS, explicitRange) // v0.10.478 (F4-1)
	}
	return handled, ok
}

// guidedDispatchSeam — v0.10.1464 TEST DİKİŞİ (prod'da nil; cosreInteractionSeam
// emsali). Yönlendirme regresyon kümesi (chat_routing_regression_test.go) gerçek
// copilotChat'ten geçen turun ROTA KARARINI burada görür; true dönerse demet
// koşmaz (store'suz test sunucusu prefetch yapamaz) ve tur guided olarak biter.
var guidedDispatchSeam func(route guidedRoute) bool

// runGuidedRoute — v0.10.172: ÇÖZÜLMÜŞ bir rota için prefetch → anlatım →
// answer. copilotChatGuided'dan ayrıldı ki LLM niyet sınıflandırıcısı
// (copilot_intent.go) deterministik router'ı atlayıp aynı paketleri ve aynı
// anlatım çağrısını kullanabilsin: iki yolun cevabı, çipleri ve linkleri
// birebir aynı üretimden çıkar. Davranış değişmedi — gövde olduğu gibi taşındı.
// loc (v0.10.758) — operatörün dilimi (chatLocationNamed): vardiya/periyot özetlerinin saatleri.
func (s *Server) runGuidedRoute(ctx context.Context, emit func(string, any), route guidedRoute, rangeS int64, question string, msgs []copilot.ChatMessage, explain, ctxService, ctxOperation, followBase string, anchorTo time.Time, loc *time.Location) (handled, ok bool) {
	if loc == nil {
		loc = time.UTC
	}
	to := anchorTo
	if to.IsZero() {
		to = time.Now()
	}
	from := to.Add(-time.Duration(rangeS) * time.Second)
	// v0.10.1138 — yapısal kapsam (chat_scope.go): verilen boyutta netleştirme
	// sorulmaz, env/takım/servis süzgeci rotaya yazılır. Kapsamsız ctx → aynen.
	route = applyChatScopeRoute(chatScopeFromCtx(ctx), route)
	chatExchangeFrom(ctx).noteRoute(string(route.Intent)) // v0.10.1153 — /ai etkileşim satırının rotası
	// v0.10.1464 — yönlendirme regresyon kümesinin dikişi (prod'da nil).
	if guidedDispatchSeam != nil && guidedDispatchSeam(route) {
		return true, true
	}

	// v0.10.434 (D7b) — open_page: overview özne ister; yoksa "hangisini
	// kastettin?" (çip: "X sayfasını aç"). Deterministik cevap, LLM yok.
	if route.Intent == guidedOpenPage && route.Service == "" && route.Page == "overview" {
		route.Intent, route.AskIntent = guidedAskService, guidedOpenPage
	}
	// v0.10.453 — trace_by_id: Explain'in çekirdeği BİREBİR (aynı prompt
	// çifti, aynı önbellek anahtarı, yüzey explain-trace:chat). Bulunamayan
	// trace eski dürüst kanıt yoluna (guidedTraceBundle) düşer.
	if route.Intent == guidedTraceByID {
		if handled, ok := s.guidedTraceExplain(ctx, emit, route, question, from, to, ctxService); handled {
			return handled, ok
		}
	}
	// v0.10.463 (D1) — find_entity: LLM'siz varlık kartı / aday çipleri / liste
	// (find_entity.go). Okuma başarısızsa serbest döngüye bırakır.
	if route.Intent == guidedFindEntity {
		// v0.10.470 (F2-3) — yalnız FindQuery: ucuz katalog taraması (servis /
		// namespace / karışık); aday yoksa eski yol.
		if route.Service == "" && len(route.ServiceOptions) == 0 && !route.FindList && route.FindQuery != "" {
			return s.guidedEntityScanAnswer(ctx, emit, route, from, to, rangeS)
		}
		if handled, ok := s.guidedFindEntityAnswer(ctx, emit, route, from, to, rangeS); handled {
			return handled, ok
		}
		return false, false
	}
	if route.Intent == guidedNamespaceServices { // v0.10.470 (F2-3)
		return s.guidedNamespaceServicesAnswer(ctx, emit, route, from, to, rangeS)
	}
	// v0.10.688 — endpoint adayları / trace listesi: LLM'siz (endpoint_traces.go).
	if route.Intent == guidedEndpointCandidates || route.Intent == guidedEndpointTraces {
		return s.guidedEndpointAnswer(ctx, emit, route, from, to, rangeS)
	}
	// v0.10.1388 — servis topolojisi / örnek trace topolojisi: LLM'siz (copilot_service_topology.go).
	if route.Intent == guidedServiceTopology || route.Intent == guidedTraceTopology {
		return s.guidedTopologyAnswer(ctx, emit, route, chatUserTexts(msgs), from, to, rangeS)
	}
	if route.Intent == guidedOutboundCall { // v0.10.1454 — giden çağrı: LLM'siz (copilot_outbound_call.go)
		return s.guidedOutboundAnswer(ctx, emit, route, chatUserTexts(msgs), from, to)
	}
	if route.Intent == guidedOffTopic { // v0.10.819 — LLM'siz kibar sınır + yol tarifi; v0.10.1458 — anahtar açıksa kısa genel cevap (chat_offtopic_general.go)
		return s.guidedOffTopicDispatch(ctx, emit, question, ctxService)
	}
	if route.Intent == guidedHowTo { // v0.10.809 — LLM'siz yol tarifi
		return s.guidedHowToAnswer(emit, route, question, ctxService)
	}
	if route.Intent == guidedSelfMeta { // LLM'siz: cevap yapılandırmada yazılı
		n := emitGuidedStep(emit, "self_meta", "")
		text := s.selfMetaAnswerTR(ctx)
		emitGuidedStepResult(emit, n, "self_meta", text, nil)
		emit("answer", map[string]any{"text": text, "suggestions": guidedSuggestions(route)})
		return true, true
	}
	if route.Intent == guidedOpenPage {
		links := dedupLinksByHref(guidedAnswerLinks(route, linkWindowBetween(from, to)))
		ans := map[string]any{"text": openPageAnswerTR(route), "suggestions": guidedSuggestions(route), "links": links}
		if len(links) > 0 {
			ans["open"] = links[0].Href
		}
		emit("answer", ans)
		return true, true
	}

	var evidence, sources string
	var err error
	switch route.Intent {
	case guidedRootCause:
		evidence, sources, err = s.guidedRootCauseBundle(ctx, emit, route.Service, route.Env, from, to, rangeS, hasLatencyAsk(normalizeGuidedMsg(question)))
	case guidedProblems:
		evidence, sources, err = s.guidedProblemsBundle(ctx, emit, route.Service, route.Env, to)
	case guidedServiceHealth:
		// v0.9.184 — operasyon-scope yükseltmesi: soru belirli bir
		// operasyonu adlandırıyorsa (ya da operatör bir operasyon
		// sayfasındaysa) RED'i o span-name'e daraltıp operasyon
		// bundle'ına yönlendir; aksi halde servis-geneli kalır.
		// v0.9.410 — takip sorusu operasyonu adlandırmaz ("peki p99?");
		// mevcut soru çözmezse devralınan temel mesajdan dene.
		// v0.10.1276 — tarama (Deep) servisin TÜM operasyonlarını okur:
		// operasyon kapsamına daraltılmaz ("operationlarını" bir operasyon adı değil).
		op := ""
		if !route.Deep {
			op = s.resolveGuidedOperation(ctx, route.Service, question, ctxOperation)
			if op == "" && followBase != "" {
				op = s.resolveGuidedOperation(ctx, route.Service, followBase, "")
			}
		}
		if op != "" {
			evidence, sources, err = s.guidedOperationHealthBundle(ctx, emit, route.Service, op, route.Env, from, to, rangeS)
		} else {
			evidence, sources, err = s.guidedServiceHealthBundle(ctx, emit, route.Service, route.Env, from, to, rangeS, route.Deep, loc)
		}
	case guidedFamilyHealth:
		evidence, sources, err = s.guidedFamilyHealthBundle(ctx, emit, route.Family, route.Env, from, to, rangeS)
	case guidedSlowTraces:
		evidence, sources, err = s.guidedSlowTracesBundle(ctx, emit, route.Service, route.Env, from, to, rangeS)
	case guidedTraceDrill: // v0.10.1327 — en yavaş trace kritik yolu + o trace'lerin logları
		evidence, sources, err = s.guidedTraceDrillBundle(ctx, emit, route.Service, route.Env, from, to, rangeS)
	case guidedServiceReport: // v0.10.1337 — operasyonlar + teknik hatalar + trace/log kanıtı (copilot_service_report.go)
		evidence, sources, err = s.guidedServiceReportBundle(ctx, emit, route, from, to, rangeS, loc) // v0.10.1415 — loc: HTTP grafik özeti saatleri
	case guidedDeployImpact:
		evidence, sources, err = s.guidedDeployBundle(ctx, emit, route.Service, route.Env, rangeS, anchorTo)
	case guidedLogErrors:
		evidence, sources, err = s.guidedLogErrorsBundle(ctx, emit, route.Service, route.Env, from, to, rangeS)
	case guidedLogField: // v0.10.433 (D5)
		evidence, sources, err = s.guidedLogFieldBundle(ctx, emit, &route, from, to, rangeS)
	case guidedPairRequests: // v0.10.436 (D2a)
		evidence, sources, err = s.guidedPairBundle(ctx, emit, &route, from, to, rangeS)
	case guidedTraceSearch: // v0.10.436 (D2b)
		evidence, sources, err = s.guidedTraceSearchBundle(ctx, emit, &route, from, to, rangeS)
	case guidedFamilyTraces: // v0.10.465 (D2)
		evidence, sources, err = s.guidedFamilyTracesBundle(ctx, emit, &route, from, to, rangeS)
	case guidedWindowCompare: // v0.10.437 (D6)
		evidence, sources, err = s.guidedWindowCompareBundle(ctx, emit, &route)
	case guidedCallPeriod: // v0.10.438 (D3)
		evidence, sources, err = s.guidedCallPeriodBundle(ctx, emit, &route, to, loc) // v0.10.758
	case guidedFanout: // v0.10.439 (D4)
		evidence, sources, err = s.guidedFanoutBundle(ctx, emit, &route, from, to, rangeS)
	case guidedMyServices:
		evidence, sources, err = s.guidedMyTeamBundle(ctx, emit, "health", &route, from, to, rangeS)
	case guidedMyProblems:
		evidence, sources, err = s.guidedMyTeamBundle(ctx, emit, "problems", &route, from, to, rangeS)
	case guidedMyExceptions:
		evidence, sources, err = s.guidedMyTeamBundle(ctx, emit, "exceptions", &route, from, to, rangeS)
	case guidedTeamExceptions: // v0.10.1385
		evidence, sources, err = s.guidedTeamExceptionsBundle(ctx, emit, &route, from, to)
	case guidedServiceExceptions:
		evidence, sources, err = s.guidedServiceExceptionsBundle(ctx, emit, &route, from, to)
	case guidedTopErrorServices:
		var rows []chstore.ErrorRank
		evidence, sources, rows, err = s.guidedTopErrorServicesFleet(ctx, emit, from, to)
		route.TeamServices = errorRankNames(rows)
	case guidedMyTopErrors:
		evidence, sources, err = s.guidedMyTeamBundle(ctx, emit, "top_errors", &route, from, to, rangeS)
	case guidedTeamTopErrors: // v0.10.1402
		evidence, sources, err = s.guidedTeamTopErrorsBundle(ctx, emit, &route, from, to, rangeS)
	case guidedTopErrorOps:
		evidence, sources, err = s.guidedTopErrorOpsBundle(ctx, emit, route.Service, from, to)
	case guidedTeamServices:
		evidence, sources, err = s.guidedTeamServicesBundle(ctx, emit, &route, from, to, rangeS)
	case guidedPodHealth:
		evidence, sources, err = s.guidedPodHealthBundle(ctx, emit, route.Service, from, to)
	case guidedMetricChart: // v0.10.1386 — tek VM range sorgusu + sohbet içi grafik
		evidence, sources, err = s.guidedMetricChartBundle(ctx, emit, &route, from, to, rangeS, loc)
	case guidedResourceInvestigation: // v0.10.1431 — Thanos cAdvisor/kube-state paketi + grafikler
		evidence, sources, err = s.guidedResourceBundle(ctx, emit, &route, to, rangeS, loc)
	case guidedShiftSummary:
		evidence, sources, err = s.guidedShiftSummaryBundle(ctx, emit, route.Service, from, to, rangeS, loc) // v0.10.758
	case guidedDBHealth:
		evidence, sources, err = s.guidedDBHealthBundle(ctx, emit, route.Service, from, to, rangeS)
	case guidedMessagingHealth:
		evidence, sources, err = s.guidedMessagingBundle(ctx, emit, route.Service, from, to, rangeS)
	case guidedTraceByID:
		evidence, sources, err = s.guidedTraceBundle(ctx, emit, route.TraceID)
	case guidedSpanByID:
		evidence, sources, err = s.guidedSpanBundle(ctx, emit, route.SpanID, from, to)
	case guidedRequestID:
		// Pencere ROTADAN gelmiyor: kimliğin İÇİNDEKİ damgadan türüyor,
		// yani sohbetin from/to'su bilinçli olarak geçilmiyor. Bundle
		// çözdüğü trace'i ve pencereyi rotaya YAZAR (derin linkler için).
		evidence, sources, err = s.guidedRequestIDBundle(ctx, emit, &route)
	case guidedAskService:
		// v0.10.429 (D1) — "hangisini kastettin?": adaylar rotada (router/
		// sınıflandırıcı) ya da buradan (açık problemi olan servisler).
		evidence, sources, err = s.guidedAskServiceEvidence(ctx, &route, question)
	}
	if err != nil {
		// Prefetch failed hard → let the free loop try; its tools may
		// route differently. The steps already emitted just render as
		// extra progress chips.
		return false, false
	}
	if route.DirectAnswer != "" { // netleştirme: girdi çıktıyı tam belirliyor
		if route.Intent == guidedAskService && disambigAskRescuable(route.AskIntent) {
			markDisambiguation(ctx) // v0.10.1126 — wiki kurtarması (chat_disambig_rescue.go)
		}
		emit("answer", withGuidedFollowups(map[string]any{"text": route.DirectAnswer, "suggestions": guidedSuggestions(route)},
			guidedFollowups(route, chatUserTexts(msgs)))) // v0.10.1383 — netleştirme turunda nil (aday çipleri kalır)
		return true, true
	}

	// The ONE self-recording model call, via the surface-explicit
	// wrapper so the ai_calls row lands as "chat-guided" — quality
	// tracking for the guided path, separate from the free-loop
	// "chat" rows.
	// v0.8.404 — token streaming: the narration call streams its
	// answer tokens as `delta` events on the chat SSE. The `answer`
	// event below stays the UNCHANGED source of truth (full text +
	// exchangeId feedback anchor) — old frontends that ignore `delta`
	// render exactly as before, and when the endpoint can't stream
	// (vLLM builds that 400 on stream:true) StreamText falls back to
	// the buffered call transparently: zero deltas, same answer.
	// v0.9.479 — çekmece bağlamı (varsa) narration bloğuna ek bölüm
	// olarak girer; explain boşken üretilen metin bayt-bayt eskisidir
	// (guidedNarrationUser, copilot_drawer.go — testte pinli).
	// v0.9.1231 — konuşma geçmişi de bloğa girer. msgs, copilot_chat.go'da
	// assemble.ClampHistory'den GEÇMİŞ dizinin ta kendisidir (rotalamadan
	// önce klamplanır); guided ikinci bir kaynak kurmaz, yalnız kendi dar
	// bütçesini (guidedHistoryMaxRunes) uygular — kanıt geçmişten önce
	// gelir. Geçmiş yoksa metin yine bayt-bayt eskisidir.
	// v0.10.1289 — operatör kuralları (cosre_rules_prompt.go): çitli blok VERİ
	// bölümünün sonuna, ek sistem mesajının önüne (DataNotInstruction sonda
	// kalır). Kural yoksa iki metin de bayt bayt eski.
	rulesSvc := route.Service
	if rulesSvc == "" {
		rulesSvc = ctxService
	}
	rulesBlock, rulesN := s.cosreRulesBlock(ctx, rulesSvc)
	if label := cosreRulesStepLabel(rulesN); label != "" {
		emitGuidedContextStep(emit, label)
	}
	user := guidedNarrationUser(question, evidence+cosreRulesDataSuffix(rulesBlock), explain, msgs)
	// v0.9.528 Faz 2 — kiminle konuşulduğu prompt'un başına girer
	// (ctx'ten; ön-söz boşsa metin bayt-bayt eskisi).
	raw, exErr := s.copilotStreamSurface(ctx, "chat-guided", withAddressee(addresseeFromCtx(ctx), cosreRulesSystemPrefix(rulesBlock)+guidedNarrationPrompt(route.Intent)), user, func(delta string) {
		emit("delta", map[string]string{"text": delta})
	})
	if exErr != nil {
		emit("error", map[string]string{"error": exErr.Error()})
		return true, false
	}
	// Deterministic provenance footer — appended server-side, never
	// trusted to the model.
	// exchangeId (v0.8.399): the id the chat handler minted rides in
	// on CallMeta — the Explain call above already recorded it on the
	// "chat-guided" ai_calls row, so the UI's thumbs up/down joins
	// back to it exactly like the free tool loop's answers.
	// v0.10.70 — MODELİN AKTARDIĞI ÇİT, SUNUCUNUN YAZDIĞI ÇİT OLMALI.
	//
	// Guided'da grafik ekrana ancak model çiti AKTARIRSA ulaşıyor
	// (v0.10.68 bunu neden sökemeyeceğimizle birlikte yazdı). Kalan risk
	// oradaydı: model spec'i DEĞİŞTİREBİLİR ya da kendi uydurduğunu araya
	// sokabilir — sonuç "doğru veriyle çizilmiş yanlış kapsam".
	//
	// İzin listesi KANITTAN türüyor ve kanıt sunucu-yazımı; yani model
	// listeye üye EKLEYEMEZ. İmzaya gerek yok: sunucu ne yazdığını zaten
	// biliyor (guided_chart_allowlist.go).
	answer := strings.TrimSpace(raw) + route.AnswerTail + "\n\nKaynak: " + sources // v0.10.1397 — sunucu eki (takım servis listesi)
	answer, _ = filterModelChartFences(answer, serverChartScopes(evidence))
	// v0.9.419 — rotadan türetilen deterministik derin linkler; frontend
	// çip olarak çizer (eski frontend'ler yok sayar).
	// v0.9.1321 (§3.1 K6) — çipler cevabın ÜZERİNDE hesaplandığı pencereyi
	// taşır. `from`/`to` yukarıda bundle'lara verilen aralığın ta kendisi,
	// yani link operatöre "bu cevap şu aralıktan çıktı" der; şimdiye
	// çapalanan bir tahmin değil.
	links := guidedAnswerLinks(route, linkWindowBetween(from, to))
	// v0.9.1142 — yapılandırılmış request-ID rotasında köprü çipi
	// ROTADAN da kurulur: kimliği sunucu biliyor, modelin onu cevapta
	// tekrar etmesini beklemek gereksiz kırılganlık. Metinden avlanan
	// kopyayla çakışırsa href'e göre tekilleşir.
	links = append(links, s.knownRequestIDLinks(ctx, route.RequestID, ctxService)...)
	// v0.9.709 — rota çiplerine cevap metnindeki request_id log
	// köprüsü çipleri eklenir (operatör-bildirimi: CoSRE id'yi
	// buluyor ama linklemiyordu).
	links = append(links, s.answerRequestIDLinks(ctx, answer, ctxService)...)
	// v0.10.1143 — filo geneli guidedProblems sıfır eşleşme: içerik yoklamasının
	// kapıyı geçen wiki sayfası yalnız bağlantı olarak (chat_wiki_probe.go).
	answer, links = s.wikiZeroMatchHint(ctx, question, answer, links)
	// v0.9.411 — konuya-duyarlı takip önerileri (guidedSuggestions,
	// copilot_followup.go). Eski frontend'ler alanı yok sayar.
	// v0.10.1383 — yapısal takip adımları (guidedFollowups, copilot_followup.go):
	// servis cevabında "Operasyonları göster / Exception'ları göster …", takım
	// cevabında servis başına özet çipleri. suggestions aynen (eski istemci).
	emit("answer", withGuidedFollowups(map[string]any{
		"text": answer, "exchangeId": copilot.MetaFromContext(ctx).ExchangeID,
		"suggestions": guidedSuggestions(route),
		"links":       dedupLinksByHref(links),
	}, guidedFollowups(route, chatUserTexts(msgs))))
	return true, true
}

// guidedServiceNames returns the live service-name list for entity
// extraction, Redis-cached for 60s so chat traffic costs at most one
// catalogue read per minute per replica. Soft-fails to nil — the
// router still handles the entity-free intents.
func (s *Server) guidedServiceNames(ctx context.Context) []string {
	const key = "copilot:guided:svcnames"
	if b, ok, _ := s.cache.Get(ctx, key); ok && len(b) > 0 {
		var names []string
		if json.Unmarshal(b, &names) == nil {
			return names
		}
	}
	names, _, err := s.store.ListServiceNames(ctx, "", 2000, 0)
	if err != nil {
		return nil
	}
	if b, merr := json.Marshal(names); merr == nil {
		_ = s.cache.Set(ctx, key, b, 60*time.Second)
	}
	return names
}

// guidedEnvNames returns the live deployment-environment list for
// env-entity extraction (v0.8.398), the env twin of guidedServiceNames:
// Redis-cached 60s so chat traffic costs at most one enumeration per
// minute per replica. ListEnvironments with zero from/to resolves to
// the last hour and is count-ordered (busiest env first — extraction's
// equal-length tie-break follows list order, so the busiest wins).
// Soft-fails to nil — the router then runs env-blind, which is the
// pre-v0.8.398 behaviour.
func (s *Server) guidedEnvNames(ctx context.Context) []string {
	const key = "copilot:guided:envnames"
	if b, ok, _ := s.cache.Get(ctx, key); ok && len(b) > 0 {
		var names []string
		if json.Unmarshal(b, &names) == nil {
			return names
		}
	}
	names, _, err := s.store.ListEnvironments(ctx, time.Time{}, time.Time{}, "", 200)
	if err != nil {
		return nil
	}
	if b, merr := json.Marshal(names); merr == nil {
		_ = s.cache.Set(ctx, key, b, 60*time.Second)
	}
	return names
}

// guidedTeamNames (v0.9.1134) — canlı takım kataloğu, guidedServiceNames'in
// takım ikizi: Redis'te 60sn, yani sohbet trafiği replika başına dakikada
// en fazla bir türetme ödüyor.
//
// Maliyet notu: kaynak okuma ListServiceMetadata ve o ZATEN süreç-içi 30sn
// cache'li + yazma-tarafı invalidasyonlu (v0.8.359). Redis katmanı asıl
// olarak takım sayımı + sıralamayı ve alias point-read'ini her mesajda
// yeniden yapmamak için var. Hata → nil: router takım-kör koşar, yani
// v0.9.1134 öncesi davranış.
//
// v0.9.1244 — okuma mcptools.ReadTeamCatalogue: list_teams tool'u ile
// AYNI katalog, aynı sıra. İki yüzeyin farklı takım listesi göstermesi
// (biri alias'ları birleştirir, öbürü birleştirmez) sessiz bir sapma
// olurdu.
func (s *Server) guidedTeamNames(ctx context.Context) []string {
	return mcptools.TeamCatalogueNames(s.guidedTeamCatalogue(ctx))
}

// guidedTeamCatalogue — v0.10.559: takım kataloğu TÜR sayaçlarıyla (owner/sre),
// 60 s cache (anahtar v2: eski değer yalnız ad dizisiydi).
func (s *Server) guidedTeamCatalogue(ctx context.Context) []mcptools.TeamCatalogueEntry {
	const key = "copilot:guided:teamcat:v2"
	if b, ok, _ := s.cache.Get(ctx, key); ok && len(b) > 0 {
		var rows []mcptools.TeamCatalogueEntry
		if json.Unmarshal(b, &rows) == nil {
			return rows
		}
	}
	data, err := mcptools.ReadTeamCatalogue(ctx, s.mcpDeps())
	if err != nil {
		return nil
	}
	if b, merr := json.Marshal(data.Teams); merr == nil {
		_ = s.cache.Set(ctx, key, b, 60*time.Second)
	}
	return data.Teams
}

// teamAskOptions — v0.10.559 (operatör-raporlu: "yalnız SY takımları çıkıyor, UG
// çıkmıyor"). SAF. Katalog servis sayısına göre sıralı; SRE takımları çok
// servise sahip olduğundan ilk N'i tek başına dolduruyordu. Seçenekler iki
// gruptan DÖNÜŞÜMLÜ seçilir: uygulama takımları (ownerTeam olduğu servis var)
// önce, sonra SRE takımları (yalnız sreTeam) — her tür temsil edilir, aynı takım
// bir kez, toplam tavan korunur. rest = listeye girmeyen takım sayısı.
func teamAskOptions(entries []mcptools.TeamCatalogueEntry, max int) (opts, owner, sre []string, rest int) {
	sorted := append([]mcptools.TeamCatalogueEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Services > sorted[j].Services }) // çağıran sırasına güvenme
	for _, e := range sorted {
		switch {
		case e.Owner > 0:
			owner = append(owner, e.Team)
		case e.SRE > 0:
			sre = append(sre, e.Team)
		}
	}
	for i, j := 0, 0; len(opts) < max && (i < len(owner) || j < len(sre)); {
		if i < len(owner) {
			opts = append(opts, owner[i])
			i++
		}
		if len(opts) < max && j < len(sre) {
			opts = append(opts, sre[j])
			j++
		}
	}
	rest = len(owner) + len(sre) - len(opts)
	if rest < 0 {
		rest = 0
	}
	return opts, owner, sre, rest
}

// renderTeamAskEvidenceTR — v0.10.559: iki grup + dışarıda kalan sayısı.
func renderTeamAskEvidenceTR(entries []mcptools.TeamCatalogueEntry, max int) string {
	opts, owner, sre, rest := teamAskOptions(entries, max)
	in := make(map[string]bool, len(opts))
	for _, o := range opts {
		in[o] = true
	}
	pick := func(src []string) []string {
		out := make([]string, 0, len(src))
		for _, t := range src {
			if in[t] {
				out = append(out, t)
			}
		}
		return out
	}
	var b strings.Builder
	if o := pick(owner); len(o) > 0 {
		fmt.Fprintf(&b, "Uygulama takımları (ownerTeam, %d/%d): %s\n", len(o), len(owner), strings.Join(o, ", "))
	}
	if s := pick(sre); len(s) > 0 {
		fmt.Fprintf(&b, "SRE takımları (sreTeam, %d/%d): %s\n", len(s), len(sre), strings.Join(s, ", "))
	}
	if rest > 0 {
		fmt.Fprintf(&b, "Katalogda %d takım daha var — listede yoksa kullanıcı adını yazabilir.\n", rest)
	}
	return b.String()
}

// emitGuidedStep — guided kanıt paketinin ⚙ çipi. v0.9.1229'dan beri
// çipin KİMLİĞİNİ döndürür; çağıran o kimlikle eşli kanıtı
// (emitGuidedStepResult) yayınlar. Kimlik üretimi chat_step_ids.go'da,
// tek sayaçta.
func emitGuidedStep(emit func(string, any), tool, args string) int {
	return emitStepChipOrigin(emit, tool, args, "guided") // v0.10.161 — köken rozeti
}

// emitGuidedStepResult — adımın KANITI (v0.9.1229).
//
// `segment` o adımın OKUDUĞU verinin metnidir: ya prompt'a giren
// bloğun ta kendisi, ya da okumanın kendi çıktısı (ör. takım→servis
// çözümlemesinin ad listesi). Model tarafından yazılmış bir özet ASLA
// değil — özetlenmiş kanıt kanıt değildir; operatörün sınayacağı şey
// modelin okuduğu şey olmalı.
//
// Birkaç okuma TEK bloğa akıyorsa (ör. pod envanteri + heap tek ortak
// katman çağrısında) blok her iki çipe de verilir: yarısını uydurmak
// ya da çipi ölü bırakmak, aynı bloğu iki kez göstermekten kötü.
func emitGuidedStepResult(emit func(string, any), i int, tool, segment string, err error) {
	emitStepEvidence(emit, i, tool, segment, err)
}

// emitGuidedContextStep — kanıt okuması OLMAYAN bağlam çipi ("bağlam:
// ekrandaki trace (…)"). Kimlik alır (çip şeridi ile detay dizisi
// hizada kalsın) ama eşli kanıt YAYINLAMAZ: okunan bir şey yok, yani
// çip düz etiket olarak kalır — doğrusu da bu.
func emitGuidedContextStep(emit func(string, any), label string) {
	_ = emitStepChipOrigin(emit, label, "", "guided")
}

// guidedStepSegment — b'ye adım açıldığından beri YAZILAN metin.
// strings.Builder.String() kopya üretmez, yani bu dilim ucuzdur.
// Kanıt bloğunu satır satır b'ye yazan bundle'lar (vardiya özeti,
// servis/operasyon sağlığı) adımın payını böyle ölçüyor.
func guidedStepSegment(b *strings.Builder, at int) string {
	s := b.String()
	if at < 0 || at > len(s) {
		return ""
	}
	return s[at:]
}

// withEnvArg appends the applied env to a step-event args echo so the
// operator's progress chip shows env=uat when the bundle read was
// env-narrowed (v0.8.398). Pure — table-tested. Bundles that CANNOT
// apply the env (logs/deploys, Phase 4 pending) never call this —
// the step echo only ever shows filters that were actually applied.
func withEnvArg(argsJSON, env string) string {
	if env == "" {
		return argsJSON
	}
	if argsJSON == "" || argsJSON == "{}" {
		return `{"env":"` + env + `"}`
	}
	return strings.TrimSuffix(argsJSON, "}") + `,"env":"` + env + `"}`
}

// guidedEnvlessNoteTR flags an env ask on a bundle whose data path has
// no env dimension yet (logs + deploy markers — env-separation Phase 4
// pending): the evidence SAYS the filter was not applied instead of
// silently ignoring it. The narration prompt forbids inventing, so
// this line is what keeps the 2B model from claiming "uat'ta ...".
// Pure — table-tested (v0.8.398).
func guidedEnvlessNoteTR(what, env string) string {
	if env == "" {
		return ""
	}
	return fmt.Sprintf("Not: %s ortam boyutu taşımıyor (env-ayrımı Faz 4 bekliyor) — %q ortam filtresi UYGULANMADI; sayılar tüm ortamların toplamı.\n", what, env)
}

// envSetNoteTR — SAF (v0.10.1373): çok ortamlı sorunun özet çipi.
func envSetNoteTR(envs []string) string {
	return "ortamlar: " + strings.Join(envs, ", ") + " (tek ortama daraltılmadı — tüm ortamlar)"
}

// guidedProblemFilter builds the problems prefetch filter. Extracted
// pure so the env threading (ProblemFilter.Env — service-scoped
// semantics, env_members.go) is pinned by a table test (v0.8.398).
func guidedProblemFilter(service, env string, limit int) chstore.ProblemFilter {
	return chstore.ProblemFilter{Status: "open", Service: service, Env: env, Limit: limit}
}

// guidedTraceFilter builds the slow-traces prefetch filter. Extracted
// pure so the env threading (TraceFilter.Env — direct deploy_env
// conjunct, raw-fallback path) is pinned by a table test (v0.8.398).
func guidedTraceFilter(service, env string, from, to time.Time) chstore.TraceFilter {
	return chstore.TraceFilter{
		Service: service, Env: env, From: from, To: to,
		Sort: "duration", Order: "desc", Limit: 10, CountMode: "skip",
	}
}

// ─── Prefetch bundles (bounded, existing reads only) ────────────────

// (a) "errors/problems now" → open problems + triage priority + the
// persisted deterministic root-cause hypotheses (v0.8.394 enrichment).
// env (v0.8.398) rides ProblemFilter.Env — the service-scoped
// semantics from env_members.go; the evidence spells that out so the
// narration never oversells the filter.
// guidedRootCauseBundle (v0.9.514) — "neden X patladı" sorusunun kanıtı.
//
// Hipotez ZATEN kayıtlı: RootCauseSynthesizer her anchor için
// deterministik sıralı adayları üretip saklıyor, v0.9.510-512 derin
// soruşturması onu pod/metrik/log/iş-boyutu kanıtıyla besliyor. Bu
// bundle o birikimi okuyup önüne koyuyor — model nedenselliği kendi
// UYDURMUYOR, hesaplanmış hipotezi anlatıyor.
//
// Sıra kasıtlı: önce hipotez (asıl cevap), sonra RED (ne değişti), sonra
// deploy (en sık sebep). Açık problem yoksa hipotez de yoktur — o durum
// SESSİZCE geçilmez, açıkça söylenir ve model değişim verisiyle cevap
// verir.
// slowAsk (v0.10.1327) — soru yavaşlık/timeout soruyor (hasLatencyAsk); p99
// yükselmesiyle birlikte en yavaş trace kırılımının kapısı.
func (s *Server) guidedRootCauseBundle(ctx context.Context, emit func(string, any), service, env string, from, to time.Time, rangeS int64, slowAsk bool) (string, string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "SORU ŞEKLİ: nedensellik — %q servisinde NE OLDU değil NEDEN OLDU sorusu.\n\n", service)

	// v0.10.808 (dış skill denetimi V4 — Honeycomb: oryantasyon SLO'yla
	// başlar): hedef/bütçe/tükenme, problemlerden ÖNCE.
	s.guidedSLOStep(ctx, emit, &b, service)

	nProb := emitGuidedStep(emit, "list_problems", withEnvArg(`{"service":"`+service+`","status":"open"}`, env))
	// v0.10.944 — probErr ayrı ad: aşağıdaki desen okuması `perr`'i yeniden
	// atıyor; kanıt bloğu problem okumasının KENDİ hatasını görmeli.
	probs, probTotal, probErr := s.guidedProblemsWithTotal(ctx, guidedProblemFilter(service, env, 10))
	if probErr == nil && len(probs) > 0 {
		probs = s.enrichProblemsForRead(ctx, probs) // v0.9.553 — deploy+öncelik, sırası sabit
		// v0.9.1229 — okumanın kanıtı hipotez EKLENMEDEN önceki bloktur;
		// sıradaki çip aynı listeyi hipotezle gösteriyor, yani
		// zenginleştirmenin NE KATTIĞI operatörde görünür oluyor.
		emitGuidedStepResult(emit, nProb, "list_problems",
			renderProblemsEvidenceTR(probs, service, env, evidenceAsOf(to), probTotal), nil)
		nRC := emitGuidedStep(emit, "root_cause_hypotheses", "")
		probs = s.store.EnrichProblemsWithRootCause(ctx, probs)
		blk := renderProblemsEvidenceTR(probs, service, env, evidenceAsOf(to), probTotal)
		emitGuidedStepResult(emit, nRC, "root_cause_hypotheses", blk, nil)
		b.WriteString(blk)
		// v0.10.1287 (AI SRE Faz 1b) — problemin kayıtlı AI SRE araştırması:
		// aynı okumaları yeniden koşmadan bulgular + özet ("KAYITLI ARAŞTIRMA").
		if saved := s.guidedSavedInvestigationTR(ctx, probs); saved != "" {
			b.WriteString("\n" + saved)
		}
	} else if probErr != nil {
		// v0.10.944 — okuma hatası "problem yok" değil (service_context ile aynı sınıf).
		emitGuidedStepResult(emit, nProb, "list_problems", "", probErr)
		b.WriteString(guidedRootCauseNoProblemsTR(probErr))
	} else {
		none := guidedRootCauseNoProblemsTR(nil)
		emitGuidedStepResult(emit, nProb, "list_problems", none, nil)
		b.WriteString(none)
	}

	nCtx := emitGuidedStep(emit, "service_context", `{"service":"`+service+`"}`)
	cx := s.buildServiceContext(ctx, service, from, to)
	// v0.10.944 — servis bağlamı (service_summary_5m) ortam kırılımı yapmıyor;
	// problemler env'e daraltılıyor. Kardeş sağlık demeti gibi söylenir; not
	// prompt'ta kalır, service_context çip segmentine girmez (atCtx sonra).
	if env != "" {
		fmt.Fprintf(&b, "Not: RED değerleri tüm ortamların toplamı (servis bağlamı ortam kırılımı yapmıyor); açık problemler %q ortamına daraltıldı.\n", env)
	}
	atCtx := b.Len()
	b.WriteString(renderServiceSnapshot(cx))
	// v0.10.944 — okuma hatası "span yok" değil: snapshot OKUNAMADI der, çip
	// hata sınıfını gösterir (yalnız baseline düştüyse adım başarılı kalır).
	if cx.curErr == nil && cx.Current.Spans == 0 {
		b.WriteString("Bu pencerede span verisi yok — değişim de okunamıyor.\n")
	}
	emitGuidedStepResult(emit, nCtx, "service_context", guidedStepSegment(&b, atCtx), cx.curErr)

	// v0.10.992 (dış skill denetimi V1) — BubbleUp: sorun hangi rota / pod /
	// sürümde yoğunlaşıyor. RED'den hemen sonra: "ne değişti"nin ardından
	// "nerede". Ortam süzgeci uygulanmaz (RED notu gibi, tüm ortamlar).
	s.guidedBubbleUpStep(ctx, emit, &b, service, probs, cx, to)

	// Deploy: "neden bozuldu" sorusunun en sık cevabı. Ayrı bir adım
	// olarak emit ediliyor ki operatör hangi kanıtın çekildiğini görsün.
	nDep := emitGuidedStep(emit, "recent_deploys", `{"service":"`+service+`"}`)
	dep, _, derr := s.guidedDeployBundle(ctx, func(string, any) {}, service, env, rangeS, to)
	emitGuidedStepResult(emit, nDep, "recent_deploys", dep, derr)
	if derr == nil && strings.TrimSpace(dep) != "" {
		b.WriteString("\n")
		b.WriteString(dep)
	}

	// v0.10.557 (Faz 4c) — penceredeki DEĞİŞİKLİKLER: deploy olayı + çıkarımsal
	// deploy + K8s rollout (list_deployments aynası; rollouts katmanı kapalıysa
	// metin bunu söyler). recent_deploys servis-kapsamlı sürüm listesi; bu adım
	// rollout'ları ve olayları da katar.
	nChg := emitGuidedStep(emit, "list_deployments", `{"service":"`+service+`"}`)
	chgOut, cerr := mcptools.ListDeploymentsWindow(ctx, s.mcpDeps(), mcptools.ListDeploymentsArgs{Service: service, Limit: 8}, from, to)
	var changes []mcptools.ChangeRow
	chgText := ""
	if cerr == nil {
		changes = mcptools.ChangesOf(chgOut)
		chgText = mcptools.RenderChangesTR(chgOut)
	}
	emitGuidedStepResult(emit, nChg, "list_deployments", chgText, cerr)
	if cerr == nil && chgText != "" {
		b.WriteString("\n")
		b.WriteString(chgText)
	}

	// v0.10.557 (Faz 4c) — log desenleri (yeni / patlayan), servise dokunanlar.
	nPat := emitGuidedStep(emit, "log_patterns", `{"service":"`+service+`"}`)
	pats, perr := s.guidedServicePatterns(ctx, service, rangeS)
	patText := renderLogPatternsTR(pats, service, rangeS)
	emitGuidedStepResult(emit, nPat, "log_patterns", patText, perr)
	if perr == nil {
		b.WriteString("\n")
		b.WriteString(patText)
	}

	// v0.10.1327 — "NEREDE": en yavaş 1–2 trace'in kritik yolu + YALNIZ o
	// trace'lerin logları; yalnız p99 tabana göre yükseldiyse ya da soru
	// yavaşlık/timeout soruyorsa (chat_trace_drill.go).
	drilled := false
	if rootCauseWantsTraceDrill(cx.Current, cx.Baseline, cx.curErr, slowAsk) {
		if _, drill, terr := s.slowTraceDrill(ctx, emit, service, env, from, to); terr != nil {
			fmt.Fprintf(&b, "\nEn yavaş trace'ler OKUNAMADI (%s) — kritik yol bilinmiyor; hedef UYDURMA.\n", sourcestate.Classify(terr))
		} else if drill != "" {
			b.WriteString("\n" + drill)
			drilled = true
		}
	}

	// v0.10.557 — yapısal kanıt bloğu (FE kartı; anlatımdan bağımsız, tek sıralayıcı).
	emit("evidence", guidedEvidencePayload(service, rangeS, cx, probs, probErr, changes, pats))

	b.WriteString("\nKURAL: Yukarıdaki kök-neden hipotezi HESAPLANMIŞ bir sıralamadır, tahmin değil. " +
		"Onu anlat ve güven skorunu birlikte ver. Hipotez yoksa ya da güveni düşükse sebep UYDURMA — " +
		"hangi kanıta baktığını yaz ve 'kesin sebep için yeterli kanıt yok' de.\n")

	src := fmt.Sprintf("SLO durumu + kök-neden hipotezi + açık problemler + servis RED değişimi + ayrışan boyutlar (BubbleUp) + deploy geçmişi + pencere değişiklikleri (rollout) + log desenleri (son %s)", fmtAgoTR(rangeS))
	if drilled {
		src += " + en yavaş trace kritik yolu + o trace'lerin logları"
	}
	if env != "" {
		src += fmt.Sprintf("; RED tüm ortamlar, problemler ortam: %s", env) // v0.10.944 — sağlık demetiyle aynı
	}
	return b.String(), src, nil
}

// guidedRootCauseNoProblemsTR — SAF (v0.10.944): kök-neden demetinde problem
// listesi BOŞ ya da OKUNAMADI iken prompt'a giden metin. Okuma hatası asla
// "AÇIK PROBLEM YOK" demez: hata ≠ boş (service_context düzeltmesiyle aynı
// sınıf). Tablo testli.
func guidedRootCauseNoProblemsTR(perr error) string {
	if perr != nil {
		return fmt.Sprintf("Problem kayıtları OKUNAMADI (%s) — bu 'açık problem yok' DEMEK DEĞİL; kayıtlı kök-neden hipotezi de okunamadı. "+
			"Aşağıdaki değişim verisiyle cevap ver, problem/hipotez varlığı hakkında sonuç çıkarma ve sebep uydurma.\n\n", sourcestate.Classify(perr))
	}
	return "Bu serviste AÇIK PROBLEM YOK — dolayısıyla kayıtlı bir kök-neden hipotezi de yok. " +
		"Aşağıdaki değişim verisiyle cevap ver ve hipotez olmadığını AÇIKÇA söyle; sebep uydurma.\n\n"
}

// asOf (v0.10.65) — kanıt YAŞLARININ dayanağı. Diğer paketler pencere
// sonunu (`to`) zaten taşıyor; bu paket taşımıyordu ve `time.Now()`
// kullanıyordu, yani çıpalı bir pencerede "4 saattir açık" cümlesi
// saatlerce kayıyordu.
func (s *Server) guidedProblemsBundle(ctx context.Context, emit func(string, any), service, env string, asOf time.Time) (string, string, error) {
	nProb := emitGuidedStep(emit, "list_problems", withEnvArg(`{"status":"open"}`, env))
	probs, probTotal, err := s.guidedProblemsWithTotal(ctx, guidedProblemFilter(service, env, 50))
	if err != nil {
		emitGuidedStepResult(emit, nProb, "list_problems", "", err)
		return "", "", err
	}
	if len(probs) == 0 && service == "" {
		markFleetZeroMatch(ctx) // v0.10.1143 — wiki ipucu (chat_wiki_probe.go)
	}
	probs = s.enrichProblemsForRead(ctx, probs) // v0.9.553 — deploy+öncelik, sırası sabit
	// v0.9.1229 — çipin kanıtı: hipotez eklenmeden ÖNCEKİ liste.
	emitGuidedStepResult(emit, nProb, "list_problems",
		renderProblemsEvidenceTR(probs, service, env, evidenceAsOf(asOf), probTotal), nil)
	nRC := emitGuidedStep(emit, "root_cause_hypotheses", "")
	probs = s.store.EnrichProblemsWithRootCause(ctx, probs)
	evidence := renderProblemsEvidenceTR(probs, service, env, evidenceAsOf(asOf), probTotal)
	emitGuidedStepResult(emit, nRC, "root_cause_hypotheses", evidence, nil)
	src := "açık problemler + triage önceliği + kök-neden hipotezleri (canlı)"
	if env != "" {
		evidence += fmt.Sprintf("Not: problem kayıtları ortam boyutu taşımaz — %q filtresi servis üyeliğiyle uygulandı (bu ortamda koşan servislerin problemleri + global kurallar).\n", env)
		src += fmt.Sprintf(", ortam: %s (servis kapsamlı)", env)
	}
	return evidence, src, nil
}

// guidedEnvREDLineTR — SAF (v0.10.1274): servis-sağlık demetinin ortam RED
// satırı. Okuma hatası "veri yok" demez (hata ≠ boş); bulunamama ortamda span
// olmadığını söyler. Alt satır kıyas bloğunun tüm ortamlar olduğunu belirtir.
func guidedEnvREDLineTR(env string, rangeS int64, w chstore.ServiceSummary, found bool, err error) string {
	tail := "Aşağıdaki kıyas/deploy/komşu bloğu tüm ortamların toplamı (servis bağlamı ortam kırılımı yapmıyor); açık problemler bu ortama daraltıldı.\n"
	switch {
	case err != nil:
		return fmt.Sprintf("Ortam %q RED'i OKUNAMADI (%s) — bu 'trafik yok' DEMEK DEĞİL.\n", env, sourcestate.Classify(err)) + tail
	case !found:
		return fmt.Sprintf("Ortam %q: bu pencerede (son %s) bu servisten span YOK.\n", env, fmtAgoTR(rangeS)) + tail
	}
	return fmt.Sprintf("Ortam %q RED (son %s): %d span, hata oranı %%%.2f, p99 %.0f ms.\n",
		env, fmtAgoTR(rangeS), w.SpanCount, w.ErrorRate, w.P99Ms) + tail
}

// (b) "service X sağlığı/health/slow" → the analyze-service context
// bundle (buildServiceContext + renderServiceSnapshot, reused
// verbatim) + the service's open problems with root-cause.
// buildServiceContext is env-BLIND (its MV reads have no env
// dimension) — an env ask (v0.8.398) narrows only the problems
// sub-read and prepends an honest one-liner so the model attributes
// the RED numbers correctly instead of claiming they're env-scoped.
//
// v0.10.1276 (CoSRE adım 7) — servis TARAMASI (mcptools.ReadServiceSweep):
// demetin girişinde arka planda başlar (paralel; goroutine emit ETMEZ — SSE
// tek goroutine'den), hükümden önce birleşir. Düz "sağlıklı mı / genel durum"
// standart kip (ilk 3 bulgu), Deep (hasSweepSignal) derin kip (ilk 5 + code.*
// örneklemi). Adımı `service_health_sweep` çipi: step-result tarama JSON'u
// (bileşik kanıt listesi → bölüm başına çip), blokları ev_block ile; kanıt
// metnine "TARAMA:" bloğu; hüküm ikisinin KÖTÜSÜ (mergeSweepVerdict).
func (s *Server) guidedServiceHealthBundle(ctx context.Context, emit func(string, any), service, env string, from, to time.Time, rangeS int64, deep bool, loc *time.Location) (string, string, error) {
	// v0.10.1391 — servisin gelen HTTP RED'i VictoriaMetrics'ten (örneklenmemiş),
	// tarama ile PARALEL; varsa span grafiklerinin YERİNE geçer
	// (copilot_service_http_red.go). Goroutine emit etmez.
	waitHTTPRED := s.startServiceHTTPRED(ctx, service, env, from, to, rangeS)
	type sweepOut struct {
		res mcptools.SweepResult
		err error
	}
	sweepCh := make(chan sweepOut, 1)
	sweepCtx, cancelSweep := context.WithCancel(ctx)
	defer cancelSweep()
	go func() {
		res, err := mcptools.ReadServiceSweep(sweepCtx, s.mcpDeps(), mcptools.SweepRequest{
			Service: service, Env: env, From: from, To: to, Window: fmt.Sprintf("%ds", rangeS), Deep: deep, SkipPatterns: true})
		sweepCh <- sweepOut{res, err}
	}()
	var b strings.Builder
	s.guidedSLOStep(ctx, emit, &b, service) // v0.10.808 (V4) — oryantasyon SLO'yla başlar
	nCtx := emitGuidedStep(emit, "service_context", `{"service":"`+service+`"}`)
	cx := s.buildServiceContext(ctx, service, from, to)
	if env != "" {
		// v0.10.1274 — ortam RED'i AYRI bir okumayla (get_service_health'in yolu):
		// servis bağlamı (kıyas + deploy + komşular) ortam kırılımı yapmıyor,
		// ama başlık sayıları artık ekrandaki ortamdan.
		ew, found, eerr := mcptools.ReadServiceEnvRED(ctx, s.mcpDeps(), service, env, from, to)
		b.WriteString(guidedEnvREDLineTR(env, rangeS, ew, found, eerr))
	}
	atSnap := b.Len()
	b.WriteString(renderServiceSnapshot(cx))
	if cx.curErr == nil && cx.Current.Spans == 0 { // v0.10.944 — hata "veri yok" değil
		b.WriteString("Bu pencerede span verisi yok.\n")
	}
	emitGuidedStepResult(emit, nCtx, "service_context", guidedStepSegment(&b, atSnap), cx.curErr)
	nProb := emitGuidedStep(emit, "list_problems", withEnvArg(`{"service":"`+service+`"}`, env))
	probs, probTotal, perr := s.guidedProblemsWithTotal(ctx, guidedProblemFilter(service, env, 10))
	if perr == nil {
		probs = s.enrichProblemsForRead(ctx, probs) // v0.9.553 — deploy+öncelik, sırası sabit
		probs = s.store.EnrichProblemsWithRootCause(ctx, probs)
		atProb := b.Len()
		if len(probs) == 0 {
			b.WriteString("Açık problem yok.\n")
		} else {
			b.WriteString(renderProblemsEvidenceTR(probs, service, env, evidenceAsOf(to), probTotal))
		}
		emitGuidedStepResult(emit, nProb, "list_problems", guidedStepSegment(&b, atProb), nil)
	} else {
		emitGuidedStepResult(emit, nProb, "list_problems", "", perr)
	}
	// v0.9.183 — CoSRE grafik: yanıta yapılandırılmış bir ```chart``` bloğu
	// ekle; frontend (CosreChart) bunu mevcut uPlot motoruyla GERÇEK
	// telemetriden çizer (LLM değil, spanMetricBatch). Servis-sağlık
	// headline'ı = error_rate. Blok görsel olarak dokümana gömülü değildir;
	// eski istemci parse edemezse düz metin olarak görünür (zararsız).
	// Servis sağlık özeti (2026-10-15) — hatalı trace'ler + error logları
	// + deterministik hüküm; hüküm kanıtın BAŞINA yazılır.
	nTr := emitGuidedStep(emit, "trace_search", withEnvArg(`{"service":"`+service+`","hasError":true,"limit":5}`, env))
	tf := traceSearchFilter(service, env, "", false, from, to)
	tf.HasError, tf.Limit, tf.Sort = true, 5, "time"
	errTraces := 0
	if rows, _, _, terr := s.store.GetTraces(ctx, tf); terr == nil {
		errTraces = len(rows)
		at := b.Len()
		if len(rows) == 0 {
			b.WriteString("Hatalı trace yok.\n")
		} else {
			b.WriteString("Son hatalı trace'ler:\n")
			for _, r := range rows {
				fmt.Fprintf(&b, "- %s · %s · %.0f ms\n", r.TraceID, r.RootName, r.DurationMs)
			}
		}
		emitGuidedStepResult(emit, nTr, "trace_search", guidedStepSegment(&b, at), nil)
	} else {
		emitGuidedStepResult(emit, nTr, "trace_search", "", terr)
	}
	if logEv, _, lerr := s.guidedLogErrorsBundle(ctx, emit, service, env, from, to, rangeS); lerr == nil && strings.TrimSpace(logEv) != "" {
		b.WriteString(logEv)
	}
	openN := 0
	if perr == nil {
		openN = len(probs)
	}
	vl, vr, vn := serviceHealthVerdict(cx.Current, cx.Baseline, cx.curErr, openN, errTraces)
	// v0.10.1276 — tarama birleşimi (en geç sweepBudget; ctx biterse boş geçer).
	argsSw, _ := json.Marshal(map[string]any{"service": service, "env": env, "deep": deep})
	nSw := emitGuidedStep(emit, mcptools.ServiceHealthSweepName, string(argsSw))
	var sw sweepOut
	select {
	case sw = <-sweepCh:
	case <-ctx.Done():
		sw.err = ctx.Err()
	}
	httpRED := waitHTTPRED()
	sweepOK := sw.err == nil && sw.res.OK
	if sweepOK {
		// v0.10.1453 (operatör: "servis sağlığı grafikleri hâlâ span metrics, bunu
		// istemiyorum, yalnız VictoriaMetrics; Service Overview zaten öyle") —
		// taramanın span operasyon grafiği (spanmetrics_1m, örneklenmiş) HER
		// ZAMAN düşer; 1391'de yalnız VM kipinde düşüyordu.
		sw.res.DropOperationCharts()
		if body, merr := json.Marshal(sw.res); merr == nil {
			emitGuidedCompositeResult(emit, nSw, mcptools.ServiceHealthSweepName, string(body))
		}
		b.WriteString(renderSweepEvidenceTR(sw.res))
	} else {
		serr := sw.err
		if serr == nil {
			serr = fmt.Errorf("tarama tamamlanamadı")
			if sw.res.Error != nil {
				serr = fmt.Errorf("%s", sw.res.Error.Message)
			}
		}
		emitGuidedStepResult(emit, nSw, mcptools.ServiceHealthSweepName, "", serr)
	}
	vl, vr, vn = mergeSweepVerdict(vl, vr, vn, sw.res, sweepOK)
	verdict := renderServiceHealthVerdictTR(vl, vr, vn)
	nV := emitGuidedStep(emit, "health_verdict", `{"service":"`+service+`"}`)
	emitGuidedStepResult(emit, nV, "health_verdict", verdict, nil)
	body := b.String()
	b.Reset()
	b.WriteString(verdict)
	b.WriteString(body)
	routeDrawn := false
	if httpRED.UseVM {
		// v0.10.1391 — servisin gelen HTTP RED'i: istek hızı · 5xx % · p95/p99,
		// VictoriaMetrics (örneklenmemiş) veri grafiği blokları; span çitleri YOK.
		b.WriteString("\n")
		b.WriteString(s.emitServiceHTTPRED(emit, service, rangeS, httpRED, loc))
		// v0.10.1481 — taramanın sorumlu operasyonu: http_route'a eşlenirse aynı
		// VM histogramından rota grafikleri; eşlenmezse tek satır not (span YOK).
		if sweepOK {
			if op, kind, ok := sw.res.ResponsibleOperation(); ok {
				rd, isVM := s.metricChartReaderFor()
				rr := s.readServiceRouteRED(ctx, rd, isVM, service, op, kind, httpRED)
				txt, drawn := s.emitServiceRouteRED(emit, service, rangeS, httpRED, rr, loc)
				b.WriteString(txt)
				routeDrawn = drawn
			}
		}
	} else {
		// v0.10.1453 — span grafiği YEDEĞİ YOK: VM çizemiyorsa grafik yerine
		// sebebi söyleyen not (servis sağlığında grafik kaynağı yalnız VM).
		b.WriteString(svcHTTPREDNoChartNoteTR(httpRED.Reason))
	}
	src := fmt.Sprintf("SLO durumu + deterministik sağlık hükmü + servis RED özeti + baseline + en sık hatalar + deploy işaretçileri + açık problemler + hatalı trace'ler + error logları + servis taraması (pod/operasyon/fonksiyon + sorunluların logları)%s (son %s)", svcHTTPREDSourcesTR(httpRED)+svcRouteREDSourcesTR(routeDrawn), fmtAgoTR(rangeS))
	if env != "" {
		src += fmt.Sprintf("; RED tüm ortamlar, problemler ortam: %s", env)
	}
	return b.String(), src, nil
}

// guidedOperationHealthBundle (v0.9.184) — the operasyon twin of the
// service-health bundle. Scopes RED to a single span name + emits a
// live operasyon-scoped chart (name = "..." DSL). Problems stay
// service-level (no operation-scoped Problem row exists) and the
// evidence says so.
func (s *Server) guidedOperationHealthBundle(ctx context.Context, emit func(string, any), service, operation, env string, from, to time.Time, rangeS int64) (string, string, error) {
	nCtx := 0
	if argsB, merr := json.Marshal(map[string]string{"service": service, "operation": operation}); merr == nil {
		nCtx = emitGuidedStep(emit, "operation_context", string(argsB))
	}
	cx := s.buildOperationContext(ctx, service, operation, from, to)
	var b strings.Builder
	if env != "" {
		fmt.Fprintf(&b, "Not: RED tüm ortamların toplamı; açık problemler %q ortamına daraltıldı.\n", env)
	}
	atSnap := b.Len()
	b.WriteString(renderOperationSnapshot(cx))
	if cx.Current.Spans == 0 {
		b.WriteString("Bu pencerede bu operasyon için span verisi yok.\n")
	}
	emitGuidedStepResult(emit, nCtx, "operation_context", guidedStepSegment(&b, atSnap), nil)

	nProb := emitGuidedStep(emit, "list_problems", withEnvArg(`{"service":"`+service+`"}`, env))
	probs, probTotal, perr := s.guidedProblemsWithTotal(ctx, guidedProblemFilter(service, env, 10))
	if perr == nil {
		probs = s.enrichProblemsForRead(ctx, probs) // v0.9.553 — deploy+öncelik, sırası sabit
		probs = s.store.EnrichProblemsWithRootCause(ctx, probs)
		atProb := b.Len()
		if len(probs) == 0 {
			b.WriteString("Servis düzeyinde açık problem yok.\n")
		} else {
			b.WriteString("Servis düzeyinde açık problemler (operasyon-özel değil):\n")
			b.WriteString(renderProblemsEvidenceTR(probs, service, env, evidenceAsOf(to), probTotal))
		}
		emitGuidedStepResult(emit, nProb, "list_problems", guidedStepSegment(&b, atProb), nil)
	} else {
		emitGuidedStepResult(emit, nProb, "list_problems", "", perr)
	}

	// v0.9.184 — operasyon-scoped canlı grafik: error_rate headline.
	// Frontend (CosreChart) bunu spanMetricBatch(name = "op") ile çizer.
	b.WriteString(chartFence(guidedChartSpec{Title: operation + " · error_rate", Service: service, Operation: operation, Agg: "error_rate", RangeS: rangeS}))

	src := fmt.Sprintf("operasyon RED özeti + baseline + servis açık problemleri + grafik (son %s)", fmtAgoTR(rangeS))
	if env != "" {
		src += fmt.Sprintf("; problemler ortam: %s", env)
	}
	return b.String(), src, nil
}

// maxTeamServices — takım-kapsamlı MV okumasının IN listesi tavanı.
// Üstü okunmaz ve kanıt bunu SÖYLER. v0.9.1134'te fonksiyon içinden
// dosya kapsamına çıkarıldı: guidedTeamServicesBundle da aynı tavana
// uymalı, iki ayrı sayı zamanla ayrışırdı. v0.9.1244'te mcptools'a
// taşındı — get_team_services de aynı tavanı ilan ediyor.
const maxTeamServices = mcptools.MaxTeamServices

// servicesForUserTeam (v0.9.375) — kullanıcının takımına ait servisler:
// ownerTeam VEYA sreTeam eşleşmesi (case-insensitive). Inbox'ın
// servicesForTeam'inden farkı: orada owner ve SRE ayrı süzgeçler (AND),
// burada "benim servisim" iki rolün BİRLEŞİMİ.
//
// v0.9.1244 — gövde mcptools.TeamServiceNames'e taşındı (get_team_services
// AYNI çözümlemeyi kullanmak zorunda; mcptools api'yi import edemez).
// Burası tek satırlık delegasyon: KİMLİK yarısı (CallMeta → users →
// User.Team) api'de kalıyor, çünkü MCP tarafında kimlik yok.
func servicesForUserTeam(ta chstore.TeamAliases, mds map[string]chstore.ServiceMetadata, team string) []string {
	return mcptools.TeamServiceNames(ta, mds, team)
}

// guidedMyTeamBundle (v0.9.375, operatör istegi) — "takımımın
// servisleri / problemleri". Kimlik CallMeta'dan; takım users
// tablosundan (tek satır FINAL okuma); servis eşlemesi metadata
// katalogundan. Takımsız kullanıcı ve servissiz takım DÜRÜST cevap
// alır (boş liste sessizce "her şey" olmaz — inbox'ın v0.9.353
// dersinin tersi yönü). wantProblems=false → aile-sağlık bundle'ı
// (tek MV okuması), true → o servislerin açık problemleri.
// guidedMyTeamBundle — takım-kapsamlı üç sorunun ORTAK gövdesi.
//
// v0.9.650 — `wantProblems bool` yerine KİP aldı. Üçüncü bir soru
// (takımın exception'ları) gelince bool yetmiyordu ve alternatif,
// kullanıcı→takım→servis çözümlemesini (25 satır: oturum kimliği,
// takım ataması, katalog eşleşmesi, 100-servis tavanı, her birinin
// kendi operatör mesajı) İKİNCİ bir yere kopyalamaktı. Bu kod
// tabanında tekrar eden hata sınıfı tam olarak o: bir kural iki yere
// bölünüp zamanla ayrışıyor.
//
// mode: "health" | "problems" | "exceptions"
func (s *Server) guidedMyTeamBundle(ctx context.Context, emit func(string, any), mode string, route *guidedRoute, from, to time.Time, rangeS int64) (string, string, error) {
	env := route.Env
	meta := copilot.MetaFromContext(ctx)
	if meta.UserID == "" {
		// v0.9.1134 — eskiden burada ÇIKMAZ vardı ("yanıtlanamıyor").
		// Artık takımı SORUYORUZ ve canlı takım listesini çip olarak
		// sunuyoruz; operatör tıklayınca guidedTeamServices devralıyor.
		return s.guidedAskTeamEvidence(ctx, route,
			"Oturum kimliği yok (auth kapalı ya da token kullanıcıya bağlı değil), bu yüzden kullanıcının takımını KENDİM okuyamıyorum.\n",
			"Oturum kimliğini okuyamadığım için takımını bilemiyorum. ")
	}
	nTeam := emitGuidedStep(emit, "resolve_user_team", "")
	u, uerr := s.store.GetUserByID(ctx, meta.UserID)
	if uerr != nil || u == nil {
		err := fmt.Errorf("user lookup: %w", uerr)
		emitGuidedStepResult(emit, nTeam, "resolve_user_team", "", err)
		return "", "", err
	}
	if u.Team == "" {
		ev, src, aerr := s.guidedAskTeamEvidence(ctx, route,
			fmt.Sprintf("Kullanıcının (%s) hesabına takım atanmamış (admin Settings → Users → Team alanı bunu kalıcı çözer).\n", u.Email),
			fmt.Sprintf("Hesabına (%s) takım atanmamış — admin Settings → Users → Team alanından atayabilir. ", u.Email))
		emitGuidedStepResult(emit, nTeam, "resolve_user_team", ev, aerr)
		return ev, src, aerr
	}
	mds, merr := s.store.ListServiceMetadata(ctx)
	if merr != nil {
		emitGuidedStepResult(emit, nTeam, "resolve_user_team", "", merr)
		return "", "", merr
	}
	ta := s.teamAliasesCtx(ctx)
	svcs := servicesForUserTeam(ta, mds, u.Team)
	if len(svcs) == 0 {
		ev := fmt.Sprintf("%q takımı hiçbir serviste ownerTeam/sreTeam olarak geçmiyor (Service Catalog). Kullanıcıya söyle: katalogda takım ataması yapılmalı.\n", u.Team)
		emitGuidedStepResult(emit, nTeam, "resolve_user_team", ev, nil)
		return ev, fmt.Sprintf("servis kataloğu (takım: %s)", u.Team), nil
	}
	trimmed := 0
	if len(svcs) > maxTeamServices {
		trimmed = len(svcs) - maxTeamServices
		svcs = svcs[:maxTeamServices]
	}
	// v0.9.651 — çözülen liste rotaya yazılıyor; guidedSuggestions
	// buradan servis-adlı çipler üretiyor.
	route.TeamServices = svcs
	// v0.9.1246 — KİMLİK burada çözülüyor, LİNKTE değil: "takımımın
	// exception'ları" cevabının altındaki derin link
	// takımın adını yazacak (v0.10.1397: /problems?owner|sre=<AD>), yani URL paylaşılabilir
	// olmalı ve "benim" kelimesini TAŞIYAMAZ (başkasının açtığı link
	// onun takımını gösterirdi — kimlik URL'e girmez).
	//
	// Yazım KATALOGDAN (TeamDisplayName): users tablosunda "sy" yazıyor
	// olabilir ama katalogda "SY" geçiyorsa çipte kataloğun yazımı
	// görünmeli. Eşleşme zaten katlamalı (TeamEqual), yani fark
	// yalnızca operatörün GÖRDÜĞÜ metinde — ve orada iki yazım iki ayrı
	// takım gibi okunuyor.
	//
	// Servissiz takımda (yukarıdaki erken dönüş) bilerek YAZILMIYOR:
	// cevabın kendisi "bu takım hiçbir serviste geçmiyor" diyorken
	// takım-filtreli boş bir sayfaya link vermek, cevabı tekrar eden
	// bir çıkmaz olurdu.
	route.Team = mcptools.TeamDisplayName(ta, mds, u.Team)
	header := fmt.Sprintf("Kullanıcının takımı: %s — %d servis (owner/SRE eşleşmesi).\n", u.Team, len(svcs)+trimmed)
	if trimmed > 0 {
		header += fmt.Sprintf("Not: ilk %d servis okundu, %d servis dışarıda kaldı.\n", maxTeamServices, trimmed)
	}
	// v0.9.1229 — çözümün kanıtı: takım satırı + ÇÖZÜLEN servis adları.
	// Adlar prompt'a liste olarak girmiyor ama okumanın çıktısı tam olarak
	// bu; "hangi servisleri benim saydı" operatörün ilk sorusu.
	emitGuidedStepResult(emit, nTeam, "resolve_user_team",
		header+"Çözülen servisler:\n- "+strings.Join(svcs, "\n- ")+"\n", nil)
	if mode == "problems" {
		nProb := emitGuidedStep(emit, "list_problems", fmt.Sprintf(`{"status":"open","teamServices":%d}`, len(svcs)))
		probs, probTotal, perr := s.guidedProblemsWithTotal(ctx, chstore.ProblemFilter{Status: "open", Services: svcs, Env: env, Limit: 50})
		if perr != nil {
			emitGuidedStepResult(emit, nProb, "list_problems", "", perr)
			return "", "", perr
		}
		probs = s.enrichProblemsForRead(ctx, probs) // v0.9.553 — deploy+öncelik, sırası sabit
		emitGuidedStepResult(emit, nProb, "list_problems",
			renderProblemsEvidenceTR(probs, "", env, evidenceAsOf(to), probTotal), nil)
		nRC := emitGuidedStep(emit, "root_cause_hypotheses", "")
		probs = s.store.EnrichProblemsWithRootCause(ctx, probs)
		var b strings.Builder
		b.WriteString(header)
		atProb := b.Len()
		if len(probs) == 0 {
			fmt.Fprintf(&b, "Takımın servislerinde açık problem yok.\n")
		} else {
			b.WriteString(renderProblemsEvidenceTR(probs, "", env, evidenceAsOf(to), probTotal))
		}
		emitGuidedStepResult(emit, nRC, "root_cause_hypotheses", guidedStepSegment(&b, atProb), nil)
		src := fmt.Sprintf("takım: %s (%d servis) — açık problemler + triage önceliği + kök-neden hipotezleri", u.Team, len(svcs)+trimmed)
		return b.String(), src, nil
	}
	if mode == "top_errors" { // v0.10.1256
		n := emitGuidedStep(emit, "top_error_services", fmt.Sprintf(`{"teamServices":%d}`, len(svcs)))
		rows, terr := s.store.TopErrorServices(ctx, from, to, svcs, topErrorRows)
		if terr != nil {
			emitGuidedStepResult(emit, n, "top_error_services", "", terr)
			return "", "", terr
		}
		ev := renderTopErrorsTR("Takımın en hatalı servisleri", rows, rangeS, "servis")
		emitGuidedStepResult(emit, n, "top_error_services", ev, nil)
		route.TeamServices = errorRankNames(rows) // takip çipleri için sıralı
		return header + ev, fmt.Sprintf("takım: %s — servis hata sıralaması (service_summary_5m)", u.Team), nil
	}
	if mode == "exceptions" {
		// v0.9.650 (operatör: "Takımıma ait servislerin hataları
		// (Exceptions) neler?") — Problem ile Exception AYRI yüzeyler.
		// v0.10.1385 (operatör: "Ekibimin teknik hataları var mı diye
		// sorduğumda CoSRE arkada Exceptions açıp filtrelenebilir …") —
		// okuma artık Exceptions KUTUSU (span + Oracle grupları, durum, son
		// görülme, öncelik), en son görülen önce; ham GetExceptions
		// gruplaması kaldırıldı (copilot_team_exceptions.go).
		blk := s.teamExceptionsBlock(ctx, emit, route, teamExcInput{Team: route.Team, Services: svcs, Axis: teamExceptionsAxis(ta, mds, u.Team)}, from, to)
		src := fmt.Sprintf("takım: %s (%d servis) — Exceptions kutusu (açık gruplar, en son görülen önce)", u.Team, len(svcs)+trimmed)
		return header + blk, src, nil
	}
	// v0.10.1397 (operatör: "bir takım seçtim veya 'takımımın servisleri
	// nasıl' dedim. Arkada chip'e basmadan otomatik o takımın servislerini
	// listelesin…") — kimlikten çözülen takımın cevabı, adı geçen takımın
	// cevabıyla AYNI gövde (guidedTeamServicesBundle: hata oranı sırası,
	// takım özeti, deterministik servis listesi + "hangi servis?" sorusu).
	// Çözüm aynı katalog kuralı (mcptools.TeamServiceNames); route.Team
	// yukarıda katalog yazımına çekildi. Eski aile-sağlık gövdesi (hata
	// SAYISI sırası) iki takım cevabını ayrıştırıyordu.
	evidence, src, err := s.guidedTeamServicesBundle(ctx, emit, route, from, to, rangeS)
	if err != nil {
		return "", "", err
	}
	return header + evidence, fmt.Sprintf("takım: %s — %s", u.Team, src), nil
}

// guidedTeamAskMax — "hangi takım?" turunda sunulan çip sayısı. 8, çip
// şeridinin tek satırda okunabildiği üst sınır. v0.10.559: seçim türe göre
// DÖNÜŞÜMLÜ (uygulama takımı, SRE takımı, …) — yalnız servis sayısına göre
// alınınca SRE takımları tavanı tek başına dolduruyordu (operatör-raporlu).
// Liste dışında bir takımı YAZABİLECEĞİ de kanıtta söyleniyor — çipler bir
// menü değil, kısayol.
const guidedTeamAskMax = 8

// guidedAskTeamEvidence (v0.9.1134, operatör istegi: "takım bilinmiyorsa
// KULLANICIYA SOR") — takım-kapsamlı bir soru kimliğe oturmadığında
// üretilen kanıt. İki degrade dalının (kimlik yok / takım atanmamış)
// ORTAK gövdesi; ikisi de eskiden çıkmaz cümleyle bitiyordu.
//
// Akışın mekaniği: buradaki takım adları route.TeamOptions'a yazılır,
// guidedSuggestions onları ÇIPLAK metin çipine çevirir, çipe tıklamak o
// metni yeni bir kullanıcı mesajı olarak gönderir ve router çıplak takım
// adını guidedTeamServices'e yönlendirir. Sunucuda konuşma durumu YOK —
// tek dayanak "çıplak takım adı kendi başına yönlenebilir" olması.
//
// whyUser — aynı gerekçenin operatöre giden hâli: cevap metnini sunucu
// kurar (route.DirectAnswer), anlatım çağrısı yapılmaz; kanıt metni ⚙
// çipinin dayanağı olarak kalır.
func (s *Server) guidedAskTeamEvidence(ctx context.Context, route *guidedRoute, why, whyUser string) (string, string, error) {
	entries := s.guidedTeamCatalogue(ctx)
	opts, _, _, _ := teamAskOptions(entries, guidedTeamAskMax) // v0.10.559 — tür dönüşümlü
	route.TeamOptions = opts
	route.DirectAnswer = askTeamAnswerTR(whyUser, entries, guidedTeamAskMax)
	var b strings.Builder
	b.WriteString(why)
	if len(opts) == 0 {
		b.WriteString("Servis kataloğunda hiç takım ataması da yok (ownerTeam/sreTeam boş). " +
			"Kullanıcıya söyle: şimdilik belirli bir SERVİS adıyla sorabilir; kalıcı çözüm Service Catalog'da takım atamak.\n")
		return b.String(), "kullanıcı kimliği + servis kataloğu (takım ataması yok)", nil
	}
	b.WriteString("KULLANICIYA SOR: hangi takımda çalışıyor? Takım adını söylediğinde o takımın servislerini " +
		"EN ÇOK HATA ALAN önce sıralayıp getireceğim.\n")
	b.WriteString(renderTeamAskEvidenceTR(entries, guidedTeamAskMax)) // v0.10.559 — iki grup
	b.WriteString("Bu adlar cevabın altında ÇİP olarak da duruyor — tıklaması yeter; listede yoksa adı yazabileceğini de söyle. İki grubu da say (uygulama VE SRE).\n")
	b.WriteString("KURAL: takım adı UYDURMA, yalnız yukarıdaki listeyi say.\n")
	return b.String(), "servis kataloğu takım listesi (takım sorusu)", nil
}

// askTeamAnswerTR — SAF: takım sorusunun operatöre giden metni. İki grup
// (uygulama VE SRE) ve "+N takım" satırı renderTeamAskEvidenceTR'den —
// v0.10.559'un "yalnız SRE takımları sayıldı" düzeltmesi korunur.
func askTeamAnswerTR(whyUser string, entries []mcptools.TeamCatalogueEntry, max int) string {
	opts, _, _, _ := teamAskOptions(entries, max)
	if len(opts) == 0 {
		return whyUser + "Servis kataloğunda takım ataması yok; şimdilik belirli bir servis adıyla sorabilirsin. " +
			"Kalıcı çözüm: Service Catalog'da takım atamak."
	}
	return whyUser + "Hangi takımdasın? Alttaki çiplerden birine tıkla ya da takım adını yaz; " +
		"o takımın servislerini en çok hata alandan başlayarak getiririm.\n" + renderTeamAskEvidenceTR(entries, max)
}

// askServiceAnswerTR — SAF: servis netleştirme sorusunun operatöre giden metni.
func askServiceAnswerTR(opts []string) string {
	if len(opts) == 0 {
		return "Servis adını çözemedim ve katalogda aday bulamadım. Servis adını yazar mısın? (Servisler sayfasından kopyalayabilirsin.)"
	}
	return fmt.Sprintf("Hangi servisi kastettin? Adaylar: %s. Alttaki çiplerden birine tıkla ya da listede yoksa adı yaz.",
		strings.Join(opts, ", "))
}

// teamServicesMaxRows — kanıtta listelenen servis satırı tavanı. Üstü
// "… ve N servis daha" satırıyla DÜRÜSTÇE söylenir; 15, küçük modelin
// (gemma4) anlatıda kaybetmeden sayabildiği üst sınır.
const teamServicesMaxRows = 15

// SIRALAMA SÖZLEŞMESİ (v0.9.1134, operatör istegi: "en çok hata alan /
// error rate yüksek olanlara göre sırala") — ORAN birincil, SAYI eşitlik
// bozucu, ad üçüncü. guidedFamilyHealthBundle'ın sırası bilerek FARKLI
// (sayı birincil): orada soru "hangisinde hata var", yani mutlak hacim.
//
// v0.9.1244 — sıralama artık ORTAK OKUMANIN İÇİNDE
// (mcptools.ReadTeamServicesRED → SortServicesByErrorRate), yani takım
// cevabının "en kötü servis" iddiası guided'da ve get_team_services'te
// aynı satırı gösteriyor. Tablo testi implementasyonun yanında:
// mcptools/team_ownership_test.go.

// renderTeamServicesEvidenceTR — takım servis listesinin kanıt bloğu
// (saf; tablo-testli). rows ZATEN sıralı gelmeli (ortak okuma sıralıyor).
// readSvcs = MV'ye sorulan servis sayısı, trimmed = tavana takılıp hiç
// sorulmayanlar. Üç dürüstlük satırı: tavana takılanlar, satır tavanı,
// pencerede hiç span üretmeyenler.
func renderTeamServicesEvidenceTR(team string, rows []chstore.ServiceSummary, readSvcs, trimmed int, rangeS int64, env string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Takım: %s — %d servis (Service Catalog ownerTeam/sreTeam eşleşmesi).\n", team, readSvcs+trimmed)
	if trimmed > 0 {
		fmt.Fprintf(&b, "Not: ilk %d servis okundu, %d servis dışarıda kaldı.\n", maxTeamServices, trimmed)
	}
	if env != "" {
		fmt.Fprintf(&b, "Not: RED değerleri tüm ortamların toplamı; %q ortam daraltması bu listede UYGULANMADI.\n", env)
	}
	if len(rows) == 0 {
		b.WriteString("Bu pencerede takımın hiçbir servisinden span verisi yok — hata oranı okunamıyor.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Servisler HATA ORANINA göre azalan (son %s, eşitlikte hata sayısı):\n", fmtAgoTR(rangeS))
	for i, r := range rows {
		if i >= teamServicesMaxRows {
			fmt.Fprintf(&b, "… ve %d servis daha (hepsinin hata oranı daha düşük).\n", len(rows)-teamServicesMaxRows)
			break
		}
		fmt.Fprintf(&b, "- %s: hata oranı %%%.2f (%d hata), p99=%.0fms, %d span\n",
			r.Name, r.ErrorRate, r.ErrorCount, r.P99Ms, r.SpanCount)
	}
	if silent := readSvcs - len(rows); silent > 0 {
		fmt.Fprintf(&b, "Not: takımın %d servisi bu pencerede hiç span üretmedi (listede yok, sessiz olabilir).\n", silent)
	}
	// YÖNLENDİRME kuralı (operatörün asıl istegi "kullanıcıyı ilgili
	// servise yönlendir"): liste tek başına bir cevap değil. Model en
	// kötü servisi ADIYLA söylemeli, yoksa 15 satırlık tablo operatöre
	// karar bırakır. Hata yoksa bunu da açıkça söylemeli — "en kötü"
	// diye temiz bir servisi işaretlemek yanlış alarm olurdu.
	// v0.10.1397 — tam liste + "hangi servis?" sorusu artık SUNUCU EKİ
	// (copilot_team_overview.go); model listeyi tekrar yazmaz, soruyu sormaz.
	b.WriteString("KURAL: cevaba EN KÖTÜ servisi adıyla ve sayısıyla başla; 2-4 cümlelik kısa özet ver. " +
		"Hiçbir serviste hata yoksa bunu AÇIKÇA söyle ve kimseyi işaretleme. " +
		"Servis tablosu, son exception grupları ve kapanış sorusu cevabın ALTINA sunucu tarafından ekleniyor — " +
		"tabloyu/listeyi TEKRAR YAZMA, kapanış sorusu SORMA. Listede olmayan servis adı UYDURMA.\n")
	return b.String()
}

// guidedTeamServicesBundle (v0.9.1134, operatör istegi) — ADI GEÇEN
// takımın servisleri, en çok hata alan önce.
//
// Okuma yolu my_services ile AYNI: takım→servis eşlemesi katalogdan
// (servicesForUserTeam, alias farkındalıklı), RED tek MV okumasından
// (GetServicesAggFilteredIn — servis başına fan-out YOK). Ayrışan tek şey
// sıralama: operatör oranı istedi (sortServicesByErrorRate).
func (s *Server) guidedTeamServicesBundle(ctx context.Context, emit func(string, any), route *guidedRoute, from, to time.Time, rangeS int64) (string, string, error) {
	team := route.Team
	if team == "" {
		return "", "", errors.New("team_services: takım adı boş")
	}
	nResolve := emitGuidedStep(emit, "resolve_team_services", `{"team":`+jsonStr(team)+`}`)
	svcs, trimmed, merr := mcptools.ReadTeamServiceNames(ctx, s.mcpDeps(), team)
	if merr != nil {
		emitGuidedStepResult(emit, nResolve, "resolve_team_services", "", merr)
		return "", "", merr
	}
	if len(svcs) == 0 {
		ev := fmt.Sprintf("%q takımı hiçbir serviste ownerTeam/sreTeam olarak geçmiyor (Service Catalog). "+
			"Kullanıcıya söyle: katalogda takım ataması yapılmalı ya da başka bir takım adı denemeli.\n", team)
		emitGuidedStepResult(emit, nResolve, "resolve_team_services", ev, nil)
		return ev, fmt.Sprintf("servis kataloğu (takım: %s)", team), nil
	}
	// Çözümün kanıtı, RED okumasından ÖNCE: hangi servisler bu takım
	// sayıldı? (v0.9.1229 — katalog eşleşmesi okumanın kendi çıktısı.)
	// Tavan (mcptools.MaxTeamServices) çözümlemenin İÇİNDE uygulandı;
	// `trimmed` tavana takılıp hiç okunmayanları sayıyor.
	emitGuidedStepResult(emit, nResolve, "resolve_team_services",
		fmt.Sprintf("%q takımına eşleşen servisler (%d):\n- %s\n", team, len(svcs), strings.Join(svcs, "\n- ")), nil)
	// v0.10.1473 — takımın son HATA logları (ES, tek sınırlı _search) RED +
	// takım özeti okunurken PARALEL koşar; Settings'te takım alanı boşsa nil
	// (okuma yok). Sonuç aşağıda en çok teamLogsWait beklenir.
	teamLogs := s.startTeamLogs(ctx, team, nil, time.Now())
	nRED := emitGuidedStep(emit, "team_services_red", fmt.Sprintf(`{"team":%s,"services":%d}`, jsonStr(team), len(svcs)))
	// v0.9.1244 — okuma + hata-oranı sırası ortak katmanda
	// (get_team_services AYNI çağrıyı yapıyor): "en kötü servis" iddiası
	// iki yüzeyde de aynı satırı göstersin.
	rows, err := mcptools.ReadTeamServicesRED(ctx, s.mcpDeps(), svcs, from, to)
	if err != nil {
		emitGuidedStepResult(emit, nRED, "team_services_red", "", err)
		return "", "", err
	}
	// v0.9.651 emsali — çözülen liste rotaya yazılıyor; çipler ve derin
	// linkler buradan besleniyor. SIRA hata oranına göre, yani
	// TeamServices[0] = EN KÖTÜ servis (link/çip metni ona bakıyor).
	names := make([]string, 0, teamServicesMaxRows)
	for i, r := range rows {
		if i >= teamServicesMaxRows {
			break
		}
		names = append(names, r.Name)
	}
	if len(names) == 0 {
		// Pencerede hiç veri yok: çipler yine de takımın servislerini
		// adlandırsın (alfabetik) — boş çip şeridi operatörü çıkmaza sokar.
		for i, sv := range svcs {
			if i >= teamServicesMaxRows {
				break
			}
			names = append(names, sv)
		}
	}
	route.TeamServices = names
	b := strings.Builder{}
	redBlk := renderTeamServicesEvidenceTR(team, rows, len(svcs), trimmed, rangeS, route.Env)
	emitGuidedStepResult(emit, nRED, "team_services_red", redBlk, nil)
	b.WriteString(redBlk)
	// v0.10.1383 — takım özeti (problem/deploy/exception) + servis başına
	// takip adımları; çip sırası RED sırası (en kötü servis aynı kalır).
	// v0.10.1397 — my_services da bu gövdeden geçiyor; onun özet çipleri
	// eskisi gibi P1 → açık problem sırasıyla (rerank), team_services RED sırası.
	// v0.10.1515 — özet servis çipi yazmaz (iki genel seçici çipi teamOverviewBlock'ta).
	b.WriteString(s.teamSummaryBlock(ctx, emit, route, team, svcs, from, to, rangeS))
	// v0.10.1397 — deterministik servis listesi + son exception grupları +
	// "Özellikle kontrol etmek istediğin bir servis var mı?" (route.AnswerTail)
	// ve servis başına çipler; özet çiplerinden SONRA (onları öne ekler).
	b.WriteString(s.teamOverviewBlock(ctx, emit, route, teamOverviewInput{Team: team, Services: svcs, Trimmed: trimmed, Rows: rows, Names: names}, from, to, rangeS))
	// v0.10.1473 — "Son loglar (takım)" bölümü + kanıt çipi (kapalıysa "").
	b.WriteString(s.teamLogsBlock(emit, team, teamLogs))
	// En kötü servisin canlı error_rate kartı (aile bundle'ının emsali).
	if len(rows) > 0 && rows[0].ErrorCount > 0 {
		b.WriteString(chartFence(guidedChartSpec{
			Title: rows[0].Name + " · error_rate", Service: rows[0].Name,
			Agg: "error_rate", RangeS: rangeS,
		}))
	}
	src := fmt.Sprintf("takım: %s (%d servis) — servis RED'i tek MV okuması, hata oranına göre sıralı (son %s)",
		team, len(svcs)+trimmed, fmtAgoTR(rangeS))
	return b.String(), src, nil
}

// jsonStr — step-event args'ında ad kaçışı. fmt %q strconv.Quote'tur ve
// kontrol karakterli bir adda geçersiz JSON üretir (v0.9.187 dersi).
func jsonStr(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// guidedFamilyHealthBundle (v0.9.192) — compares a service FAMILY's RED
// side by side ("mobile bff'lerde hangisinde hata var"). ONE MV read
// (GetServicesAggFilteredIn with the family as the serviceIn allowlist)
// — no per-service fan-out. Evidence is error-ranked so the narration
// can answer "hangisinde" directly; the worst service gets a live
// error_rate chart.
func (s *Server) guidedFamilyHealthBundle(ctx context.Context, emit func(string, any), family []string, env string, from, to time.Time, rangeS int64) (string, string, error) {
	nFam := 0
	if argsB, merr := json.Marshal(map[string]any{"services": family}); merr == nil {
		nFam = emitGuidedStep(emit, "family_context", string(argsB))
	}
	rows, err := s.store.GetServicesAggFilteredIn(ctx, from, to, "", family, "", "", len(family), 0)
	if err != nil {
		emitGuidedStepResult(emit, nFam, "family_context", "", err)
		return "", "", err
	}
	// Most-broken first: errors desc, then error-rate, then traffic.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ErrorCount != rows[j].ErrorCount {
			return rows[i].ErrorCount > rows[j].ErrorCount
		}
		if rows[i].ErrorRate != rows[j].ErrorRate {
			return rows[i].ErrorRate > rows[j].ErrorRate
		}
		return rows[i].SpanCount > rows[j].SpanCount
	})

	var b strings.Builder
	if env != "" {
		fmt.Fprintf(&b, "Not: RED değerleri tüm ortamların toplamı; %q ortam daraltması bu karşılaştırmada uygulanmıyor.\n", env)
	}
	fmt.Fprintf(&b, "Servis ailesi (%d servis, son %d dakika), hata sayısına göre sıralı:\n", len(family), rangeS/60)
	const maxRows = 12
	winSec := float64(rangeS)
	for i, r := range rows {
		if i >= maxRows {
			fmt.Fprintf(&b, "… ve %d servis daha (hepsi daha az hatalı).\n", len(rows)-maxRows)
			break
		}
		rate := 0.0
		if winSec > 0 {
			rate = float64(r.SpanCount) / winSec
		}
		// v0.10.944 — SpanCount/pencere: tüm span türleri, istek hızı değil.
		fmt.Fprintf(&b, "- %s: rate=%.1f span/s, error=%.2f%% (%d hata), p99=%.0fms\n",
			r.Name, rate, r.ErrorRate, r.ErrorCount, r.P99Ms)
	}
	if len(rows) == 0 {
		b.WriteString("Bu pencerede ailenin hiçbir servisinden span verisi yok.\n")
	}
	// v0.9.1229 — kanıt, grafik bloğundan ÖNCEKİ metin: ```chart``` fence'i
	// okumanın çıktısı değil, ondan türetilmiş bir görselleştirme talimatı.
	emitGuidedStepResult(emit, nFam, "family_context", b.String(), nil)
	// Worst-offender chart: the family question is about errors, so the
	// top row (errors-ranked) gets the live error_rate card.
	if len(rows) > 0 && rows[0].ErrorCount > 0 {
		b.WriteString(chartFence(guidedChartSpec{
			Title: rows[0].Name + " · error_rate", Service: rows[0].Name,
			Agg: "error_rate", RangeS: rangeS,
		}))
	}
	src := fmt.Sprintf("servis ailesi RED karşılaştırması — %d servis tek MV okuması (son %s)", len(family), fmtAgoTR(rangeS))
	return b.String(), src, nil
}

// guidedChartSpec is the deterministic chart block CoSRE emits inside a
// ```chart``` fence. json.Marshal (v0.9.187) — NOT fmt %q, which is
// strconv.Quote: a service/operation name with a control char produces
// \a / \x1b escapes that are invalid JSON, so the frontend's JSON.parse
// throws and the chart is silently dropped. Marshal keeps the block
// valid for any name.
type guidedChartSpec struct {
	Title     string `json:"title"`
	Service   string `json:"service"`
	Operation string `json:"operation,omitempty"`
	Agg       string `json:"agg"`
	RangeS    int64  `json:"rangeS"`
	// v0.9.1186 (Faz 4.4) — kırılım. TEK anahtar, çünkü sohbet balonundaki
	// ~560px'lik bir kartta iki anahtarlı kırılım seri sayısını çarpar ve
	// okunmaz bir spagetti üretir; kırılım tam da okunabilirlik için var.
	GroupBy string `json:"groupBy,omitempty"`
	// FromNs/ToNs — MUTLAK pencere (unix ns). Doluysa RangeS'i ezer.
	//
	// Neden yalnız SUNUCU doldurur (tool'da yok): küçük modele epoch
	// nanosaniye hesaplatmak, [[project-copilot-runtime]]'ın "prefetch+
	// narrate, aritmetik yaptırma" doktrininin tersi. Guided/insight
	// yolları pencereyi ZATEN biliyor (problemin/olayın penceresi) —
	// bilen taraf yazsın, modelden istemeyelim.
	FromNs int64 `json:"fromNs,omitempty"`
	ToNs   int64 `json:"toNs,omitempty"`
	// v0.10.547 (Faz 3.5) — karşılaştırma (kesikli ikinci seri, shiftS s önce)
	// ve kaynak ("metric" = metrik deposu / VM; boş = span rollup).
	Compare *guidedChartCompare `json:"compare,omitempty"`
	Source  string              `json:"source,omitempty"`
}

type guidedChartCompare struct {
	Kind   string `json:"kind"`
	ShiftS int64  `json:"shiftS"`
}

func chartFence(spec guidedChartSpec) string {
	j, err := json.Marshal(spec)
	if err != nil {
		return ""
	}
	return "\n```chart\n" + string(j) + "\n```\n"
}

// resolveGuidedOperation upgrades a service-health question to an
// operation-scoped one. Two signals, precision over recall:
//  1. the question TEXT contains a live operation name of the service
//     (bounded substring, len ≥ 6 to skip bare verbs) — the strongest,
//     "GET /orders/:id nasıl";
//  2. the operator is ON an operation page (ctxOperation from ?op=) AND
//     the message has an operation-signal word ("bu operasyonun durumu").
//
// Returns "" (→ service-level) when neither fires, so "checkout nasıl"
// stays a service answer. The op-name list is Redis-cached 60s.
func (s *Server) resolveGuidedOperation(ctx context.Context, service, raw, ctxOperation string) string {
	if service == "" {
		return ""
	}
	return pickGuidedOperation(normalizeGuidedMsg(raw), s.guidedOperationNames(ctx, service), ctxOperation)
}

// pickGuidedOperation is the PURE core of resolveGuidedOperation (table-
// tested in copilot_guided_test.go). msg is already normalized; ops is
// the live operation-name list; ctxOperation is the ?op= the operator is
// viewing. Longest text-matched op wins; else the ctx op iff a signal
// word is present AND it's a real op; else "".
func pickGuidedOperation(msg string, ops []string, ctxOperation string) string {
	best := ""
	for _, op := range ops {
		if len(op) < 6 || !opNameDistinctive(op) {
			continue
		}
		// indexBounded (not strings.Contains) so an op name never matches
		// INSIDE a word/service token — same word-boundary discipline the
		// service matcher uses ("mobile-bff" ∉ "mobile-bff-uat").
		if indexBounded(msg, normalizeGuidedMsg(op)) >= 0 && len(op) > len(best) {
			best = op
		}
	}
	if best != "" {
		return best
	}
	if ctxOperation != "" && hasThisOperationSignal(msg) {
		for _, op := range ops {
			if op == ctxOperation {
				return ctxOperation // guard a stale ?op= against the live list
			}
		}
	}
	return ""
}

// opNameDistinctive — the op name carries a structural separator
// (space/slash/dot/colon) that real APM span names have ("GET /orders",
// "SELECT users", "svc.Method/Call") but a bare business word
// ("checkout", "payment") does not. Free-text matching a BARE word
// collides with the service-identifying token that resolved the service
// (asking "checkout neden yavaş?" is a SERVICE question, not the span
// named "checkout"), so bare-word ops are reachable only via the ?op=
// context fallback, never text-match. (v0.9.184 review-fix.)
func opNameDistinctive(op string) bool {
	return strings.ContainsAny(op, " /.:")
}

// hasThisOperationSignal — the message DEICTICALLY points at one
// operation ("bu operasyon", "this endpoint"). Used ONLY for the ?op=
// context fallback. Demonstrative-scoped on purpose: a bare noun like
// "işlem"/"route" also appears in whole-service asks ("tüm işlemler
// nasıl?") which must NOT be narrowed to the viewed op. (v0.9.184
// review-fix — plural/bare-noun false-trigger.)
func hasThisOperationSignal(msg string) bool {
	for _, kw := range []string{
		"bu operasyon", "bu endpoint", "bu işlem", "bu islem",
		"bu uç nokta", "bu uc nokta", "bu servis çağrısı",
		"şu operasyon", "su operasyon", "this operation", "this endpoint",
	} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// guidedOperationNames returns a service's live operation (span-name)
// list for entity extraction, Redis-cached 60s per service. Soft-fails
// to nil (→ resolveGuidedOperation returns "", service-level answer).
func (s *Server) guidedOperationNames(ctx context.Context, service string) []string {
	key := "copilot:guided:opnames:" + service
	if b, ok, _ := s.cache.Get(ctx, key); ok && len(b) > 0 {
		var names []string
		if json.Unmarshal(b, &names) == nil {
			return names
		}
	}
	names, _, err := s.store.ListOperationNames(ctx, service, "", 500, 0)
	if err != nil {
		return nil
	}
	if b, merr := json.Marshal(names); merr == nil {
		_ = s.cache.Set(ctx, key, b, 60*time.Second)
	}
	return names
}

// (c) "en yavaş/slowest traces [service]" → duration-ranked trace
// summaries from the trace_summary_5m fast path (Sort=duration,
// CountMode=skip — the same shape /traces uses). env (v0.8.398) rides
// TraceFilter.Env — the direct deploy_env conjunct; a non-empty env
// takes the raw-fallback path exactly like the /traces ?env= pick.
func (s *Server) guidedSlowTracesBundle(ctx context.Context, emit func(string, any), service, env string, from, to time.Time, rangeS int64) (string, string, error) {
	nSlow := emitGuidedStep(emit, "slow_traces", withEnvArg(`{"service":"`+service+`","sort":"duration"}`, env))
	rows, _, _, err := s.store.GetTraces(ctx, guidedTraceFilter(service, env, from, to))
	if err != nil {
		emitGuidedStepResult(emit, nSlow, "slow_traces", "", err)
		return "", "", err
	}
	src := fmt.Sprintf("duration'a göre sıralı trace listesi (son %s)", fmtAgoTR(rangeS))
	if env != "" {
		src += fmt.Sprintf(", ortam: %s", env)
	}
	ev := renderSlowTracesEvidenceTR(rows, service, env, rangeS)
	emitGuidedStepResult(emit, nSlow, "slow_traces", ev, nil)
	// v0.10.1327 — listenin en yavaş 1–2 trace'i: kritik yol + o trace'lerin
	// logları (chat_trace_drill.go); liste aynı okumadan, ikinci GetTraces yok.
	if drill := s.traceDrillEvidence(ctx, emit, pickDrillTraces(rows), service, from, to); drill != "" {
		ev += "\n" + drill
		src += " + en yavaş trace kritik yolu + o trace'lerin logları"
	}
	return ev, src, nil
}

// guidedTraceBundle — v0.9.537. Yapıştırılan/bağlamdan gelen trace
// ID'sinin kanıt paketi. Montaj buildTraceExplainInput'un AYNISI
// (explain butonu, çekmece sohbeti ve guided yol tek paketi paylaşır —
// v0.9.482 deseni): span seçimi + ilişkili loglar + dürüstlük notu.
//
// Bulunamayan trace HATA DEĞİL dürüst kanıttır: yol serbest döngüye
// düşerse operatör yine cevapsız kalırdı; model "bulunamadı + olası
// nedenler" kanıtını anlatır (guided prompt uydurmayı yasaklar).
func (s *Server) guidedTraceBundle(ctx context.Context, emit func(string, any), traceID string) (string, string, error) {
	nTrace := emitGuidedStep(emit, "trace", `{"id":"`+traceID+`"}`)
	in, err := s.buildTraceExplainInput(ctx, traceID)
	if errors.Is(err, errExplainTraceNotFound) {
		ev := fmt.Sprintf("Trace %s Coremetry deposunda BULUNAMADI.\n"+
			"Olası nedenler: ID eksik/yanlış kopyalandı, trace retention "+
			"penceresinin dışında kaldı, ya da bu ortam yerine başka bir "+
			"ortamda üretildi. Kullanıcıya bunu söyle; span/istatistik UYDURMA.", traceID)
		// Bulunamamak HATA DEĞİL kanıttır (ok=true): çipin arkasında
		// aranan kimlik ve neden bulunamamış olabileceği duruyor.
		emitGuidedStepResult(emit, nTrace, "trace", ev, nil)
		return ev, "trace araması (bulunamadı)", nil
	}
	if err != nil {
		emitGuidedStepResult(emit, nTrace, "trace", "", err)
		return "", "", err
	}
	emitGuidedStepResult(emit, nTrace, "trace", in.User, nil)
	return in.User, fmt.Sprintf("trace %s — span kanıtı + ilişkili loglar", traceID), nil
}

// guidedSpanBundle — v0.9.548. Çıplak SPAN id'sinin kanıt paketi.
//
// Span tek başına anlamsızdır: neyin parçası olduğu bilinmeden "bu span
// neden yavaş" cevaplanamaz. O yüzden önce trace'i bulunur, sonra
// v0.9.537'nin trace paketi AYNEN kullanılır — ikinci bir montaj yok.
// Anlatıma hangi span'in sorulduğu ayrıca söylenir ki model waterfall
// içinde onu işaret edebilsin.
//
// Pencere çağırandan gelir (sohbetin aralığı, v0.9.529): span_id'de
// indeks yok, arama zaman-sınırlı bir tarama. Sınırsız pencere
// prod'da pahalıya kaçardı.
func (s *Server) guidedSpanBundle(ctx context.Context, emit func(string, any), spanID string, from, to time.Time) (string, string, error) {
	nSpan := emitGuidedStep(emit, "span", `{"id":"`+spanID+`"}`)
	traceID, err := s.store.FindTraceIDBySpan(ctx, spanID, from, to)
	if err != nil {
		emitGuidedStepResult(emit, nSpan, "span", "", err)
		return "", "", err
	}
	if traceID == "" {
		// Bulunamamak HATA DEĞİL dürüst kanıt. Hata döndürsek soru
		// serbest döngüye düşer ve operatör yine cevapsız kalır.
		ev := fmt.Sprintf("Span %s bu zaman penceresinde BULUNAMADI.\n"+
			"Olası nedenler: ID eksik/yanlış kopyalandı, span sohbetin zaman "+
			"aralığının dışında (aralığı genişletmeyi öner), ya da retention "+
			"penceresinin dışına düştü. Kullanıcıya bunu söyle; span/istatistik "+
			"UYDURMA.", spanID)
		emitGuidedStepResult(emit, nSpan, "span", ev, nil)
		return ev, "span araması (bulunamadı)", nil
	}
	in, terr := s.buildTraceExplainInput(ctx, traceID)
	if errors.Is(terr, errExplainTraceNotFound) {
		ev := fmt.Sprintf("Span %s trace %s'e ait ama trace'in span'leri okunamadı "+
			"(retention ya da kısmi yazım). Kullanıcıya bunu söyle; UYDURMA.", spanID, traceID)
		emitGuidedStepResult(emit, nSpan, "span", ev, nil)
		return ev, "span → trace (span'ler okunamadı)", nil
	}
	if terr != nil {
		emitGuidedStepResult(emit, nSpan, "span", "", terr)
		return "", "", terr
	}
	// Hangi span'in sorulduğu anlatımın ÖNÜNE yazılıyor: model
	// waterfall içinde onu işaret edebilsin.
	ev := fmt.Sprintf("SORULAN SPAN: %s (trace %s içinde)\n\n%s", spanID, traceID, in.User)
	emitGuidedStepResult(emit, nSpan, "span", ev, nil)
	return ev, fmt.Sprintf("span %s → trace %s kanıtı", spanID, traceID), nil
}

// guidedRequestIDBundle — v0.9.1142. YAPILANDIRILMIŞ kurumsal istek
// numarasının kanıt paketi.
//
// Zincirin tamamı: kimlik → gömülü tarih+saat (yerel banka saati) →
// o pencerede TEK log araması → eşleşen kaydın trace_id'si →
// v0.9.537'nin trace kanıt paketi (buildTraceExplainInput). Montaj
// guidedSpanBundle'ın AYNISI, çünkü ikisi de aynı şeyi yapıyor: elde
// olan kimliği trace'e çevirip mevcut anlatıyı beslemek.
//
// PENCERE NEDEN SOHBETİN ARALIĞI DEĞİL: kimlik kendi damgasını taşıyor.
// Sohbetin 30dk'lık varsayılanı dün öğlen üretilmiş bir kimliği asla
// bulamazdı; ±10dk'lık dar pencere ise hem doğru hem ucuz (ES 10B
// doc/gün — pencere maliyetin TEK sınırı). Tz belirsizliğini pencereyi
// genişleterek çözmüyoruz: locu doğru kullanıyoruz (reqid.Location).
//
// Bulunamamak HATA DEĞİL dürüst kanıttır: cevap ARANAN PENCEREYİ ve
// ARANAN YERİ söyler, halüsinasyon yasaktır (guided prompt zaten
// uydurmayı yasaklıyor, kanıt da açıkça talimat veriyor).
func (s *Server) guidedRequestIDBundle(ctx context.Context, emit func(string, any), route *guidedRoute) (string, string, error) {
	loc := reqid.Location(s.reqidTZSetting(ctx))
	id, ok := reqid.Parse(route.RequestID, loc)
	if !ok {
		// Router aynı ayrıştırıcıyla yönlendirdiği için buraya normalde
		// düşülmez; düşülürse kanıt dürüst kalır (panik/uydurma yok).
		return fmt.Sprintf("Request ID %s yapılandırılmış biçime uymadı, gömülü zaman okunamadı. "+
			"Kullanıcıya bunu söyle ve kimliği log arayüzünde aramasını öner; veri UYDURMA.",
			route.RequestID), "request_id (biçim çözülemedi)", nil
	}
	from, to := id.Window()
	route.ReqWindowFromMs = from.UnixMilli()
	route.ReqWindowToMs = to.UnixMilli()

	nParse := emitGuidedStep(emit, "parse_request_id", fmt.Sprintf(`{"time":%q,"tz":%q}`, reqid.FmtLocal(id.TS), id.TS.Location().String()))
	nSearch := emitGuidedStep(emit, "search_logs", fmt.Sprintf(`{"request_id":%q,"from":%q,"to":%q}`,
		id.Raw, reqid.FmtLocal(from), reqid.FmtLocal(to)))

	backend := "log"
	if s.logs != nil {
		backend = s.logs.Backend()
	}
	// Kimliğin kendisi kanıtın BAŞINDA: model hangi işlemi anlattığını
	// (kanal, müşteri, saat) söyleyebilsin.
	//
	// v0.9.1229 — bu blok AYRIŞTIRMA adımının kanıtı, o yüzden log
	// aramasından ÖNCE kuruluyor: arama düşse bile operatör kimlikten
	// nelerin okunduğunu (ve hangi pencerenin tarandığını) görebilmeli.
	head := fmt.Sprintf("SORULAN REQUEST ID: %s\n"+
		"Kimlikten okunan: işlem zamanı %s · fonksiyon %s · kanal %s · alt kod %s · müşteri no %s\n"+
		"Aranan pencere: %s → %s (kimliğin damgası ±10dk), aranan yer: LOG kayıtları (%s backend).\n",
		id.Raw, reqid.FmtLocal(id.TS), id.FuncCode, id.Channel, id.SubCode, id.CustomerNo,
		reqid.FmtLocal(from), reqid.FmtLocal(to), backend)
	emitGuidedStepResult(emit, nParse, "parse_request_id", head, nil)

	res, err := reqid.Resolve(ctx, s.logs, id)
	if err != nil {
		emitGuidedStepResult(emit, nSearch, "search_logs", "", err)
		return "", "", err
	}
	if res.Partial {
		head += "UYARI: log backend'i KISMİ cevap döndürdü (soft timeout / eksik shard) — " +
			"aşağıdaki sonuç gerçek cevabın alt kümesi olabilir.\n"
	}

	if res.TraceID == "" {
		body := fmt.Sprintf(
			"\nBu pencerede bu kimliği taşıyan, trace bağlamı olan bir log kaydı BULUNAMADI "+
				"(eşleşen log satırı: %d).\n"+
				"Olası nedenler: kimlik eksik/yanlış kopyalandı; kimliği loglayan bileşen trace "+
				"üretmiyor; kimlik log GÖVDESİNDE değil yapısal bir alanda duruyor (serbest metin "+
				"araması gövdeyi tarar); ya da kayıtlar retention penceresinin dışına düştü.\n"+
				"Kullanıcıya BUNU ve aranan pencereyi söyle; trace/span/süre UYDURMA. "+
				"Varsa dış log köprüsü linkini kullanmasını öner.", res.MatchedLogs)
		emitGuidedStepResult(emit, nSearch, "search_logs", body, nil)
		return head + body, fmt.Sprintf("request_id → log araması (%s → %s, bulunamadı)",
			reqid.FmtLocal(from), reqid.FmtLocal(to)), nil
	}

	route.TraceID = res.TraceID
	// Servis rotanın ÇIKTISI (TeamServices emsali): takip çipleri ve derin
	// linkler böylece somut bir servise oturuyor — operatör "peki bu
	// servisin hata logları?" diye devam edebilsin.
	route.Service = res.Service
	in, terr := s.buildTraceExplainInput(ctx, res.TraceID)
	if errors.Is(terr, errExplainTraceNotFound) {
		body := fmt.Sprintf("\nKimlik trace %s ile eşleşti (log servisi: %s) ama trace'in "+
			"span'leri okunamadı (retention ya da kısmi yazım). Kullanıcıya bunu söyle; UYDURMA.",
			res.TraceID, res.Service)
		emitGuidedStepResult(emit, nSearch, "search_logs", body, nil)
		return head + body, "request_id → trace (span'ler okunamadı)", nil
	}
	if terr != nil {
		emitGuidedStepResult(emit, nSearch, "search_logs", "", terr)
		return "", "", terr
	}
	match := fmt.Sprintf("\nEşleşen log kaydı: servis %s · trace %s · span %s (eşleşen satır: %d).\n",
		res.Service, res.TraceID, res.SpanID, res.MatchedLogs)
	ev := head + match
	if res.DistinctTraces > 1 {
		// Aynı kimlik yeniden deneme / asenkron devam yüzünden birden çok
		// trace'e dokunmuş olabilir; tek trace gibi anlatmak yanlış olurdu.
		note := fmt.Sprintf("NOT: bu pencerede kimliği taşıyan %d FARKLI trace var; "+
			"aşağıdaki en yenisi. Bunu söyle.\n", res.DistinctTraces)
		match += note
		ev += note
	}
	// Arama adımının kanıtı EŞLEŞMEnin kendisi; altındaki trace paketi
	// ayrı bir okumadan geliyor (kendi çipi yok, kanıtı da burada değil).
	emitGuidedStepResult(emit, nSearch, "search_logs", match, nil)
	ev += "\n" + in.User
	return ev, fmt.Sprintf("request_id → log araması → trace %s kanıtı", res.TraceID), nil
}

// guidedDeployRef unifies the two deploy reads (global
// RecentDeployEntry vs per-service Deploy) for the renderer.
type guidedDeployRef struct {
	Service string
	Version string
	TimeNs  int64
}

// (d) "deploy etkisi/son deploy" → recent rollouts + before/after RED
// impact (ComputeDeployImpact, single bounded CH pass per deploy,
// capped at 3). Deploy markers carry NO env dimension (env-separation
// Phase 4 pending) — an env ask (v0.8.398) is answered honestly via
// guidedEnvlessNoteTR instead of silently ignored; the step echo also
// omits env because the filter was not applied.
// anchorTo (v0.10.64) — pencerenin SONU. Sıfır = şimdi.
//
// ⚠ v0.10.33 çıpayı guided kademesine getirdi ama BU fonksiyona hiç
// geçirmedi: pencere `time.Now()` ile kuruluyordu. Operatör dün geceye
// zoom yapıp "son deploy neydi" diye sorduğunda BUGÜNÜN deploy'ları
// dönüyor ve cevap DÜNÜN penceresi diye etiketleniyordu — v0.10.50'de
// serbest döngüde düzeltilen kusurun guided'daki ikizi.
// guidedDeployMinLookback — deploy sorusunun en kısa geriye bakışı.
const guidedDeployMinLookback = 24 * time.Hour

func (s *Server) guidedDeployBundle(ctx context.Context, emit func(string, any), service, env string, rangeS int64, anchorTo time.Time) (string, string, error) {
	// Deploy questions imply a wider horizon than the default 30m chat
	// window — "son deploy" is rarely in the last half hour. Floor the
	// lookback at 6h, cap 24h (GetRecentDeploys scales its CH timeout
	// with the window).
	// v0.10.1250 (operatör: "24 saati search et") — taban 6 sa → 24 sa:
	// 12 saatlik sohbet penceresinde "son deploy" sabahki deploy'u kaçırıyordu.
	lookback := time.Duration(rangeS) * time.Second
	if lookback < guidedDeployMinLookback {
		lookback = guidedDeployMinLookback
	}
	var refs []guidedDeployRef
	nRecent := emitGuidedStep(emit, "recent_deploys", `{"service":"`+service+`"}`)
	if service != "" {
		now := anchorTo
		if now.IsZero() {
			now = time.Now()
		}
		deps, err := s.store.GetServiceDeploys(ctx, service, now.Add(-lookback), now)
		if err != nil {
			emitGuidedStepResult(emit, nRecent, "recent_deploys", "", err)
			return "", "", err
		}
		for _, d := range deps {
			refs = append(refs, guidedDeployRef{Service: service, Version: d.Version, TimeNs: d.TimeUnixNs})
		}
	} else {
		deps, err := s.store.GetRecentDeploys(ctx, lookback, 10)
		if err != nil {
			emitGuidedStepResult(emit, nRecent, "recent_deploys", "", err)
			return "", "", err
		}
		for _, d := range deps {
			refs = append(refs, guidedDeployRef{Service: d.Service, Version: d.Version, TimeNs: d.FirstSeenNs})
		}
	}
	// Newest first, impact for the top 3 only (bounded CH cost).
	sort.Slice(refs, func(i, j int) bool { return refs[i].TimeNs > refs[j].TimeNs })
	if len(refs) > 5 {
		refs = refs[:5]
	}
	// v0.9.1229 — işaretçi okumasının kanıtı: etki HESAPLANMADAN önceki
	// liste, aynı renderer'la. Her etki adımı kendi satırını ayrıca
	// gösteriyor, yani hangi çipin ne getirdiği ayrışıyor.
	emitGuidedStepResult(emit, nRecent, "recent_deploys",
		renderDeployEvidenceTR(refs, nil, lookback, evidenceAsOf(anchorTo)), nil)
	impacts := make([]*chstore.DeployImpact, len(refs))
	for i, ref := range refs {
		if i >= 3 {
			break
		}
		nImp := emitGuidedStep(emit, "deploy_impact", `{"service":"`+ref.Service+`","version":"`+ref.Version+`"}`)
		imp, ierr := s.store.ComputeDeployImpact(ctx, ref.Service, ref.Version, ref.TimeNs, 600)
		if ierr == nil {
			impacts[i] = imp
		}
		emitGuidedStepResult(emit, nImp, "deploy_impact",
			renderDeployEvidenceTR(refs[i:i+1], impacts[i:i+1], lookback, evidenceAsOf(anchorTo)), ierr)
	}
	src := "deploy işaretçileri + öncesi/sonrası RED etkisi (±10dk pencere)"
	if env != "" {
		src += "; ortam filtresi uygulanamadı (deploy verisi ortam boyutu taşımıyor)"
	}
	return guidedEnvlessNoteTR("deploy işaretçileri", env) +
		renderDeployEvidenceTR(refs, impacts, lookback, evidenceAsOf(anchorTo)), src, nil
}

// (e) "log hataları/log errors [service]" → severity histogram totals
// + the curated failure-pattern detector hits (both reads carry the
// existing ES/CH cost guards; the pattern window snaps to the same
// rungs the /anomalies endpoint uses). Logs carry NO env dimension
// (env-separation Phase 4 pending) — an env ask (v0.8.398) is
// answered honestly via guidedEnvlessNoteTR instead of silently
// ignored; the step echo omits env because the filter was not applied.
func (s *Server) guidedLogErrorsBundle(ctx context.Context, emit func(string, any), service, env string, from, to time.Time, rangeS int64) (string, string, error) {
	nHist := emitGuidedStep(emit, "log_severity_histogram", `{"service":"`+service+`"}`)
	bucketSec := int(rangeS / 30)
	if bucketSec < 60 {
		bucketSec = 60
	}
	series, err := s.logs.Histogram(ctx, logstore.Filter{Service: service, From: from, To: to}, bucketSec, "severity")
	if err != nil {
		emitGuidedStepResult(emit, nHist, "log_severity_histogram", "", err)
		return "", "", err
	}
	nPat := emitGuidedStep(emit, "log_patterns", "")
	pats, perr := s.guidedServicePatterns(ctx, service, rangeS) // v0.10.557 — ortak yardımcı
	// v0.10.1213 — asıl kanıt: en yeni ~200 hata satırının şablon grupları.
	nSamp := emitGuidedStep(emit, "recent_error_logs", `{"service":"`+service+`","limit":200}`)
	recs, fb, serr := s.guidedRecentErrorSample(ctx, service, from, to)
	sample := ""
	if serr == nil {
		sample = renderLogSampleTR(recs, fb, service)
	}
	emitGuidedStepResult(emit, nSamp, "recent_error_logs", sample, serr)
	nTpl := emitGuidedStep(emit, "new_log_templates", `{"service":"`+service+`"}`)
	tpls, tsvcs, terr := s.guidedNewLogTemplates(ctx, service, recs, from, to)
	if terr == nil {
		sample += renderNewLogTemplatesTR(tpls, tsvcs, rangeS)
	}
	emitGuidedStepResult(emit, nTpl, "new_log_templates", renderNewLogTemplatesTR(tpls, tsvcs, rangeS), terr)
	src := fmt.Sprintf("son %d hata logu örneklemi + yeni log şablonları + severity histogramı + hata pattern tespitleri (son %s)", guidedLogSampleN, fmtAgoTR(rangeS))
	if env != "" {
		src += "; ortam filtresi uygulanamadı (log verisi ortam boyutu taşımıyor)"
	}
	ev := guidedEnvlessNoteTR("log verisi", env) + sample +
		renderLogErrorsEvidenceTR(series, pats, service, rangeS)
	// v0.9.1229 — iki okuma TEK bloğa akıyor (renderer severity dağılımını
	// ve pattern eşleşmelerini birlikte kuruyor), o yüzden blok iki çipe de
	// veriliyor: yarısını uydurmaktansa aynı kanıtı iki kez göstermek.
	// Pattern okuması yumuşak-düşer (pats=nil) — çip bunu ok=false ile SÖYLER.
	emitGuidedStepResult(emit, nHist, "log_severity_histogram", ev, nil)
	emitGuidedStepResult(emit, nPat, "log_patterns", ev, perr)
	return ev, src, nil
}

// ─── Evidence renderers (pure, table-tested) ────────────────────────

// guidedServicePatterns — v0.10.557: servise dokunan (ana ya da topServices'te)
// yeni/patlayan log desenleri, sayıya göre azalan, en çok 5. Desenler EK kanıt:
// dedektör hatası yumuşak (nil, err) döner. Örneklem tabanlı (Drain), tam tarama
// yok; pencere basamaklı (snapAnomalyWindow).
func (s *Server) guidedServicePatterns(ctx context.Context, service string, rangeS int64) ([]anomaly.LogPatternAnomaly, error) {
	pats, perr := anomaly.DetectLogPatterns(ctx, s.logs, snapAnomalyWindow(time.Duration(rangeS)*time.Second))
	if perr != nil {
		return nil, perr
	}
	pats = filterGuidedPatterns(pats, service, 5)
	return pats, nil
}

// filterGuidedPatterns — SAF yarı (test pinli).
func filterGuidedPatterns(pats []anomaly.LogPatternAnomaly, service string, max int) []anomaly.LogPatternAnomaly {
	if service != "" {
		kept := make([]anomaly.LogPatternAnomaly, 0, len(pats))
		for _, p := range pats {
			if p.Service == service {
				kept = append(kept, p)
				continue
			}
			for _, ts := range p.TopServices {
				if ts.Service == service {
					kept = append(kept, p)
					break
				}
			}
		}
		pats = kept
	}
	sort.SliceStable(pats, func(i, j int) bool { return pats[i].CurrentCount > pats[j].CurrentCount })
	if len(pats) > max {
		pats = pats[:max]
	}
	return pats
}

// renderLogPatternsTR — v0.10.557: kök-neden rotasının desen satırları (histogramsız).
func renderLogPatternsTR(pats []anomaly.LogPatternAnomaly, service string, rangeS int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Log desenleri (son %s, %s): ", fmtAgoTR(rangeS), service)
	if len(pats) == 0 {
		b.WriteString("yeni ya da patlayan desen yok (örneklem).\n")
		return b.String()
	}
	b.WriteString("\n")
	for _, p := range pats {
		fmt.Fprintf(&b, "- %s ×%d (%s", p.Pattern, p.CurrentCount, p.Kind)
		if p.BaselineCount > 0 {
			fmt.Fprintf(&b, ", baseline %d, ×%.1f", p.BaselineCount, p.Ratio)
		}
		b.WriteString(")\n")
	}
	return b.String()
}

// guidedEvidencePayload — v0.10.557: kök-neden rotasının YAPISAL kanıt bloğu
// (event: block, type evidence). Anlatım metninin ikizi değil, FE kartının verisi:
// RED şimdi/taban, açık problemler + hipotez, penceredeki değişiklikler, log
// desenleri; hipotez yoksa verdict bunu söyler. SAF (test pinli), listeler tavanlı.
//
// v0.10.944 — okunamayan RED penceresi sıfır DEĞİL: current/baseline yerine
// currentUnavailable/baselineUnavailable (hata sınıfı) gider — FE kartı sıfırı
// ölçüm diye çiziyordu. Problem okuması düştüyse (probErr) verdict "açık
// problem yok" DEMEZ.
func guidedEvidencePayload(service string, rangeS int64, cx *aiServiceContext, probs []chstore.Problem, probErr error, changes []mcptools.ChangeRow, pats []anomaly.LogPatternAnomaly) map[string]any {
	out := map[string]any{"question": "root_cause", "service": service, "rangeS": rangeS}
	if cx != nil {
		red := func(r aiRED) map[string]any {
			return map[string]any{"spans": r.Spans, "rate": r.Rate, "errorRate": r.ErrorRate, "p95Ms": r.P95Ms, "p99Ms": r.P99Ms}
		}
		redM := map[string]any{}
		if cx.curErr != nil {
			redM["currentUnavailable"] = string(sourcestate.Classify(cx.curErr))
		} else {
			redM["current"] = red(cx.Current)
		}
		if cx.baseErr != nil {
			redM["baselineUnavailable"] = string(sourcestate.Classify(cx.baseErr))
		} else {
			redM["baseline"] = red(cx.Baseline)
		}
		out["red"] = redM
	}
	pl := make([]map[string]any, 0, len(probs))
	for i, p := range probs {
		if i >= 5 {
			break
		}
		row := map[string]any{"id": p.ID, "ruleName": p.RuleName, "severity": p.Severity, "status": p.Status, "startedAt": p.StartedAt, "metric": p.Metric, "value": p.Value, "threshold": p.Threshold}
		if p.RootCause != nil {
			row["topSuspect"], row["confidence"] = p.RootCause.TopSuspect, p.RootCause.Confidence
		}
		pl = append(pl, row)
	}
	out["problems"] = pl
	cl := make([]map[string]any, 0, len(changes))
	for i, c := range changes {
		if i >= 8 {
			break
		}
		cl = append(cl, map[string]any{"source": c.Source, "timeUnixNs": c.TimeUnixNs, "workload": c.Workload, "service": c.Service, "version": c.Version, "status": c.Status, "namespace": c.Namespace})
	}
	out["changes"] = cl
	ll := make([]map[string]any, 0, len(pats))
	for _, p := range pats {
		ll = append(ll, map[string]any{"pattern": p.Pattern, "kind": p.Kind, "currentCount": p.CurrentCount, "baselineCount": p.BaselineCount, "ratio": p.Ratio, "service": p.Service})
	}
	out["logPatterns"] = ll
	verdict := "hipotez yok — açık problem yok ya da korelatör henüz hesaplamadı"
	if probErr != nil {
		verdict = fmt.Sprintf("problem kayıtları okunamadı (%s) — hipotez durumu bilinmiyor", sourcestate.Classify(probErr))
	} else {
		for _, p := range probs {
			if p.RootCause != nil && p.RootCause.TopSuspect != "" {
				verdict = fmt.Sprintf("kök-neden şüphelisi %s (güven %.2f)", p.RootCause.TopSuspect, p.RootCause.Confidence)
				break
			}
		}
	}
	out["verdict"] = verdict
	return out
}

const guidedMaxLines = 10

// guidedScopeTR renders the "(servis: X, ortam: Y)" scope fragment
// shared by the problems + slow-traces evidence headers (v0.8.398).
// Pure — table-tested. Empty parts drop out; both empty = "".
func guidedScopeTR(service, env string) string {
	var parts []string
	if service != "" {
		parts = append(parts, "servis: "+service)
	}
	if env != "" {
		parts = append(parts, "ortam: "+env)
	}
	if len(parts) == 0 {
		return ""
	}
	return ", " + strings.Join(parts, ", ")
}

func renderProblemsEvidenceTR(probs []chstore.Problem, service, env string, now time.Time, total problemsTotal) string {
	scope := ""
	if sc := guidedScopeTR(service, env); sc != "" {
		scope = " (" + sc[2:] + ")" // strip the leading ", " — header form
	}
	if len(probs) == 0 {
		return "Açık problem yok" + scope + ".\n"
	}
	var crit, warn, info int
	for _, p := range probs {
		switch p.Severity {
		case "critical":
			crit++
		case "warning":
			warn++
		default:
			info++
		}
	}
	var b strings.Builder
	// v0.10.21 — TOPLAM ARTIK len(probs) DEĞİL. `probs` bir SQL LIMIT'inin
	// çıktısı; uzunluğunu "toplam" diye basmak, 47 problemli bir serviste
	// modele "toplam 10" vermek demekti (guided_problem_total.go).
	// Şiddet sayımları GÖSTERİLEN satırlar üzerinden. Liste kırpılmamışsa
	// bu zaten tam sayımdır ve fazladan açıklama gürültü olur; kırpıldıysa
	// "toplam 47 (kritik 1, warning 1, info 0)" kendi içinde çelişir, o
	// yüzden ifşa ŞART.
	fmt.Fprintf(&b, "Açık problemler%s: %s (kritik %d, warning %d, info %d%s)\n",
		scope, problemsCountPhraseTR(total, len(probs)), crit, warn, info,
		problemsBreakdownCaveatTR(total, len(probs)))
	shown := len(probs)
	if shown > guidedMaxLines {
		shown = guidedMaxLines
	}
	for i, p := range probs {
		if i >= guidedMaxLines {
			break
		}
		name := p.RuleName
		if name == "" {
			name = p.Metric
		} else if p.Metric != "" && p.Metric != p.RuleName {
			// v0.10.405 (CoSRE denetimi P4) — kural adı varken METRİK adı
			// düşüyordu; model "değer 4.20"nin neyin değeri olduğunu bilmiyordu.
			name += " [" + p.Metric + "]"
		}
		unit := problemMetricUnitTR(p.Metric)
		fmt.Fprintf(&b, "- [%s] %s — %s (%s, %s önce): değer %.2f%s / eşik %.2f%s",
			p.Priority, p.Service, name, p.Severity,
			fmtAgoTR(now.UnixNano()/1e9-p.StartedAt/1e9), p.Value, unit, p.Threshold, unit)
		if p.RootCause != nil && p.RootCause.TopSuspect != "" {
			fmt.Fprintf(&b, " | kök-neden şüphelisi: %s (güven %.2f)",
				p.RootCause.TopSuspect, p.RootCause.Confidence)
		}
		if p.PriorityReason != "" {
			fmt.Fprintf(&b, " | öncelik nedeni: %s", p.PriorityReason)
		}
		b.WriteString("\n")
	}
	// v0.10.21 — KIRPMA İFŞASI. Eski dal `i >= guidedMaxLines` idi ve
	// limit=10 rotalarında YAPISAL OLARAK ULAŞILAMAZDI (döngü 0..9, indeks
	// hiç 10'a çıkmaz), yani model yanlış toplamı üstüne hiçbir uyarı
	// almadan alıyordu. İfşa artık GÖSTERİLEN ile TOPLAM farkından çıkıyor.
	b.WriteString(problemsTruncationNoteTR(total, shown, len(probs)))
	return b.String()
}

func renderSlowTracesEvidenceTR(rows []chstore.TraceRow, service, env string, rangeS int64) string {
	scope := guidedScopeTR(service, env)
	if len(rows) == 0 {
		return fmt.Sprintf("En yavaş trace'ler (son %s%s): bu pencerede trace bulunamadı.\n", fmtAgoTR(rangeS), scope)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "En yavaş trace'ler (son %s%s, duration'a göre):\n", fmtAgoTR(rangeS), scope)
	for _, r := range rows {
		flag := ""
		if r.HasError {
			flag = ", HATA"
		}
		fmt.Fprintf(&b, "- %.0fms — %s / %s (%d span%s) trace=%s\n",
			r.DurationMs, r.ServiceName, r.RootName, r.SpanCount, flag, r.TraceID)
	}
	return b.String()
}

func renderDeployEvidenceTR(refs []guidedDeployRef, impacts []*chstore.DeployImpact, lookback time.Duration, now time.Time) string {
	if len(refs) == 0 {
		return fmt.Sprintf("Son %s içinde deploy görülmedi.\n", fmtAgoTR(int64(lookback.Seconds())))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Son deploylar (son %s):\n", fmtAgoTR(int64(lookback.Seconds())))
	for i, ref := range refs {
		fmt.Fprintf(&b, "- %s %s (%s önce)", ref.Service, ref.Version,
			fmtAgoTR(now.UnixNano()/1e9-ref.TimeNs/1e9))
		if i < len(impacts) && impacts[i] != nil {
			imp := impacts[i]
			fmt.Fprintf(&b, " | etki (±10dk): p99 %.0fms→%.0fms (%%%+.1f), error %%%.2f→%%%.2f, rps %.1f→%.1f",
				imp.Before.P99Ms, imp.After.P99Ms, imp.P99DeltaPct,
				imp.Before.ErrorRate*100, imp.After.ErrorRate*100,
				imp.Before.RPS, imp.After.RPS)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// guidedSeverityOrder pins the histogram render order worst-first so
// the model reads FATAL/ERROR before the INFO noise.
var guidedSeverityOrder = []string{"FATAL", "ERROR", "WARN", "INFO", "DEBUG", "TRACE"}

func renderLogErrorsEvidenceTR(series []logstore.LogSeries, pats []anomaly.LogPatternAnomaly, service string, rangeS int64) string {
	scope := ""
	if service != "" {
		scope = fmt.Sprintf(", servis: %s", service)
	}
	totals := map[string]int64{}
	var grand int64
	for _, s := range series {
		for _, p := range s.Points {
			totals[s.Name] += p.V
			grand += p.V
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Log severity dağılımı (son %s%s): ", fmtAgoTR(rangeS), scope)
	if grand == 0 {
		b.WriteString("bu pencerede log yok.\n")
	} else {
		var parts []string
		seen := map[string]bool{}
		for _, name := range guidedSeverityOrder {
			if v, ok := totals[name]; ok && v > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", name, v))
				seen[name] = true
			}
		}
		// Non-canonical band names (backend-specific) trail, sorted for
		// deterministic output.
		var rest []string
		for name, v := range totals {
			if !seen[name] && v > 0 {
				rest = append(rest, fmt.Sprintf("%s %d", name, v))
			}
		}
		sort.Strings(rest)
		parts = append(parts, rest...)
		b.WriteString(strings.Join(parts, ", "))
		fmt.Fprintf(&b, " (toplam %d)\n", grand)
	}
	if len(pats) > 0 {
		b.WriteString("Öne çıkan hata pattern'leri:\n")
		for _, p := range pats {
			fmt.Fprintf(&b, "- %s ×%d (%s, %s", p.Pattern, p.CurrentCount, p.Service, p.Kind)
			if p.BaselineCount > 0 {
				fmt.Fprintf(&b, ", baseline %d", p.BaselineCount)
			}
			b.WriteString(")\n")
		}
	} else {
		b.WriteString("Bilinen hata pattern'lerinde eşleşme yok.\n")
	}
	return b.String()
}

// containsPhrase — çok kelimeli bir kalıbı KELİME SINIRINDA arar
// (v0.9.570).
//
// Sınırsız strings.Contains, Türkçede sessiz bir yanlış-yönlendirme
// üretiyordu ve çarpışma tam da en doğal SRE sorusundaydı:
//
//	"bu trace neden yavaş?" ⊃ "en yavaş"   →  ned|EN YAVAŞ
//	"neden uzun sürdü"      ⊃ "en uzun"    →  ned|EN UZUN sürdü
//
// Yani operatör /trace sayfasında ekrandaki trace'i sorarken router
// onu "en yavaş trace'leri listele" niyetine yönlendiriyor ve FİLO
// GENELİ bir liste dönüyordu. Hata görünmez: cevap makul bir cevap,
// yalnız SORULAN soruya ait değil.
//
// Sınır tanımı: kalıbın YALNIZ BAŞINDA harf/rakam olmamalı. Son ek
// serbest — ve bu bilinçli bir asimetri:
//
//   - Bug BAŞTAYDI: "ned|en yavaş" kalıbı bir kelimenin ortasında
//     yakalıyordu.
//   - SONDA ek dayatmak yanlış olurdu: "slow trace" → "slow traceS"
//     (İngilizce çoğul) ve "en yavaş" → "en yavaşI", "yavaşLAR"
//     (Türkçe eklemeli yapı). İki uçta da sınır isteyen ilk taslağım
//     mevcut testleri düşürdü ve haklı olarak düşürdü.
//
// Türkçe harfler unicode.IsLetter ile doğru ele alınır — ASCII testi
// ş/ğ/ı'yı sınır sanardı ve kalıbın kendisini bölerdi.
func containsPhrase(msg, phrase string) bool {
	if phrase == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(msg[i:], phrase)
		if j < 0 {
			return false
		}
		start := i + j
		if !alnumBefore(msg, start) {
			return true
		}
		// Kayarak devam et: aynı kalıp ilerde SINIRDA geçebilir.
		i = start + 1
		if i >= len(msg) {
			return false
		}
	}
}

func alnumBefore(s string, idx int) bool {
	if idx <= 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:idx])
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// hasDemonstrativeTrace — mesaj EKRANDAKİ trace'i mi işaret ediyor?
//
// "bu trace", "şu trace", "this trace" gibi işaret zamirli kullanımlar
// bir LİSTE değil, TEK bir şey sorar. Bu ayrım olmadan "bu trace neden
// yavaş?" sorusu "en yavaş trace'leri listele" niyetiyle çarpışıyor ve
// operatör ekranındaki trace yerine filo geneli bir liste görüyordu.
func hasDemonstrativeTrace(msg string) bool {
	for _, p := range []string{"bu trace", "şu trace", "this trace", "bu iz"} {
		if containsPhrase(msg, p) {
			return true
		}
	}
	return false
}

// guidedNarrationPrompt — intent'e göre anlatım sistem prompt'u (saf,
// tablo testli; v0.9.1131, operatör-raporlu).
//
// Rapor: "✨ Explain trace ile chat'e 'bu trace'i açıkla' demek farklı
// davranıyor; Explain daha iyi." Kanıt paketi v0.9.537'den beri AYNI
// (guidedTraceBundle → buildTraceExplainInput), fark yalnız
// anlatıcıdaydı: guided yol HER intent'i jenerik sohbet prompt'uyla
// anlatıyordu; ✨ Explain ise v0.9.842'nin 3-bölümlü derin trace
// prompt'unu kullanıyor. Trace/span intent'leri artık explain'in
// prompt'unu birebir kullanır — iki kapı, aynı kanıt, AYNI anlatı.
// Diğer intent'ler sohbet formatında kalır (tablo+takip akışına o
// uyuyor).
func guidedNarrationPrompt(intent guidedIntent) string {
	switch intent {
	// v0.9.1142 — request_id rotasının kanıtı da (çözüldüğünde) trace
	// paketidir; anlatıcı aynı olmalı, yoksa aynı kanıt iki kapıdan iki
	// farklı derinlikte anlatılırdı (v0.9.1131'in tam olarak bu kazası).
	case guidedTraceByID, guidedSpanByID, guidedRequestID:
		return copilot.SystemPromptTrace()
	case guidedServiceReport: // v0.10.1337 — sabit altı bölümlü rapor anlatıcısı
		return copilot.SystemPromptServiceReport()
	case guidedResourceInvestigation: // v0.10.1431 — SAYILAR / HİPOTEZ / SONRAKİ ADIM; sayı yeniden hesaplanmaz
		return copilot.SystemPromptResourceInvestigation()
	}
	return copilot.SystemPromptGuidedChat()
}

// isSelfMetaQuestion — soru asistanın KENDİSİ hakkında mı?
//
// v0.10.13, operatör bildirimi: "sen hangi modelsin" sorusu hiçbir
// intent'e uymuyor, RAG doküman yoluna düşüyor ve orada "Yüklü
// dokümanlarda bu bilgi yok" cevabı alıyordu — oysa cevap
// yapılandırmada duruyor.
//
// Kapı BİRLEŞİM, tek kelime değil (guidedQueueLag emsali): tek başına
// "model" telemetri sorularında da geçebilir ("model servisinin p99'u"),
// tek başına "sen" ise her cümlede olabilir. İkisi birlikte niyeti
// belirliyor. `kimsin`/`nesin` gibi tek kelimeler kendi başına yeterli,
// çünkü onların telemetri karşılığı yok.
func isSelfMetaQuestion(toks []string) bool {
	// Tek başına yeterli olanlar: bunlar bir servisi ya da metriği
	// adlandıramaz.
	if tokenHasPrefix(toks, "kimsin", "nesin", "kimsiniz") {
		return true
	}
	// İngilizce kalıp İFADE olarak aranıyor, token birleşimi olarak
	// DEĞİL: "who/what" + "you" birleşimi "what services do you have"
	// gibi gerçek telemetri sorularını da yakalardı ve düzeltme yeni bir
	// kusur üretirdi. Dar kalıp, dar eşleşme.
	joined := strings.Join(toks, " ")
	if strings.Contains(joined, "who are you") || strings.Contains(joined, "what are you") {
		return true
	}
	// Birleşim: özne (sen/siz/hangi/which/who) + kimlik (model/asistan/...)
	subject := tokenHasPrefix(toks, "sen", "senin", "siz", "hangi", "which", "who", "what")
	identity := tokenHasPrefix(toks, "model", "modeli", "modelsin", "asistan", "assistant", "llm", "yapay")
	return subject && identity
}

// selfMetaAnswerTR — asistanın kendisi hakkındaki soru; LLM'siz.
//
// Cevap YAPILANDIRMADA yazılı: bir anlatıcıya "model adını harfi harfine
// kopyala" demek yerine metni sunucu kurar — küçük modelin adı tanınmış bir
// markayla değiştirme riski kalmaz. Model adı sohbet yüzeyinin ÇÖZÜLEN
// profilinden gelir (WithProfile > yüzey haritası > grup kardeşi >
// varsayılan), anlatım çağrısının kullanacağı zincirin aynısı.
//
// Model adı sır DEĞİL (operatör Helm values'ına kendi yazıyor); ModelFor
// yalnız modeli döndürür, baseURL/apiKey'i değil.
func (s *Server) selfMetaAnswerTR(ctx context.Context) string {
	model := ""
	if s.copilot != nil {
		m := copilot.MetaFromContext(ctx)
		m.Surface = "chat-guided"
		model = s.copilot.ModelFor(copilot.WithMeta(ctx, m))
	}
	if model == "" {
		return "AI asistanı bu kurulumda YAPILANDIRILMAMIŞ (model seçilmemiş " +
			"ya da sağlayıcı kapalı). Ayarlar → AI bölümünden yapılandırılır."
	}
	return "Ben CoSRE: Coremetry'nin içine gömülü SRE asistanıyım; telemetriyi " +
		"(trace, log, metrik, problem) okuyup anlatırım. Bu kurulumda çalışan model: `" + model + "`."
}

// evidenceAsOf — "kaç saat önce" hesabının DAYANAĞI (v0.10.65).
//
// Çıpalı bir pencerede yaşları GERÇEK şimdiye göre yazmak, doğru veriye
// yanlış etiket koymaktır: dün gece 03:00'teki bir deploy "2 saat önce"
// değil, ÇAPAYA göre öyle. Aynı şey problem yaşları için de geçerli —
// "4 saattir açık" cümlesi çıpalı pencerede saatlerce kayıyordu.
//
// Tek yardımcı, çünkü kural tek: kanıtın yaşı, kanıtın PENCERESİNE göre.
// Çıpa yoksa davranış aynen eskisi.
func evidenceAsOf(anchorTo time.Time) time.Time {
	if anchorTo.IsZero() {
		return time.Now()
	}
	return anchorTo
}

// problemMetricUnitTR — v0.10.405 (CoSRE denetimi P4): problem kanıt
// satırındaki sayının BİRİMİ; tek kaynak chstore.ProblemMetricUnit (arka
// plan açıklayıcısı da aynı birimi yazsın diye oraya taşındı).
func problemMetricUnitTR(metric string) string { return chstore.ProblemMetricUnit(metric) }

// serviceIntentFor — v0.10.429 (D1): mesaj servis-kapsamlı bir kılavuz
// niyet taşıyor mu ve hangisi (router switch'inin servisli dallarıyla AYNI
// öncelik). guidedNone ⇒ servis sorusu değil, aday aranmaz.
func serviceIntentFor(msg string, toks []string) guidedIntent {
	switch {
	case hasSweepSignal(toks): // v0.10.1276 — router'daki sıra (tarama why/pod'dan önce)
		return guidedServiceHealth
	case serviceReportShape(toks): // v0.10.1337 — router'daki sıra (rapor taramadan sonra, why'dan önce)
		return guidedServiceReport
	case hasWhySignal(toks):
		return guidedRootCause
	case hasSlowTraceSignal(msg):
		return guidedSlowTraces
	case hasDeploySignal(toks):
		return guidedDeployImpact
	case hasLogSignal(toks) && hasErrorSignal(toks):
		return guidedLogErrors
	case hasPodSignal(toks):
		return guidedPodHealth
	case hasMessagingSignal(toks):
		return guidedMessagingHealth
	case hasDBSignal(toks):
		return guidedDBHealth
	case hasHealthSignal(toks) || hasErrorSignal(toks) || hasProblemSignal(toks):
		return guidedServiceHealth
	}
	return guidedNone
}

// guidedAskServiceEvidence — v0.10.429 (D1): guidedAskTeamEvidence'in
// servis ikizi. Adaylar rotada yoksa (sınıflandırıcı "servis gerekli ama
// yok" dedi) açık problemi olan servisler sunulur; o da yoksa katalogun
// ilk adları. Çipler guidedSuggestions'ta tam kılavuz cümle olur.
func (s *Server) guidedAskServiceEvidence(ctx context.Context, route *guidedRoute, question string) (string, string, error) {
	src := "canlı servis kataloğu (ad eşleşmedi)"
	if len(route.ServiceOptions) == 0 {
		src = "açık problemli servisler"
		seen := map[string]bool{}
		if probs, err := s.store.ListProblems(ctx, chstore.ProblemFilter{Status: "open"}); err == nil {
			for _, p := range probs {
				if p.Service == "" || seen[p.Service] {
					continue
				}
				seen[p.Service] = true
				route.ServiceOptions = append(route.ServiceOptions, p.Service)
				if len(route.ServiceOptions) >= guidedServiceAskMax {
					break
				}
			}
		}
		if len(route.ServiceOptions) == 0 {
			src = "servis kataloğu"
			names := s.guidedServiceNames(ctx)
			if len(names) > guidedServiceAskMax {
				names = names[:guidedServiceAskMax]
			}
			route.ServiceOptions = append(route.ServiceOptions, names...)
		}
	}
	if route.AskIntent == guidedNone {
		route.AskIntent = guidedServiceHealth
	}
	var b strings.Builder
	b.WriteString("Operatörün sorusu bir SERVİS gerektiriyor ama adı çözülemedi ya da birden çok servise oturuyor.\n")
	fmt.Fprintf(&b, "Soru: %q\n", strings.TrimSpace(question))
	route.DirectAnswer = askServiceAnswerTR(route.ServiceOptions) // anlatım çağrısı yok
	if len(route.ServiceOptions) == 0 {
		b.WriteString("Katalogda aday yok. KULLANICIYA SÖYLE: servis adını yazsın (Servisler sayfasından kopyalayabilir).\n")
		return b.String(), src, nil
	}
	fmt.Fprintf(&b, "KULLANICIYA SOR: şu servislerden hangisini kastettin? (%d aday): %s\n", len(route.ServiceOptions), strings.Join(route.ServiceOptions, ", "))
	b.WriteString("Bu adlar cevabın altında ÇİP olarak duruyor — tıklaması yeter; listede yoksa adı yazabileceğini de söyle.\n")
	b.WriteString("KURAL: servis adı UYDURMA, yalnız yukarıdaki listeyi say; veri anlatma (henüz seçilmedi).\n")
	return b.String(), src, nil
}

// guidedWindowCompareBundle — v0.10.437 (D6): aynı servisin iki mutlak
// penceredeki RED'i (service_summary_5m; v0.10.944 — env verilmiş ve
// kapsıyorsa service_env_summary_5m). Konum rotadaki pencerelerin kendi
// konumu (tarayıcı ofseti).
func (s *Server) guidedWindowCompareBundle(ctx context.Context, emit func(string, any), route *guidedRoute) (string, string, error) {
	if len(route.Windows) != 2 || route.Service == "" {
		return "", "", fmt.Errorf("window_compare: iki pencere ve servis gerekli")
	}
	loc := route.Windows[0].From.Location()
	// v0.10.944 — ortam: service_summary_5m deploy_env taşımaz, route.Env
	// yok sayılıyordu (prod + uat aynı adla birleşiyordu). env verilmiş ve
	// service_env_summary_5m İKİ pencerenin en erken başını kapsıyorsa ikisi
	// de ortamlı okunur; değilse ikisi de birleşik + dürüst not. Bir pencere
	// ortamlı, diğeri birleşik OKUNMAZ.
	earliest := route.Windows[0].From
	if route.Windows[1].From.Before(earliest) {
		earliest = route.Windows[1].From
	}
	scoped := route.Env != "" && s.store.EnvSummaryCovers(ctx, earliest)
	reds := make([]aiRED, 0, 2)
	for i, w := range route.Windows {
		// v0.10.444 — yalnız RED (service_summary_5m): buildServiceContext
		// pencere başına 8 okuma yapıyordu (exception ham tarama, komşu
		// örnekleme, deploy…) ve 7'si atılıyordu — 24 saatlik iki pencere
		// için iki tam-gün ham spans taraması.
		// v0.10.944 — pencere başına TEK satır: p50/p95/p99 kovaların tdigest
		// durumlarının birleşimi (ServiceWindowRED). Eskisi 5 dk kovaları
		// çekip yüzdeliklerini span ağırlıklı ortalıyordu (aggRED).
		stepArgs := fmt.Sprintf(`{"service":%q,"window":%q}`, route.Service, absWindowLabel(w, loc))
		var wr chstore.ServiceWindowRED
		var err error
		if scoped {
			stepArgs = withEnvArg(stepArgs, route.Env) // yalnız UYGULANAN süzgeç çipte görünür
		}
		n := emitGuidedStep(emit, "service_red", stepArgs)
		if scoped {
			wr, err = s.store.ServiceEnvWindowRED(ctx, route.Service, route.Env, w.From, w.To)
		} else {
			wr, err = s.store.ServiceWindowRED(ctx, route.Service, w.From, w.To)
		}
		if err != nil {
			emitGuidedStepResult(emit, n, "service_red", "", err)
			return "", "", err
		}
		red := windowRED(wr, w.To.Sub(w.From).Seconds())
		reds = append(reds, red)
		emitGuidedStepResult(emit, n, "service_red", fmt.Sprintf("pencere %d: %d span", i+1, red.Spans), nil)
	}
	ev, src := windowCompareEvidenceTR(route.Service, route.Env, scoped, route.Windows, reds, loc)
	return ev, src, nil
}

// errorRankNames — SAF: sıralı adlar (takip çipleri).
func errorRankNames(rows []chstore.ErrorRank) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}
