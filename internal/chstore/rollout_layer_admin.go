package chstore

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/migrations"
)

// rollout_layer_admin.go — 0012 ROLLOUTS KATMANI ŞEMASI SİHİRBAZI (v0.10.197,
// rollouts audit §5(j); entity_layer_admin.go'nun aynası).
//
//   Durum     host başına kolon / index / tablo / MV / Distributed var-yok
//             (clusterAllReplicas — dağıtık DDL'in yarım kaldığı host görünür)
//   Ön kontrol system.clusters + önerilen ad, spans_local var mı, 0011 kolonları
//             (cluster/k8s_namespace) var mı, replicaset + image KAPSAMA (son
//             15 dk, cluster kırılımlı — 2026-08-30 dersi: bir cluster tam set,
//             öteki namespace bile yok) ve LowCardinality kapısı (uniq RS adı /
//             imaj adı ≤ 100k)
//   Uygula    gömülü 0012, `uptrace_all` token'ı gerçek küme adıyla, ifade
//             ifade, İLK HATADA DUR (IF NOT EXISTS → yeniden basmak güvenli).
//             withMV=false → ADIM 6 (MV + sarmalayıcı) ATLANIR: kapsama kapısı
//             geçmeden MV'yi prod'a alma (audit R1) — Faz 1a/1b ayrımı.
//   Geri al   YALNIZ MV + sarmalayıcı — yazımı keser, kolon/tablo/veri KALIR.
//
// Boot'ta ASLA koşmaz; tek tetikleyici admin düğmesi (ev kuralı v0.9.613).
//
// v0.10.960 — 0015 ROLLOUTS v2 STATE TABLOLARI (P1.8; docs/rollouts/v2-audit.md
// §10.5, kararlar 6 / 9 / 24 / 25). YENİ SİHİRBAZ DEĞİL, bu kartın uzantısı:
//
//   Durum     nesne listesinin sonuna sekiz tablo (kind table, §10.3 sırası,
//             rollout_v2_schema.go'dan türer) → aynı clusterAllReplicas
//             (system.tables) probe'u host başına VAR / KISMİ / YOK der.
//   Ön kontrol (0015) system.clusters + istenen küme orada mı, spans_local,
//             sekiz tablonun host başına motoru (system.tables) ve ZK yolu
//             (system.replicas). REDDEDER: (a) bir host'ta Replicated OLMAYAN
//             kopya — dış Distributed + ALLOW_UNSET_CLUSTER boot'unun ilk
//             host'a kurduğu düz tablo; 0015'in IF NOT EXISTS'i orada no-op
//             kalırdı; (b) sabit yoldan FARKLI ZK yolu — eksik host'lar 0015
//             ile AYRI replikasyon grubuna katılırdı (split-brain); (c) küme
//             kipinde özel önek (cfg.ReplicaPath) — 0015'in sabit yolu o
//             önekte BİRLEŞİK sayılmaz: tabloyu tutmayan bir host'a boot
//             (kural 2) `<önek>/{shard}/<ad>` kurar (ayrı grup), kart onları
//             "eski" listeler, sihirbaz bu önekte sekizi yeniden kuramaz.
//             v0.10.971 — boot'un kural 3'ü kalktı: hiç var olmayan sekiz
//             tabloyu boot bu önekte zaten '<önek>/state/<ad>' yoluna kurar,
//             0015 orada gereksizdir. (Eski gerekçe — "kuşak probe'unu bozar,
//             sonraki state tablolarını shard'lı yola düşürür" — kural 3 ile
//             birlikte geçersiz.)
//   Uygula    gömülü 0015, `uptrace_all` → gerçek küme adı, ifade ifade, İLK
//             HATADA DUR (IF NOT EXISTS → yeniden basmak güvenli). 0012'nin
//             apply'ından AYRI yol ve kapı: 0011 / kapsama / LC kapıları state
//             tablolarıyla ilgisiz, onlara takılmamalı.
//   Geri al   gömülü 0015 rollback — sekiz tablo ters sırada DROP … SYNC,
//             ilk hatada DURMAZ (IF EXISTS). VERİ GİDER; argocd_sync_events
//             tarihçesi geri gelmez (Argo yalnız son 10 kaydı tutar). 0012'nin
//             MV-yalnız geri alması DEĞİŞMEDİ.
//
// ZK yolu (karar 25): 0015 '/clickhouse/tables/state/<ad>' SABİT yazar (0012
// gibi); boot öneki ve yolu çalışma zamanında çözer (state_replication.go
// zkPrefix + useUnifiedStatePath). Varsayılan önekte, tablo hiçbir node'da
// yokken ikisi AYNI ifadeyi üretir (rollout_layer_admin_test.go pinler;
// v0.10.971'dan beri kümede başka eski state tabloları olsa da); ayrıştığı
// durumları yukarıdaki (b)/(c) reddi yakalar.

