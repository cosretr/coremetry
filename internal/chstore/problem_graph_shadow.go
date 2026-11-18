package chstore

// problem_graph_shadow.go — v0.10.2274 (entity grafı ile Problem birleştirme,
// dilim S1; tasarım docs/problems/entity-graph-merge-design.md §2, §7, §8, §9).
//
// "Bağla, kapatma" modelinin GÖLGE ölçümü: evaluator lideri her tikte açık
// Problem'ler arasında graf kurallarının KURACAĞI bağları hesaplar; hiçbir
// Problem, sahip, üye, durum, öncelik, bildirim, liste satırı ya da CoSRE
// girdisi değişmez.
//
// SAKLAMA — şema değişmez: sonuç `system_settings` tablosunda tek blob
// (anahtar `problem_graph_shadow`), ML tik durumu (`ml_tick_status`)
// emsali — mevcut kolona yeni bir değer. Tasarımın önerdiği `ml_events`
// (`detector = member_graph`) satırları S1'de KULLANILMADI: ml_events'i
// okuyan anomali listeleri, birleşik akış, CoSRE araçları ve Problem üyelik
// okuyucuları yeni dedektörü görürdü (S1 "hiçbir şey değişmez" kuralı).
// Blob tavanlı (ProblemGraphBlobMax): ayar anlık görüntüsü her pod'da 30 sn'de
// bir bütün tabloyu okur.
//
// Kural kimlikleri SÖZLEŞMEDİR (arayüz kural kimliğinden cümle kurar).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const problemGraphShadowKey = "problem_graph_shadow"

// ProblemGraphBlobMax — blob tavanı (bayt); aşılırsa en küçük kayıtlar düşer.
const ProblemGraphBlobMax = 256 << 10

// Bağ kuralları (birleşme gerekçesi) ve bağlanmama nedenleri — kalıcı kimlik.
const (
	GraphRuleSvc  = "M-SVC"  // aynı servis (Faz 1 kuralı, değişmez)
	GraphRuleCall = "M-CALL" // A calls B, tek adım
	GraphRuleDep  = "M-DEP"  // ortak bağımlılık (kendi Problem'i yok), iki üyede bağımlılık olayı
	GraphRuleWL   = "M-WL"   // aynı iş yükü iki servisi sunuyor (serves)
	GraphRuleNode = "M-NODE" // etkilenen pod'lar aynı node'da
	GraphRuleHost = "M-HOST" // aynı K8s dışı host

	GraphNoRel   = "N-REL"   // graf ilişkisi yok
	GraphNoTime  = "N-TIME"  // 30 dk pencere dışı
	GraphNoCap   = "N-CAP"   // emme sınırı (S1: yalnız ölçülür, uygulanmaz)
	GraphNoHub   = "N-HUB"   // geniş ortak nokta, kendi sinyali yok
	GraphNoKind  = "N-KIND"  // bağlanmayan aile
	GraphNoSize  = "N-SIZE"  // üye tavanı (ve sahipten 3 bağ uzaklık)
	GraphNoGraph = "N-GRAPH" // graf verisi yok ya da eski
)

// ProblemGraphMember — birleşik Problem'in bir üyesi (bir Problem satırı).
type ProblemGraphMember struct {
	ProblemID string `json:"id"`
	Entity    string `json:"entity"` // entity kimliği (svc:…, db:…)
	Type      string `json:"type"`   // service | database | queue | external
	Name      string `json:"name"`
	RuleID    string `json:"ruleId,omitempty"`
	Title     string `json:"title,omitempty"`
	Severity  string `json:"severity,omitempty"`
	StartedAt int64  `json:"startedAt"` // unix ns
	Owner     bool   `json:"owner,omitempty"`
	// Link — bu üyeyi kümeye bağlayan kural (sahipte boş).
	Link string `json:"link,omitempty"`
	// Peer — bağlandığı komşu üyenin adı; Via — ortak varlığın adı
	// (iş yükü / bağımlılık / node / host); Path — graftaki yol (adlar).
	Peer   string   `json:"peer,omitempty"`
	Via    string   `json:"via,omitempty"`
	Path   []string `json:"path,omitempty"`
	Strict bool     `json:"strict,omitempty"` // M-CALL: çağıranda hedefe ait bağımlılık olayı var
	// GraphMissing — varlığın graf verisi yok: yalnız aynı servis kuralı.
	GraphMissing bool `json:"graphMissing,omitempty"`
	// Late — v0.10.2341 (S4, yalnız açıkken): aynı servisin emme sınırından
	// (90 dk) sonra başlayan satırı — "geç katıldı"; ikinci Problem yok.
	Late bool `json:"late,omitempty"`
}