type RolloutLayerObject = EntityLayerObject
type RolloutLayerObjectStatus = EntityLayerObjectStatus

// RolloutLayerObjects — 0012'nin yarattığı her nesne + (v0.10.960) 0015'in
// sekiz state tablosu (test pinler; 0012 bölümü önde ve değişmedi).
func RolloutLayerObjects() []RolloutLayerObject {
	return append(rolloutLayer0012Objects(), rolloutV2LayerObjects()...)
}

func rolloutLayer0012Objects() []RolloutLayerObject {
	return []RolloutLayerObject{
		{Name: "k8s_deployment", Kind: "column", Table: "spans_local"},
		{Name: "k8s_statefulset", Kind: "column", Table: "spans_local"},
		{Name: "k8s_daemonset", Kind: "column", Table: "spans_local"},
		{Name: "k8s_replicaset", Kind: "column", Table: "spans_local"},
		{Name: "container_image", Kind: "column", Table: "spans_local"},
		{Name: "container_image_tag", Kind: "column", Table: "spans_local"},
		{Name: "idx_k8s_namespace", Kind: "index", Table: "spans_local"},
		{Name: "idx_k8s_deployment", Kind: "index", Table: "spans_local"},
		{Name: "idx_k8s_statefulset", Kind: "index", Table: "spans_local"},
		{Name: "idx_k8s_daemonset", Kind: "index", Table: "spans_local"},
		{Name: "idx_k8s_replicaset", Kind: "index", Table: "spans_local"},
		{Name: "idx_container_image", Kind: "index", Table: "spans_local"},
		{Name: "idx_container_image_tag", Kind: "index", Table: "spans_local"},
		{Name: "workload_rollouts", Kind: "table"},
		{Name: "rollout_reconcile_runs", Kind: "table"},
		{Name: "workload_revision_activity_1m_local", Kind: "mv"},
		{Name: "workload_revision_activity_1m", Kind: "distributed"},
	}
}

// RolloutLayerStatusResult — sihirbaz kartı.
type RolloutLayerStatusResult struct {
	Cluster      string                     `json:"cluster"`
	Objects      []RolloutLayerObjectStatus `json:"objects"`
	ActivityRows uint64                     `json:"activityRows"` // MV son 15 dk (yazıyor mu kanıtı)
	Generated    int64                      `json:"generated"`
}

// RolloutLayerClusterCoverage — bir span cluster değerinin kapsaması (son 15 dk örneklem).
type RolloutLayerClusterCoverage struct {
	Cluster string `json:"cluster"`
	// Total — tam pencere sayımı (LC cluster kolonu, ucuz); Sampled — hash
	// örnekleminde görülen (kapsama oranlarının paydası). Total>0 && Sampled==0
	// = "bu cluster'ı ölçemedim" → kapı KAPALI (inceleme B2).
	Total      uint64  `json:"total"`
	Sampled    uint64  `json:"sampled"`
	ReplicaSet float64 `json:"replicaset"` // 0..1
	Image      float64 `json:"image"`      // 0..1
	Namespace  float64 `json:"namespace"`  // 0..1
}

// RolloutLayerPreflightResult — "bu küme 0012'yi kaldırır mı".
type RolloutLayerPreflightResult struct {
	Clusters         []string `json:"clusters"`
	SuggestedCluster string   `json:"suggestedCluster,omitempty"`
	SpansLocal       bool     `json:"spansLocal"`
	// Layer0011 — cluster + k8s_namespace kolonları var mı (MV onları okur).
	Layer0011 bool `json:"layer0011"`
	// Coverage — span cluster değeri başına (2026-08-30 dersi).
	Coverage []RolloutLayerClusterCoverage `json:"coverage"`
	// MVGate — her cluster'da replicaset kapsaması ≥ %95 → ADIM 6 uygulanabilir.
	MVGate      bool     `json:"mvGate"`
	UniqRS1h    uint64   `json:"uniqRs1h"`
	UniqImage1h uint64   `json:"uniqImage1h"`
	ProbeErrors []string `json:"probeErrors,omitempty"`
	Supported   bool     `json:"supported"`
	Detail      string   `json:"detail"`
	Generated   int64    `json:"generated"`
}

const (
	rolloutLayerFile   = "0012_rollout_layer.sql"
	rolloutLayerLCGate = 100_000
	// rolloutLayerMVGate — MV kapısı: her cluster'da k8s.replicaset.name
	// kapsaması (audit R1, §12 Faz 1b).
	rolloutLayerMVGate = 0.95
)