// ProblemGraphNearMiss — aynı pencerede açık olup bağlanmayan Problem.
type ProblemGraphNearMiss struct {
	ProblemID string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	StartedAt int64  `json:"startedAt"`
	Reason    string `json:"reason"`         // N-*
	Hint      string `json:"hint,omitempty"` // ilişki vardı ama kuralın ek koşulu sağlanmadı (M-*)
	Peer      string `json:"peer,omitempty"` // karşılaştırılan üyenin adı
	Via       string `json:"via,omitempty"`  // ortak varlık (N-HUB / ipucu)
	// GroupSize — karşı tarafın kendi kümesindeki varlık sayısı (1 = tek).
	GroupSize int `json:"groupSize,omitempty"`
}

// Köken kuralları (S2, tasarım §3) — sıralı; ilk ayıran kazanır, puan yok.
// Kimlikler SÖZLEŞMEDİR (arayüz cümleyi kimlikten kurar).
const (
	OriginRuleRollout = "K-ROLLOUT" // başlangıçtan önceki 30 dk'da iş yükünde rollout, en erkenlerden
	OriginRuleShared  = "K-SHARED"  // ≥ 2 üyenin bağlı olduğu, kendi sinyali olan ortak varlık
	OriginRuleDeep    = "K-DEEP"    // çağrı zincirinin en derin halkası
	OriginRuleFirst   = "K-FIRST"   // en erken başlangıç (5 dk içi eşit)

	OriginStrong  = "strong"  // kazanan ≥ 2 kuralda önde ("güçlü")
	OriginWeak    = "weak"    // tek kural ("zayıf")
	OriginUnclear = "unclear" // hiçbir kural ayırmadı ("köken belirsiz")

	// Kanıt kaynağı kimlikleri.
	OriginSrcRollout = "rollout"    // workload_rollouts
	OriginSrcCalls   = "calls"      // entity_relations calls
	OriginSrcDepEv   = "dep_events" // ml_events bağımlılık olayları
	OriginSrcOnset   = "onset"      // problems.started_at
)

// ProblemGraphOriginRule — bir köken kuralının sonucu (kural sırasıyla).
type ProblemGraphOriginRule struct {
	Rule      string `json:"rule"`
	Separated bool   `json:"separated"`
	// Off — kuralın verisi bu tur yok (okuma hatası / bayat): ayırmadı sayılır.
	Off    bool   `json:"off,omitempty"`
	Leader string `json:"leader,omitempty"` // önde olan varlığın adı
	// Others — K-SHARED: lidere bağlı üyeler; K-ROLLOUT / K-DEEP / K-FIRST eşitlerde: eşit adaylar.
	Others []string `json:"others,omitempty"`
	Via    string   `json:"via,omitempty"`    // K-ROLLOUT: iş yükü adı
	At     int64    `json:"at,omitempty"`     // K-ROLLOUT: rollout başlangıcı; K-FIRST: liderin başlangıcı (unix ns)
	Runner string   `json:"runner,omitempty"` // K-FIRST: ikinci en erken
	RunAt  int64    `json:"runAt,omitempty"`  // K-FIRST: ikincinin başlangıcı
	Source string   `json:"source"`
}

// ProblemGraphBlast — kökenin etki alanı (called_by, 2 adım, adım başına ≤ 100).
type ProblemGraphBlast struct {
	Affected  []string `json:"affected"`            // ölçülmüş sapması olan üyeler (köken hariç)
	AtRisk1   []string `json:"atRisk1,omitempty"`   // kökeni çağırıyor, sapma yok (ilk 8)
	AtRisk1N  int      `json:"atRisk1N"`            // toplam
	AtRisk2   []string `json:"atRisk2,omitempty"`   // etkilenenleri / ilk adımı çağırıyor, sapma yok (ilk 8)
	AtRisk2N  int      `json:"atRisk2N"`            // toplam
	Cut       bool     `json:"cut,omitempty"`       // adım tavanı (100) ya da LIMIT'e değdi
	Off       bool     `json:"off,omitempty"`       // called_by okunamadı / calls bayat
	NamesDrop bool     `json:"namesDrop,omitempty"` // blob tavanı: risk adları düşürüldü, sayılar duruyor
}

// ProblemGraphOrigin — kümenin olası kökeni (S2; salt okunur gölge).
type ProblemGraphOrigin struct {
	Entity     string                   `json:"entity,omitempty"` // belirsizde boş
	Name       string                   `json:"name,omitempty"`
	Type       string                   `json:"type,omitempty"`
	Confidence string                   `json:"confidence"`
	Rule       string                   `json:"rule,omitempty"` // kazanan (ilk ayıran) kural
	Leads      []string                 `json:"leads,omitempty"`
	OnsetAt    int64                    `json:"onsetAt,omitempty"` // kökenin ilk sapması (kendi Problem'i yoksa 0)
	Earliest   string                   `json:"earliest"`          // en erken başlayan üye
	EarliestAt int64                    `json:"earliestAt"`
	Rules      []ProblemGraphOriginRule `json:"rules"`
	Blast      *ProblemGraphBlast       `json:"blast,omitempty"`
}

// ProblemGraphGroup — bir birleşik Problem (gölge) ya da bağlanmayanları
// olan tek Problem. OwnerID = en erken açılan üyenin Problem kimliği.
type ProblemGraphGroup struct {
	OwnerID    string                 `json:"ownerId"`
	Entities   int                    `json:"entities"`
	Members    []ProblemGraphMember   `json:"members"`
	NearMisses []ProblemGraphNearMiss `json:"nearMisses,omitempty"`
	Truncated  bool                   `json:"truncated,omitempty"` // üye satırı tavanı (25)
	// Origin — S2: olası köken + etki alanı (yalnız ≥ 2 varlıklı kümede).
	Origin *ProblemGraphOrigin `json:"origin,omitempty"`
	// Onset — v0.10.2341 (S4, yalnız açıkken): kümenin hatırlanan en erken
	// başlangıcı (unix ns); emme sınırı bundan ölçülür.
	Onset int64 `json:"onset,omitempty"`
}

// ProblemGraphReads — okuma ölçümü (lider başlangıcından beri).
type ProblemGraphReads struct {
	Passes        int64 `json:"passes"`
	SkippedFew    int64 `json:"skippedFew"`    // aday < 2: ek okuma yok
	Refreshes     int64 `json:"refreshes"`     // önbellek ıskası → okuma turu
	Relations     int64 `json:"relations"`     // entity_relations okuması
	MaxRelPerPass int   `json:"maxRelPerPass"` // tek turdaki en çok entity_relations okuması
	Runs          int64 `json:"runs"`          // entity_sync_runs tazelik okuması
	Evidence      int64 `json:"evidence"`      // ml_events bağımlılık olayı okuması
	Errors        int64 `json:"errors"`
	Rollouts      int64 `json:"rollouts"` // S2: workload_rollouts okuması (K-ROLLOUT)
	Blast         int64 `json:"blast"`    // S2: called_by okuması (etki alanı, tur başına ≤ 2)
}