// rolloutLayerStatements — gömülü 0012, küme adıyla, ifadelere bölünmüş;
// withMV=false → MV + Distributed sarmalayıcı ifadeleri düşer (Faz 1a). Saf.
func rolloutLayerStatements(cluster string, withMV bool) ([]string, error) {
	raw, err := migrations.FS.ReadFile(rolloutLayerFile)
	if err != nil {
		return nil, fmt.Errorf("gömülü %s okunamadı: %w", rolloutLayerFile, err)
	}
	stmts := SplitSQLStatements(AdaptRollupDDL(string(raw), cluster))
	if withMV {
		return stmts, nil
	}
	out := stmts[:0:0]
	for _, s := range stmts {
		if strings.Contains(s, "workload_revision_activity_1m") {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// rolloutLayerRollbackStatements — yalnız MV + sarmalayıcı (saf, testli); SYNC şart.
func rolloutLayerRollbackStatements(cluster string) []string {
	return []string{
		"DROP TABLE IF EXISTS workload_revision_activity_1m ON CLUSTER " + cluster + " SYNC",
		"DROP TABLE IF EXISTS workload_revision_activity_1m_local ON CLUSTER " + cluster + " SYNC",
	}
}

// rolloutLayerMVGateOK — saf: adı olan HER cluster'ın örneklemi var VE RS
// kapsaması eşiğin üstünde. Sampled=0 "bu cluster'ı ölçemedim" demektir ve
// kapıyı KAPATIR (inceleme B2: eski hâli atlıyordu — 2026-08-30 olayının
// ta kendisi); ” satırı (cluster'sız, k8s dışı trafik) kapıya girmez; adı
// olan hiç cluster yoksa false.
func rolloutLayerMVGateOK(cov []RolloutLayerClusterCoverage, gate float64) bool {
	n := 0
	for _, c := range cov {
		if c.Cluster == "" {
			continue
		}
		n++
		if c.Sampled == 0 || c.ReplicaSet < gate {
			return false
		}
	}
	return n > 0
}

// rolloutLayerClusterRe — cluster adı DDL'e (`ON CLUSTER x`) ham giriyor ve
// AdaptRollupDDL ifade bölmeden ÖNCE koşuyor: ';' içeren ad ifade üretirdi
// (inceleme S5). system.clusters adları bu alfabede.
var rolloutLayerClusterRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func validRolloutLayerCluster(c string) bool { return rolloutLayerClusterRe.MatchString(c) }

// RolloutLayerStatus — host başına nesne varlığı (EntityLayerStatus aynası).
func (s *Store) RolloutLayerStatus(ctx context.Context) (RolloutLayerStatusResult, error) {
	out := RolloutLayerStatusResult{Generated: time.Now().Unix(), Objects: []RolloutLayerObjectStatus{}} // [] değil null: FE .map()
	cluster := s.entityLayerCluster(ctx)
	out.Cluster = cluster
	hosts := 1
	colSrc, idxSrc, tblSrc := "system.columns", "system.data_skipping_indices", "system.tables"
	if cluster != "" {
		var n uint64
		if err := s.conn.QueryRow(ctx, `SELECT count() FROM system.clusters WHERE cluster = ?`, cluster).Scan(&n); err == nil && n > 0 {
			hosts = int(n)
		}
		q := func(t string) string { return fmt.Sprintf("clusterAllReplicas('%s', %s)", cluster, t) }
		colSrc, idxSrc, tblSrc = q("system.columns"), q("system.data_skipping_indices"), q("system.tables")
	}
	spansTable := "spans_local"
	if cluster == "" {
		spansTable = "spans"
	}
	count := func(sql string, args ...any) (int, error) {
		var n uint64
		if err := s.conn.QueryRow(ctx, sql+" SETTINGS max_execution_time = 10", args...).Scan(&n); err != nil {
			return 0, err
		}
		return int(n), nil
	}
	for _, o := range RolloutLayerObjects() {
		st := RolloutLayerObjectStatus{EntityLayerObject: o, Hosts: hosts}
		var have int
		var err error
		switch o.Kind {
		case "column":
			have, err = count(`SELECT count() FROM `+colSrc+` WHERE database = currentDatabase() AND table = ? AND name = ?`, spansTable, o.Name)
		case "index":
			have, err = count(`SELECT count() FROM `+idxSrc+` WHERE database = currentDatabase() AND table = ? AND name = ?`, spansTable, o.Name)
		case "table", "mv", "distributed":
			name := o.Name
			if cluster == "" && strings.HasSuffix(name, "_local") {
				// v0.10.208 — tek düğümde `_local` ile sarmalayıcı AYNI tabloya
				// eşlenir; aynı şeyi iki satır "VAR" göstermek yanıltıyordu →
				// `_local` satırı tek düğümde listelenmez (apply/rollback nesne
				// listesi ve küme kipi değişmedi).
				continue
			}
			have, err = count(`SELECT count() FROM `+tblSrc+` WHERE database = currentDatabase() AND name = ?`, name)
		}
		if err != nil {
			st.Err = err.Error()
			st.State = "unknown"
		} else {
			st.HaveHosts = have
			st.State = entityLayerObjectState(have, hosts)
		}
		out.Objects = append(out.Objects, st)
	}
	var rows uint64
	if err := s.conn.QueryRow(ctx, `SELECT count() FROM workload_revision_activity_1m WHERE bucket >= now() - INTERVAL 15 MINUTE SETTINGS max_execution_time = 10`).Scan(&rows); err == nil {
		out.ActivityRows = rows
	}
	return out, nil
}

// RolloutLayerPreflight — hiçbir şey yazmaz.
func (s *Store) RolloutLayerPreflight(ctx context.Context) (RolloutLayerPreflightResult, error) {
	out := RolloutLayerPreflightResult{
		SuggestedCluster: strings.TrimSpace(s.cfg.ClusterName),
		Generated:        time.Now().Unix(),
		Clusters:         []string{}, // null değil (FE .includes)
		Coverage:         []RolloutLayerClusterCoverage{},
	}
	if out.SuggestedCluster == "" {
		out.SuggestedCluster = s.discoverSpansCluster(ctx)
	}
	rows, err := s.conn.Query(ctx, `SELECT DISTINCT cluster FROM system.clusters ORDER BY cluster LIMIT 100`)
	if err != nil {
		out.ProbeErrors = append(out.ProbeErrors, "system.clusters: "+err.Error())
	} else {
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err == nil && c != "" {
				out.Clusters = append(out.Clusters, c)
			}
		}
		rows.Close()
	}
	if ok, err := s.tableExists(ctx, "spans_local"); err != nil {
		out.ProbeErrors = append(out.ProbeErrors, "spans_local: "+err.Error())
	} else {
		out.SpansLocal = ok
	}
	// 0011 kolonları (MV cluster + k8s_namespace okur): spans üzerinde probe.
	_, hasCluster := s.spansColumnExpr(ctx, "cluster")
	_, hasNS := s.spansColumnExpr(ctx, "k8s_namespace")
	out.Layer0011 = hasCluster && hasNS
	// Kapsama — span cluster değeri başına, İKİ sorgu (inceleme B1/B2):
	//   (1) tam pencere sayımı yalnız LC `cluster` kolonuyla (ucuz: 15 dk ×
	//       1 bayt/satır) → hangi cluster'lar VAR (Total);
	//   (2) deterministik hash örneklemi (cityHash64(trace_id) % 50 = 0, %2)
	//       → res_keys kapsaması. Eski hâli baş-örneklemiydi (LIMIT 200000):
	//       (service_name, time) anahtarının ÖNEKİ = alfabetik ilk servisler
	//       → koca bir cluster örnekleme hiç girmez ve kapı AÇILIRDI
	//       ([[feedback-limit-by-is-prefix-sampling]]); üstelik `cluster`
	//       kolonu alt sorguda projekte edilmediği için kolonlu (= hedef)
	//       kurulumda Code 47 ile hep patlıyordu.
	// (1)'de olup (2)'de görünmeyen cluster Sampled=0 ile listelenir ve
	// kapıyı KAPATIR; '' (cluster'sız, k8s dışı) satırı görünür, kapıya
	// girmez. Alias anahtarlar (k8s.container.image.name /
	// kubernetes.namespace.name) terfi kolonuyla aynı coalesce (S9).
	// cluster kolonu yoksa kapsama ölçülmez — Layer0011=false zaten
	// Supported=false.
	if hasCluster {
		type covAgg struct{ sampled, rs, img, ns uint64 }
		samples := map[string]covAgg{}
		covRows, err := s.conn.Query(ctx, `
			SELECT cluster AS c, count() AS sampled,
			       countIf(has(res_keys, 'k8s.replicaset.name')) AS rs,
			       countIf(has(res_keys, 'container.image.name') OR has(res_keys, 'k8s.container.image.name')) AS img,
			       countIf(has(res_keys, 'k8s.namespace.name') OR has(res_keys, 'kubernetes.namespace.name')) AS ns
			FROM spans
			WHERE time >= now() - INTERVAL 15 MINUTE AND time <= now() AND cityHash64(trace_id) % 50 = 0
			GROUP BY c ORDER BY sampled DESC LIMIT 50
			SETTINGS max_execution_time = 15, max_rows_to_read = 50000000, read_overflow_mode = 'break'`)
		if err != nil {
			out.ProbeErrors = append(out.ProbeErrors, "kapsama: "+err.Error())
		} else {
			for covRows.Next() {
				var c string
				var a covAgg
				if err := covRows.Scan(&c, &a.sampled, &a.rs, &a.img, &a.ns); err == nil {
					samples[c] = a
				}
			}
			covRows.Close()
		}
		totRows, err := s.conn.Query(ctx, `
			SELECT cluster AS c, count() AS total FROM spans
			WHERE time >= now() - INTERVAL 15 MINUTE AND time <= now()
			GROUP BY c ORDER BY total DESC LIMIT 50
			SETTINGS max_execution_time = 15`)
		if err != nil {
			out.ProbeErrors = append(out.ProbeErrors, "cluster sayımı: "+err.Error())
		} else {
			for totRows.Next() {
				var c string
				var total uint64
				if err := totRows.Scan(&c, &total); err != nil || total == 0 {
					continue
				}
				a := samples[c]
				row := RolloutLayerClusterCoverage{Cluster: c, Total: total, Sampled: a.sampled}
				if a.sampled > 0 {
					row.ReplicaSet = float64(a.rs) / float64(a.sampled)
					row.Image = float64(a.img) / float64(a.sampled)
					row.Namespace = float64(a.ns) / float64(a.sampled)
				}
				out.Coverage = append(out.Coverage, row)
			}
			totRows.Close()
		}
	}
	out.MVGate = out.Layer0011 && rolloutLayerMVGateOK(out.Coverage, rolloutLayerMVGate)
	// LC kapısı: 1 saatlik pencerede %5 hash örneklemi + okuma tavanı (S6 —
	// sınırsız tarama 20 s'de hata verip sihirbazı kapatıyordu). Örneklem
	// kardinaliteyi hafif küçümser; saatte ≥20 span basan her RS/imaj adı
	// yine görülür, eşik 100k.
	if err := s.conn.QueryRow(ctx, `
		SELECT uniq(res_values[indexOf(res_keys, 'k8s.replicaset.name')]), uniq(res_values[indexOf(res_keys, 'container.image.name')])
		FROM spans WHERE time >= now() - INTERVAL 1 HOUR AND time <= now()
		  AND has(res_keys, 'k8s.replicaset.name') AND cityHash64(trace_id) % 20 = 0
		SETTINGS max_execution_time = 20, max_rows_to_read = 100000000, read_overflow_mode = 'break'`).Scan(&out.UniqRS1h, &out.UniqImage1h); err != nil {
		out.ProbeErrors = append(out.ProbeErrors, "uniq rs/image: "+err.Error())
	}
	switch {
	case len(out.ProbeErrors) > 0:
		out.Detail = "probe hatası — emin olamadığımız kümeye DDL basmıyoruz"
	case !out.SpansLocal:
		out.Detail = "spans_local yok — bu kurulum tek düğüm; 0012 dağıtık şema içindir (uygulama boot'ta kendi kurar)"
	case !out.Layer0011:
		out.Detail = "0011 kolonları (cluster / k8s_namespace) yok — önce K8s entity katmanı (0011)"
	case out.UniqRS1h > rolloutLayerLCGate || out.UniqImage1h > rolloutLayerLCGate:
		out.Detail = fmt.Sprintf("son 1 saatte %d RS adı / %d imaj adı > %d — LowCardinality kapısı; dosyayı düz String'e çevirip elle uygula", out.UniqRS1h, out.UniqImage1h, rolloutLayerLCGate)
	default:
		out.Supported = true
		if out.MVGate {
			out.Detail = fmt.Sprintf("uygulanabilir — her cluster'da replicaset kapsaması ≥ %%%.0f; MV (ADIM 6) dahil", rolloutLayerMVGate*100)
		} else {
			out.Detail = "uygulanabilir (kolon + index + tablolar) — MV kapısı KAPALI: en az bir cluster'da k8s.replicaset.name kapsaması eşiğin altında (collector) — ADIM 6 atlanır"
		}
	}
	return out, nil
}

// RolloutLayerApply — gömülü 0012, ifade ifade; ilk hatada durur. withMV
// yalnız kapı açıkken (çağıran preflight'a bakar).
func (s *Store) RolloutLayerApply(ctx context.Context, cluster string, withMV bool) []RollupStmtResult {
	c := strings.TrimSpace(cluster)
	if c == "" {
		return []RollupStmtResult{{Head: "ön koşul", Err: "cluster adı zorunlu — DDL `ON CLUSTER` yazıyor"}}
	}
	if !validRolloutLayerCluster(c) {
		return []RollupStmtResult{{Head: "ön koşul", Err: "cluster adı geçersiz — yalnız harf/rakam/_ . - (≤64)"}}
	}
	stmts, err := rolloutLayerStatements(c, withMV)
	if err != nil {
		return []RollupStmtResult{{Head: "ön koşul", Err: err.Error()}}
	}
	out := make([]RollupStmtResult, 0, len(stmts))
	for _, stmt := range stmts {
		r := RollupStmtResult{Head: stmtHead(stmt)}
		if err := s.conn.Exec(ctx, stmt); err != nil {
			r.Err = err.Error()
			out = append(out, r)
			return out
		}
		r.OK = true
		out = append(out, r)
	}
	return out
}

// RolloutLayerRollback — yalnız MV; ilk hatada DURMAZ.
func (s *Store) RolloutLayerRollback(ctx context.Context, cluster string) []RollupStmtResult {
	c := strings.TrimSpace(cluster)
	if c == "" {
		return []RollupStmtResult{{Head: "ön koşul", Err: "cluster adı zorunlu"}}
	}
	if !validRolloutLayerCluster(c) {
		return []RollupStmtResult{{Head: "ön koşul", Err: "cluster adı geçersiz — yalnız harf/rakam/_ . - (≤64)"}}
	}
	stmts := rolloutLayerRollbackStatements(c)
	out := make([]RollupStmtResult, 0, len(stmts))
	for _, stmt := range stmts {
		r := RollupStmtResult{Head: stmtHead(stmt)}
		if err := s.conn.Exec(ctx, stmt); err != nil {
			r.Err = err.Error()
		} else {
			r.OK = true
		}
		out = append(out, r)
	}
	return out
}

// ───────────────── v0.10.960 — 0015 Rollouts v2 state tabloları ─────────────────

const (
	rolloutV2LayerFile         = "0015_rollouts_v2.sql"
	rolloutV2LayerRollbackFile = "0015_rollouts_v2_rollback.sql"
	// rolloutV2ZKPrefix — 0015'in SABİT ZK öneki (karar 25); boot'un
	// varsayılanıyla (state_replication.go zkPrefix) aynı.
	rolloutV2ZKPrefix = "/clickhouse/tables"
)

// rolloutV2TableNames — sekiz tablo adı, §10.3 sırasıyla. TEK KAYNAK
// rollout_v2_schema.go: elle ikinci bir liste tutulmaz.
func rolloutV2TableNames() []string {
	ddls := rolloutV2TableDDLs()
	out := make([]string, 0, len(ddls))
	for _, ddl := range ddls {
		if n, ok := ddlCreatesObject(ddl); ok {
			out = append(out, n)
		}
	}
	return out
}

func rolloutV2LayerObjects() []RolloutLayerObject {
	names := rolloutV2TableNames()
	out := make([]RolloutLayerObject, 0, len(names))
	for _, n := range names {
		out = append(out, RolloutLayerObject{Name: n, Kind: "table"})
	}
	return out
}

// rolloutV2LayerStatements — gömülü 0015, küme adıyla, ifadelere bölünmüş. SAF.
func rolloutV2LayerStatements(cluster string) ([]string, error) {
	raw, err := migrations.FS.ReadFile(rolloutV2LayerFile)
	if err != nil {
		return nil, fmt.Errorf("gömülü %s okunamadı: %w", rolloutV2LayerFile, err)
	}
	return SplitSQLStatements(AdaptRollupDDL(string(raw), cluster)), nil
}

// rolloutV2LayerRollbackStatements — gömülü 0015 rollback (sekiz DROP … SYNC). SAF.
func rolloutV2LayerRollbackStatements(cluster string) ([]string, error) {
	raw, err := migrations.FS.ReadFile(rolloutV2LayerRollbackFile)
	if err != nil {
		return nil, fmt.Errorf("gömülü %s okunamadı: %w", rolloutV2LayerRollbackFile, err)
	}
	return SplitSQLStatements(AdaptRollupDDL(string(raw), cluster)), nil
}

// rolloutV2HostTable — sekiz tablodan birinin bir host'taki hâli (probe satırı).
type rolloutV2HostTable struct {
	Host, Table, Engine, ZKPath string
}

func rolloutV2NameList() string {
	names := rolloutV2TableNames()
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "'" + n + "'" // adlar kendi sabit DDL'imizden, kullanıcı girdisi değil
	}
	return strings.Join(q, ", ")
}

// rolloutV2TablesProbeSQL / rolloutV2ReplicasProbeSQL — SAF. Küme adı
// validRolloutLayerCluster'dan geçmiş olmalı (DDL'e değil, sorguya ham girer).
// skip_unavailable_shards YOK: ulaşılamayan host probe'u düşürür → ön kontrol
// reddeder (o host'a ON CLUSTER DDL zaten kuyrukta takılırdı).
func rolloutV2TablesProbeSQL(cluster string) string {
	return fmt.Sprintf("SELECT hostName() AS host, name, engine FROM clusterAllReplicas('%s', system.tables) "+
		"WHERE database = currentDatabase() AND name IN (%s) LIMIT 1000 SETTINGS max_execution_time = 10",
		cluster, rolloutV2NameList())
}

func rolloutV2ReplicasProbeSQL(cluster string) string {
	return fmt.Sprintf("SELECT hostName() AS host, table, zookeeper_path FROM clusterAllReplicas('%s', system.replicas) "+
		"WHERE database = currentDatabase() AND table IN (%s) LIMIT 1000 SETTINGS max_execution_time = 10",
		cluster, rolloutV2NameList())
}

// rolloutV2LayerConflicts — SAF: 0015'i basmayı güvensiz kılan host
// durumları, (host, tablo) sırasında. Tablo hiç yoksa ya da her yerde
// Replicated + sabit yoldaysa boş.
func rolloutV2LayerConflicts(engines, replicas []rolloutV2HostTable) []string {
	type row struct{ host, table, msg string }
	var rows []row
	for _, e := range engines {
		if !strings.HasPrefix(e.Engine, "Replicated") {
			rows = append(rows, row{e.Host, e.Table, fmt.Sprintf("%s: %s — motor %s (Replicated değil; 0015'in IF NOT EXISTS'i bu host'ta no-op kalır — boot'un kurduğu kopyayı önce o host'ta düşür)", e.Host, e.Table, e.Engine)})
		}
	}
	for _, r := range replicas {
		want := unifiedStatePath(rolloutV2ZKPrefix, r.Table)
		if r.ZKPath != want {
			rows = append(rows, row{r.Host, r.Table, fmt.Sprintf("%s: %s — ZK yolu %s (0015: %s; eksik host'lar AYRI replikasyon grubuna katılırdı)", r.Host, r.Table, r.ZKPath, want)})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].host != rows[j].host {
			return rows[i].host < rows[j].host
		}
		return rows[i].table < rows[j].table
	})
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.msg
	}
	return out
}