// ProblemGraphShadow — gölge raporu + o anki kümeler (detay sayfası okur).
type ProblemGraphShadow struct {
	At      int64 `json:"at"`    // unix sn
	Since   int64 `json:"since"` // sayaçların başladığı an (lider değişince sıfırlanır)
	WindowH int   `json:"windowH"`
	Units   int   `json:"units"` // bu tikte aday varlık sayısı
	// Links — kural başına FARKLI bağ (pencerede); M-CALL düz sürüm.
	Links map[string]int64 `json:"links,omitempty"`
	// CallStrict — M-CALL bağlarından katı sürümü de sağlayanlar.
	CallStrict int64 `json:"callStrict"`
	// NoLink — neden başına bağlanmayan FARKLI Problem.
	NoLink map[string]int64 `json:"noLink,omitempty"`
	// Sizes / SizesStrict — küme boyu (varlık) dağılımı; düz / katı M-CALL.
	Sizes       map[string]int64 `json:"sizes,omitempty"`
	SizesStrict map[string]int64 `json:"sizesStrict,omitempty"`
	// Age — servisler arası bağ kurulurken sahibin yaşı (dk kovası), kural başına.
	Age       map[string]int64            `json:"age,omitempty"`
	AgeByRule map[string]map[string]int64 `json:"ageByRule,omitempty"`
	// GraphMissing — graf verisi olmadığı için bağ aranmayan FARKLI Problem.
	GraphMissing int64 `json:"graphMissing"`
	// FamilyOff — kural ailesi kapalı geçen okuma turu (bayat / okunamadı).
	FamilyOff map[string]int64 `json:"familyOff,omitempty"`
	// OriginWins — S2: kazanan köken kuralı başına FARKLI küme (24 sa);
	// OriginUnclear — kökeni belirsiz küme; OriginConf — güven dağılımı.
	OriginWins    map[string]int64  `json:"originWins,omitempty"`
	OriginUnclear int64             `json:"originUnclear"`
	OriginConf    map[string]int64  `json:"originConf,omitempty"`
	Reads         ProblemGraphReads `json:"reads"`
	// Frequent — v0.10.2280 (S3): sık tekrar eden AÇIK Problem'lerin gölge
	// kararı (Problem kimliği → karar; ≤ 200). FrequentReport — 24 sa sayaçları.
	Frequent       map[string]FrequentVerdict `json:"frequent,omitempty"`
	FrequentReport *FrequentReport            `json:"frequentReport,omitempty"`
	// MergeReport — v0.10.2341 (S4): sonradan bağlanan üyelerin 24 sa
	// sayaçları (geçiş ölçütü); her iki kipte dolar.
	MergeReport *ProblemGraphMergeReport `json:"mergeReport,omitempty"`
	Groups      []ProblemGraphGroup      `json:"groups,omitempty"`
	Truncated   bool                     `json:"truncated,omitempty"` // blob tavanı
}

// GetProblemGraphShadow — son blob (yoksa ok=false).
func (s *Store) GetProblemGraphShadow(ctx context.Context) (ProblemGraphShadow, bool, error) {
	raw, err := s.GetSetting(ctx, problemGraphShadowKey)
	if err != nil || len(raw) == 0 {
		return ProblemGraphShadow{}, false, err
	}
	var r ProblemGraphShadow
	if err := json.Unmarshal(raw, &r); err != nil {
		return ProblemGraphShadow{}, false, nil
	}
	return r, true, nil
}

// SaveProblemGraphShadow — blobu yazar (tavan çağıranda: FitProblemGraphBlob).
func (s *Store) SaveProblemGraphShadow(ctx context.Context, r ProblemGraphShadow) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, problemGraphShadowKey, raw)
}