// rolloutV2LayerGate — ön kontrol kararının girdileri (SAF karar için).
type rolloutV2LayerGate struct {
	ProbeErrors  int
	SpansLocal   bool
	Clusters     []string
	Cluster      string
	CustomPrefix string // küme kipinde varsayılandan farklı ZK öneki; "" = yok
	Conflicts    []string
}

// rolloutV2LayerDecision — SAF karar; sıra: emin değilsek hiç basma.
func rolloutV2LayerDecision(g rolloutV2LayerGate) (bool, string) {
	switch {
	case g.ProbeErrors > 0:
		return false, "probe hatası — emin olamadığımız kümeye DDL basmıyoruz"
	case !g.SpansLocal:
		return false, "spans_local yok — bu kurulum tek düğüm; 0015 dağıtık şema içindir (uygulama sekiz tabloyu boot'ta kendi kurar, rollout_v2_schema.go)"
	case g.Cluster == "":
		return false, "küme seçilmedi — DDL `ON CLUSTER` yazıyor"
	case !validRolloutLayerCluster(g.Cluster):
		return false, "küme adı geçersiz — yalnız harf/rakam/_ . - (≤64)"
	case !slices.Contains(g.Clusters, g.Cluster):
		return false, fmt.Sprintf("%q system.clusters'ta yok — ON CLUSTER DDL kuyrukta süresiz bekler (v0.9.613)", g.Cluster)
	case g.CustomPrefix != "":
		// v0.10.971 — ret aynen, gerekçe yeni: kural 3 kalktı (kuşak probe'u yok).
		return false, fmt.Sprintf("küme kipinde özel ZK öneki (%s) — 0015 %s/state/<ad> SABİT yazar; bu önekte o yol birleşik sayılmaz (eksik host boot'la ayrı gruba düşer, kart 'eski' gösterir). Boot hiçbir host'ta olmayan sekiz tabloyu %s/state/<ad> yoluna zaten birleşik kurar (v0.10.971) — 0015 gerekmez; yine de gerekiyorsa dosyayı öneke uyarlayıp elle uygula (karar 25)", g.CustomPrefix, rolloutV2ZKPrefix, g.CustomPrefix)
	case len(g.Conflicts) > 0:
		return false, fmt.Sprintf("%d çakışma — %s", len(g.Conflicts), strings.Join(g.Conflicts, " · "))
	}
	return true, "uygulanabilir: sekiz Rollouts v2 state tablosu (ON CLUSTER, ReplicatedReplacingMergeTree, IF NOT EXISTS; ilk hatada durur)"
}

// RolloutV2LayerPreflightResult — "0015 bu kümeye güvenle basılır mı".
type RolloutV2LayerPreflightResult struct {
	Clusters         []string `json:"clusters"`
	SuggestedCluster string   `json:"suggestedCluster,omitempty"`
	// Cluster — çakışma probe'unun koştuğu küme (istenen; boşsa önerilen).
	Cluster    string `json:"cluster"`
	SpansLocal bool   `json:"spansLocal"`
	// BootManaged — cluster_name dolu: boot sekiz tabloyu kendisi kurar
	// (spans varsa arka plana ertelenmiş); 0015 yalnız eksik host'ları tamamlar.
	BootManaged bool     `json:"bootManaged"`
	Conflicts   []string `json:"conflicts"`
	ProbeErrors []string `json:"probeErrors,omitempty"`
	Supported   bool     `json:"supported"`
	Detail      string   `json:"detail"`
	Generated   int64    `json:"generated"`
}

// RolloutV2LayerPreflight — hiçbir şey yazmaz. cluster boşsa önerilen küme
// (cfg.ClusterName ya da spans Distributed'ının kümesi) probe edilir.
func (s *Store) RolloutV2LayerPreflight(ctx context.Context, cluster string) (RolloutV2LayerPreflightResult, error) {
	out := RolloutV2LayerPreflightResult{
		SuggestedCluster: strings.TrimSpace(s.cfg.ClusterName),
		BootManaged:      s.clusterMode(),
		Generated:        time.Now().Unix(),
		Clusters:         []string{}, // null değil
		Conflicts:        []string{},
	}
	if out.SuggestedCluster == "" {
		out.SuggestedCluster = s.discoverSpansCluster(ctx)
	}
	out.Cluster = strings.TrimSpace(cluster)
	if out.Cluster == "" {
		out.Cluster = out.SuggestedCluster
	}
	rows, err := s.conn.Query(ctx, `SELECT DISTINCT cluster FROM system.clusters ORDER BY cluster LIMIT 100`)
	if err != nil {
		out.ProbeErrors = append(out.ProbeErrors, "system.clusters: "+err.Error())
	} else {
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err == nil && c != "" {
				out.Clusters = append(out.Clusters, c)
			}
		}
		rows.Close()
	}
	if ok, err := s.tableExists(ctx, "spans_local"); err != nil {
		out.ProbeErrors = append(out.ProbeErrors, "spans_local: "+err.Error())
	} else {
		out.SpansLocal = ok
	}
	// Çakışma probe'u yalnız geçerli + tanımlı kümede (aksi hâlde karar
	// zaten reddeder; tanımsız adla clusterAllReplicas hata verirdi).
	if validRolloutLayerCluster(out.Cluster) && slices.Contains(out.Clusters, out.Cluster) {
		engines, err := s.rolloutV2HostTables(ctx, rolloutV2TablesProbeSQL(out.Cluster), false)
		if err != nil {
			out.ProbeErrors = append(out.ProbeErrors, "tablo motorları: "+err.Error())
		}
		replicas, err := s.rolloutV2HostTables(ctx, rolloutV2ReplicasProbeSQL(out.Cluster), true)
		if err != nil {
			out.ProbeErrors = append(out.ProbeErrors, "ZK yolları: "+err.Error())
		}
		out.Conflicts = append(out.Conflicts, rolloutV2LayerConflicts(engines, replicas)...)
	}
	custom := ""
	if s.clusterMode() && s.zkPrefix() != rolloutV2ZKPrefix {
		custom = s.zkPrefix()
	}
	out.Supported, out.Detail = rolloutV2LayerDecision(rolloutV2LayerGate{
		ProbeErrors: len(out.ProbeErrors), SpansLocal: out.SpansLocal,
		Clusters: out.Clusters, Cluster: out.Cluster, CustomPrefix: custom, Conflicts: out.Conflicts,
	})
	return out, nil
}