// FitProblemGraphBlob — SAF: blob tavanı aşılırsa ÖNCE etki alanının risk
// adı listeleri düşer (sayılar kalır, NamesDrop), sonra sık tekrar
// kararlarının bölüm listeleri, hâlâ aşıyorsa SONDAKİ
// kümeler düşer (çağıran önemliyi öne dizer); Truncated işaretlenir. Dönüş
// JSON boyu.
func FitProblemGraphBlob(r *ProblemGraphShadow, max int) int {
	if raw, err := json.Marshal(r); err == nil && len(raw) > max {
		for i := range r.Groups {
			if o := r.Groups[i].Origin; o != nil && o.Blast != nil && (len(o.Blast.AtRisk1)+len(o.Blast.AtRisk2)) > 0 {
				b := *o.Blast
				b.AtRisk1, b.AtRisk2, b.NamesDrop = nil, nil, true
				oo := *o
				oo.Blast = &b
				r.Groups[i].Origin = &oo
			}
		}
	}
	// v0.10.2280 (S3): ikinci olarak sık tekrar kararlarının bölüm listeleri
	// düşer (sayılar kalır).
	if raw, err := json.Marshal(r); err == nil && len(raw) > max && len(r.Frequent) > 0 {
		m := make(map[string]FrequentVerdict, len(r.Frequent))
		for id, v := range r.Frequent {
			v.Episodes = nil
			m[id] = v
		}
		r.Frequent = m
	}
	for {
		raw, err := json.Marshal(r)
		if err != nil {
			return 0
		}
		if len(raw) <= max || len(r.Groups) == 0 {
			return len(raw)
		}
		drop := len(r.Groups) / 4
		if drop < 1 {
			drop = 1
		}
		r.Groups = r.Groups[:len(r.Groups)-drop]
		r.Truncated = true
	}
}

// FindProblemGraphGroup — SAF: Problem'in kümesi (üye ya da tek başına
// bağlanmayanları olan). Yoksa ok=false.
func FindProblemGraphGroup(r ProblemGraphShadow, problemID string) (ProblemGraphGroup, bool) {
	for _, g := range r.Groups {
		for _, m := range g.Members {
			if m.ProblemID == problemID {
				return g, true
			}
		}
	}
	return ProblemGraphGroup{}, false
}

// EntityLastRunAt — sahte cluster_id'li geçişin (ör. `(topology)` = calls)
// son başarılı koşusu; since'ten eski ya da yoksa sıfır zaman.
func (s *Store) EntityLastRunAt(ctx context.Context, cid string, since time.Time) (time.Time, error) {
	var t time.Time
	row := s.conn.QueryRow(ctx, `
		SELECT max(started_at) FROM entity_sync_runs
		WHERE cluster_id = ? AND status IN ('ok', 'partial') AND started_at >= ?
		SETTINGS max_execution_time = 5`, cid, since)
	if err := row.Scan(&t); err != nil {
		return time.Time{}, fmt.Errorf("entity last run %s: %w", cid, err)
	}
	if t.Before(since) {
		return time.Time{}, nil
	}
	return t, nil
}

// GraphDependencyEvent — bağımlılık olayının (servis, hedef) çifti.
type GraphDependencyEvent struct {
	Service string
	Target  string // ml Code "fam|target" → target
}

// ProblemGraphDependencyEvents — aday servislerin son pencerede görülen
// bağımlılık olayları (dependency_latency / dependency_error_rate). Sınırlı:
// servis ≤ 500, last_seen alt sınırı, LIMIT, max_execution_time.
func (s *Store) ProblemGraphDependencyEvents(ctx context.Context, services []string, since time.Time, limit int) ([]GraphDependencyEvent, error) {
	if len(services) == 0 {
		return nil, nil
	}
	if len(services) > 500 {
		services = services[:500]
	}
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	rows, err := s.conn.Query(ctx, `
		SELECT DISTINCT service, code FROM ml_events
		WHERE detector IN ('dependency_latency', 'dependency_error_rate')
		  AND service IN (?) AND last_seen >= ?
		LIMIT `+fmt.Sprint(limit)+`
		SETTINGS max_execution_time = 10`, services, since)
	if err != nil {
		return nil, fmt.Errorf("graph dependency events: %w", err)
	}
	defer rows.Close()
	var out []GraphDependencyEvent
	for rows.Next() {
		var svc, code string
		if err := rows.Scan(&svc, &code); err != nil {
			return nil, err
		}
		_, tgt, ok := strings.Cut(code, "|")
		if !ok {
			tgt = code
		}
		if tgt != "" {
			out = append(out, GraphDependencyEvent{Service: svc, Target: tgt})
		}
	}
	return out, rows.Err()
}