// rolloutV2HostTables — (host, tablo, motor | zk yolu) satırları.
func (s *Store) rolloutV2HostTables(ctx context.Context, q string, zk bool) ([]rolloutV2HostTable, error) {
	rows, err := s.conn.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rolloutV2HostTable
	for rows.Next() {
		var r rolloutV2HostTable
		third := &r.Engine
		if zk {
			third = &r.ZKPath
		}
		if err := rows.Scan(&r.Host, &r.Table, third); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RolloutV2LayerApply — gömülü 0015, ifade ifade; ilk hatada durur. Kapı
// (RolloutV2LayerPreflight) çağıranda: HTTP ucu her istekte koşar.
func (s *Store) RolloutV2LayerApply(ctx context.Context, cluster string) []RollupStmtResult {
	c := strings.TrimSpace(cluster)
	if c == "" || !validRolloutLayerCluster(c) {
		return []RollupStmtResult{{Head: "ön koşul", Err: "cluster adı zorunlu/geçersiz — yalnız harf/rakam/_ . - (≤64)"}}
	}
	stmts, err := rolloutV2LayerStatements(c)
	if err != nil {
		return []RollupStmtResult{{Head: "ön koşul", Err: err.Error()}}
	}
	return s.execStmtsStopOnError(ctx, stmts)
}

// RolloutV2LayerRollback — sekiz tabloyu VERİSİYLE düşürür; ilk hatada
// DURMAZ (IF EXISTS — bir tablonun hatası ötekileri bırakmasın).
func (s *Store) RolloutV2LayerRollback(ctx context.Context, cluster string) []RollupStmtResult {
	c := strings.TrimSpace(cluster)
	if c == "" || !validRolloutLayerCluster(c) {
		return []RollupStmtResult{{Head: "ön koşul", Err: "cluster adı zorunlu/geçersiz — yalnız harf/rakam/_ . - (≤64)"}}
	}
	stmts, err := rolloutV2LayerRollbackStatements(c)
	if err != nil {
		return []RollupStmtResult{{Head: "ön koşul", Err: err.Error()}}
	}
	out := make([]RollupStmtResult, 0, len(stmts))
	for _, stmt := range stmts {
		r := RollupStmtResult{Head: stmtHead(stmt)}
		if err := s.conn.Exec(ctx, stmt); err != nil {
			r.Err = err.Error()
		} else {
			r.OK = true
		}
		out = append(out, r)
	}
	return out
}
