// v0.10.978 — boot Keeper muhafızı: eksik gözlemde "hiçbir yerde yok"
// hükmünden ÖNCE Keeper'a (system.zookeeper) bakılır.
//
// KÖR NOKTA (v0.10.971, stateProbeCoverage): küme geneli probe
// `skip_unavailable_shards=1` ile koşar; cevap vermeyen bir replikada ESKİ
// yolda duran tablo "hiçbir node'da yok" sanılır ve kural 4 onu birleşik yola
// kurar — yeni bölünme. 971 bunu yalnız logluyordu.
//
// Bu dosya şunu çiviler:
//  1. Muhafız YALNIZ gözlem eksikken ve tablo gözlenmemişken konuşulur; tam
//     gözlemde ve kural 1/2'de Keeper'a HİÇ sorulmaz (SAF karar: decideStatePath).
//  2. Keeper'da YALNIZ eski yolda replika kümesi varsa ona KATILIR; birleşik
//     yolda varsa (eski yolda da olsa) ya da hiçbir yerde yoksa birleşik
//     (kural 4). İki yolda da varsa gerekçe ve log ⚠ ile çakışmayı söyler:
//     0009 `_old` penceresinde eski kazansaydı muhafız var olan birleşik grubu
//     BÖLERDİ (yedeğin kümesine katılırdı) — v0.10.971 hükmü korunur.
//  3. Keeper okuması düşerse v0.10.971 davranışı (birleşik) + gürültülü ⚠.
//  4. system.zookeeper sorgusu tavanlı (LIMIT 1, max_execution_time) ve YALNIZ
//     eksik gözlemde çıkar (sahte driver.Conn: probeConn); birleşik znode HER
//     ZAMAN okunur (eski yol kanıtı kısa devre yapmaz).
//  5. {shard} makro okuması (küme geneli) gözlem başına BİR kez; Keeper
//     okumaları tablo başına.
//
// Canlı ClickHouse yok; host adları sentetik (host-1..4, uptrace_all).
package chstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/config"
)

const kpPfx = "/clickhouse/tables"

// TestDecideStatePathKeeperGuard — SAF karar. Kapalı `keeper` çağrılırsa test
// kızarır: muhafızın hangi dallarda SUSTUĞU da sözleşmenin parçası.
func TestDecideStatePathKeeperGuard(t *testing.T) {
	legacy := func(n string) string { return kpPfx + "/01/" + n }
	unified := func(n string) string { return kpPfx + "/state/" + n }
	obs := func(ok, complete bool, paths map[string]string) stateObservation {
		return stateObservation{ok: ok, complete: complete, paths: paths}
	}
	tests := []struct {
		name       string
		obs        stateObservation
		tbl        string
		evidence   *keeperEvidence // nil → muhafız çağrılmamalı
		wantUnif   bool
		wantGuard  stateGuard
		wantReason string
		wantAlso   string // ikinci gerekçe parçası ("" = yok)
	}{
		{
			name: "TAM gözlem, tablo yok → kural 4, muhafız SORULMAZ",
			obs:  obs(true, true, map[string]string{"problems": legacy("problems")}),
			tbl:  "ai_eval_runs", wantUnif: true, wantGuard: stateGuardSkipped, wantReason: statePathFreshReason,
		},
		{
			name: "kural 1: probe koşmadı (gözlem eksik sayılır) → ESKİ, muhafız SORULMAZ",
			obs:  stateObservation{},
			tbl:  "ai_eval_runs", wantUnif: false, wantGuard: stateGuardSkipped, wantReason: "probe koşmadı",
		},
		{
			name: "kural 2: eksik gözlemde tablo eski yolda GÖZLENDİ → ESKİ, muhafız SORULMAZ",
			obs:  obs(true, false, map[string]string{"problems": legacy("problems")}),
			tbl:  "problems", wantUnif: false, wantGuard: stateGuardSkipped, wantReason: "komşularına katılıyor",
		},
		{
			name: "kural 2: eksik gözlemde tablo birleşik yolda GÖZLENDİ → BİRLEŞİK, muhafız SORULMAZ",
			obs:  obs(true, false, map[string]string{"users": unified("users")}),
			tbl:  "users", wantUnif: true, wantGuard: stateGuardSkipped, wantReason: "zaten birleşik",
		},
		{
			name:     "EKSİK + Keeper: YALNIZ eski yolda replika var → ESKİ (komşularına katıl)",
			obs:      obs(true, false, map[string]string{"problems": legacy("problems")}),
			tbl:      "ai_eval_runs",
			evidence: &keeperEvidence{legacyPath: kpPfx + "/02/ai_eval_runs"},
			wantUnif: false, wantGuard: stateGuardLegacy, wantReason: statePathKeeperLegacyReason,
		},
		{
			name:     "EKSİK + Keeper: birleşik yolda replika var → BİRLEŞİK",
			obs:      obs(true, false, map[string]string{}),
			tbl:      "ai_eval_runs",
			evidence: &keeperEvidence{unified: true},
			wantUnif: true, wantGuard: stateGuardUnified, wantReason: statePathKeeperUnifiedReason,
		},
		{
			// 0009 `_old` penceresi: yedek `<t>_old` eski yolda (RENAME znode
			// taşımaz), canlı `<t>` birleşik yolda; gözlem `_old`'u görmez
			// (stateProbeTable) ve Keeper yedeğin kümesini `<t>`'ninkinden
			// ayıramaz. Eski kazansaydı tablosu olmayan node YEDEĞİN grubuna
			// katılır, var olan birleşik grup bölünürdü — v0.10.971 burada zaten
			// birleşik diyordu. Hüküm BİRLEŞİK, gerekçe her iki yolu ⚠ ile söyler.
			name:     "EKSİK + Keeper: her iki yolda da var → BİRLEŞİK (kural 4 korunur) + ⚠ eski yol da var",
			obs:      obs(true, false, map[string]string{}),
			tbl:      "ai_eval_runs",
			evidence: &keeperEvidence{legacyPath: kpPfx + "/01/ai_eval_runs", unified: true},
			wantUnif: true, wantGuard: stateGuardUnified, wantReason: kpPfx + "/01/ai_eval_runs",
			wantAlso: statePathKeeperUnifiedReason + "; ⚠ eski yolda da replika kümesi var",
		},
		{
			name:     "EKSİK + Keeper: hiçbir yolda yok → BİRLEŞİK (kural 4)",
			obs:      obs(true, false, map[string]string{}),
			tbl:      "ai_eval_runs",
			evidence: &keeperEvidence{},
			wantUnif: true, wantGuard: stateGuardNoEvidence, wantReason: statePathFreshReason,
		},
		{
			name:     "EKSİK + Keeper okunamadı → BİRLEŞİK (v0.10.971 davranışı) + uyarı bayrağı",
			obs:      obs(true, false, map[string]string{}),
			tbl:      "ai_eval_runs",
			evidence: &keeperEvidence{err: errors.New("code: 497, message: Not enough privileges (ACCESS_DENIED)")},
			wantUnif: true, wantGuard: stateGuardFailed, wantReason: "ACCESS_DENIED",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			keeper := func(name string) keeperEvidence {
				calls++
				if tc.evidence == nil {
					t.Fatalf("muhafız %q için SORULDU — bu dalda Keeper'a bakılmamalı", name)
				}
				if name != tc.tbl {
					t.Fatalf("muhafız %q için soruldu, %q bekleniyordu", name, tc.tbl)
				}
				return *tc.evidence
			}
			got, reason, guard := decideStatePath(tc.obs, kpPfx, tc.tbl, keeper)
			if got != tc.wantUnif {
				t.Errorf("unified = %v (%s), beklenen %v", got, reason, tc.wantUnif)
			}
			if guard != tc.wantGuard {
				t.Errorf("guard = %v, beklenen %v (%s)", guard, tc.wantGuard, reason)
			}
			if reason == "" || !strings.Contains(reason, tc.wantReason) {
				t.Errorf("gerekçe %q, %q içermeli", reason, tc.wantReason)
			}
			if tc.wantAlso != "" && !strings.Contains(reason, tc.wantAlso) {
				t.Errorf("gerekçe %q, %q da içermeli", reason, tc.wantAlso)
			}
			if wantCalls := 0; tc.evidence != nil {
				wantCalls = 1
				if calls != wantCalls {
					t.Errorf("muhafız %d kez soruldu, %d bekleniyordu", calls, wantCalls)
				}
			}
			if tc.wantGuard == stateGuardFailed && !strings.Contains(reason, "koşamadı") {
				t.Errorf("Keeper hatasında gerekçe muhafızın koşamadığını söylemeli: %q", reason)
			}
			// Sarmalayıcı (cluster.go ve seed planının çağırdığı) aynı hükmü verir.
			if u, r := useUnifiedStatePath(withKeeper(tc.obs, keeper), kpPfx, tc.tbl); u != got || r != reason {
				t.Errorf("useUnifiedStatePath = (%v, %q), decideStatePath = (%v, %q)", u, r, got, reason)
			}
		})
	}
}

// withKeeper — gözlemin kopyasına muhafızı bağlar (testler için).
func withKeeper(obs stateObservation, k stateKeeperLookup) stateObservation {
	obs.keeper = k
	return obs
}

// TestDecideStatePathNoGuardBound — muhafız bağlı DEĞİLSE (elle kurulan gözlem:
// sihirbazın gölge Store'u, eski testler) eksik gözlem kural 4'ü değiştirmez —
// gerekçe metni v0.10.971 ile bayt bayt aynı kalır (seed planı bunu pinler).
func TestDecideStatePathNoGuardBound(t *testing.T) {
	obs := stateObservation{ok: true, paths: map[string]string{}}
	got, reason, guard := decideStatePath(obs, kpPfx, "events", nil)
	if !got || reason != statePathFreshReason || guard != stateGuardSkipped {
		t.Errorf("= (%v, %q, %v), kural 4 + statePathFreshReason bekleniyordu", got, reason, guard)
	}
}

// TestLegacyShardDirs — gözlenen eski yollardan shard dizinleri: yalnız
// `<önek>/<dizin>/<tablo>` biçimindekiler, tekilleştirilmiş, sıralı, tavanlı.
func TestLegacyShardDirs(t *testing.T) {
	tests := []struct {
		name  string
		paths map[string]string
		want  []string
	}{
		{
			name: "iki shard dizini, birleşik yol elenir, tekrar tekilleşir",
			paths: map[string]string{
				"problems":     kpPfx + "/01/problems",
				"users":        kpPfx + "/state/users",
				"ai_eval_runs": kpPfx + "/02/ai_eval_runs",
				"slos":         kpPfx + "/01/slos",
			},
			want: []string{"01", "02"},
		},
		{
			// 0010 ARA DURUM: `problems` `/state/problems_repart` yolunda gözlenir —
			// eski yol DEĞİL, shard dizini de değil (sonek tablo adıyla uyuşmaz).
			name:  "_repart yolu shard dizini değildir",
			paths: map[string]string{"problems": kpPfx + "/state/problems_repart"},
			want:  nil,
		},
		{
			name:  "yabancı önek elenir",
			paths: map[string]string{"problems": "/other/01/problems"},
			want:  nil,
		},
		{
			// system.replicas makroyu GENİŞLETİR; literal `{shard}` yalnız sentetik
			// testlerde görülür ve Keeper'da var olamaz.
			name:  "genişlememiş makro elenir",
			paths: map[string]string{"problems": kpPfx + "/{shard}/problems"},
			want:  nil,
		},
		{
			name:  "önekin altında iç içe dizin olduğu gibi alınır",
			paths: map[string]string{"problems": kpPfx + "/coremetry/01/problems"},
			want:  []string{"coremetry/01"},
		},
		{
			// Önek ile sonek üst üste biner (dizin yok): dilim sınırı çaprazlanmaz,
			// yol elenir — boot'ta panik değil.
			name:  "tablo doğrudan önek altında (dizin yok) elenir",
			paths: map[string]string{"tables": kpPfx + "/tables", "a": kpPfx + "/a"},
			want:  nil,
		},
		{name: "boş gözlem", paths: map[string]string{}, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := legacyShardDirs(tc.paths, kpPfx)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("= %q, beklenen %q", got, tc.want)
			}
		})
	}
	// Tavan: muhafızın Keeper okuması sayısı sınırlı kalır.
	many := map[string]string{}
	for i := 0; i < stateKeeperMaxDirs+5; i++ {
		many[fmt.Sprintf("t%02d", i)] = fmt.Sprintf("%s/%02d/t%02d", kpPfx, i, i)
	}
	if got := legacyShardDirs(many, kpPfx); len(got) != stateKeeperMaxDirs {
		t.Errorf("tavan: %d dizin, %d bekleniyordu", len(got), stateKeeperMaxDirs)
	}
}

// TestKeeperReplicasPathAndSQL — bakılan znode'lar ve sorgunun şekli: yalnız
// `path = ?` (kısıtsız Keeper taraması değil), LIMIT 1 (kanıt = en az bir
// replika znode'u), tavanlı, node-yerel (Keeper zaten kümenin ortak belleği).
func TestKeeperReplicasPathAndSQL(t *testing.T) {
	if got := keeperReplicasPath(kpPfx, "01", "ai_eval_runs"); got != kpPfx+"/01/ai_eval_runs/replicas" {
		t.Errorf("eski yol znode'u: %q", got)
	}
	if got := keeperReplicasPath(kpPfx, stateReplicaDir, "ai_eval_runs"); got != unifiedStatePath(kpPfx, "ai_eval_runs")+"/replicas" {
		t.Errorf("birleşik yol znode'u unifiedStatePath ile ayrıştı: %q", got)
	}
	for _, want := range []string{"SELECT name FROM system.zookeeper WHERE path = ?", "LIMIT 1", "SETTINGS max_execution_time = 5"} {
		if !strings.Contains(stateKeeperReplicasSQL, want) {
			t.Errorf("%q içermeli: %s", want, stateKeeperReplicasSQL)
		}
	}
	if strings.Contains(stateKeeperReplicasSQL, "clusterAllReplicas") || strings.Contains(stateKeeperReplicasSQL, "LIKE") {
		t.Errorf("Keeper okuması node-yerel ve tam yol olmalı: %s", stateKeeperReplicasSQL)
	}
}

// kpConn — probeConn üzerine muhafızın iki okuması (system.zookeeper,
// system.macros) eklenmiş sahte bağlantı; ai_eval_runs hiçbir gözlemde yok.
func kpConn(roster, answered uint64) *probeConn {
	return &probeConn{local: host1Rows(), cluster: host1Rows(), roster: roster, answered: answered}
}

const kpTable = "ai_eval_runs"

// TestStateKeeperGuardWiring — sahte driver.Conn üzerinde boot probe'u +
// karar: system.zookeeper sorgusu YALNIZ eksik gözlemde, doğru yollarla ve
// tavanlı çıkar; sonuç adaptDDL'in ürettiği ENGINE argümanlarına yansır.
func TestStateKeeperGuardWiring(t *testing.T) {
	legacyZK := keeperReplicasPath(kpPfx, "01", kpTable)
	unifiedZK := keeperReplicasPath(kpPfx, stateReplicaDir, kpTable)
	cases := []struct {
		name        string
		conn        *probeConn
		wantUnified bool
		wantReason  string
		wantZK      []string // sırayla bakılan znode'lar; nil = HİÇ bakılmamalı
		wantMacros  bool     // {shard} makroları okundu mu
		wantWarn    string   // "" = muhafız ⚠'sı yok
		wantLog     string
	}{
		{
			// TUZAK: Keeper'da eski yol kanıtı var ama gözlem TAM → bakılmaz bile.
			name: "tam gözlem → Keeper'a bakılmaz, kural 4",
			conn: func() *probeConn {
				c := kpConn(4, 4)
				c.zk = map[string][]string{legacyZK: {"01-2"}}
				return c
			}(),
			wantUnified: true, wantReason: statePathFreshReason,
		},
		{
			// Eski yol kanıtı kısa devre DEĞİL: birleşik znode da okunur (iki yolda
			// da küme varsa hüküm birleşik olacaktır).
			name: "eksik (1/4) + yalnız eski yolda replika → ESKİ, birleşik yola da BAKILIR",
			conn: func() *probeConn {
				c := kpConn(4, 1)
				c.zk = map[string][]string{legacyZK: {"01-2"}}
				return c
			}(),
			wantUnified: false, wantReason: statePathKeeperLegacyReason,
			wantZK:  []string{legacyZK, unifiedZK},
			wantLog: "eski yolda replika kümesi VAR",
		},
		{
			name: "eksik + yalnız birleşik yolda replika → BİRLEŞİK",
			conn: func() *probeConn {
				c := kpConn(4, 1)
				c.zk = map[string][]string{unifiedZK: {"01-1", "02-1"}}
				return c
			}(),
			wantUnified: true, wantReason: statePathKeeperUnifiedReason,
			wantZK: []string{legacyZK, unifiedZK},
		},
		{
			// 0009 `_old` penceresi (2×2 prod, host-1 boş diskle yeniden kuruldu,
			// host-2..4 cevap vermiyor): `<t>_old` yedeği `/01/<t>` altında, canlı
			// `<t>` `/state/<t>` altında. Eski kazansaydı host-1'in `<t>`'si yedeğin
			// grubuna katılırdı — yeni bölünme. Hüküm BİRLEŞİK, log iki yolu da adıyla söyler.
			name: "eksik + HER İKİ yolda replika (0009 `_old` penceresi) → BİRLEŞİK + ⚠ eski yol da var",
			conn: func() *probeConn {
				c := kpConn(4, 1)
				c.zk = map[string][]string{legacyZK: {"01-2"}, unifiedZK: {"01-1", "02-1"}}
				return c
			}(),
			wantUnified: true, wantReason: statePathKeeperUnifiedReason,
			wantZK:  []string{legacyZK, unifiedZK},
			wantLog: "HER İKİ yolda da replika kümesi var: birleşik (" + unifiedZK + ") ve eski (" + legacyZK + ")",
		},
		{
			name:        "eksik + hiçbir yolda yok (ZNONODE hatası) → BİRLEŞİK, kural 4",
			conn:        kpConn(4, 1),
			wantUnified: true, wantReason: statePathFreshReason,
			wantZK:  []string{legacyZK, unifiedZK},
			wantLog: "hiçbir yolda replika kümesi yok",
		},
		{
			// Yeni CH sürümleri olmayan znode için hata yerine BOŞ sonuç döndürür.
			name: "eksik + hiçbir yolda yok (boş sonuç) → BİRLEŞİK",
			conn: func() *probeConn {
				c := kpConn(4, 1)
				c.zkNoNodeAsEmpty = true
				return c
			}(),
			wantUnified: true, wantReason: statePathFreshReason,
			wantZK: []string{legacyZK, unifiedZK},
		},
		{
			name: "eksik + Keeper okunamadı → BİRLEŞİK (v0.10.971) + ⚠ muhafız koşamadı",
			conn: func() *probeConn {
				c := kpConn(4, 1)
				c.zkErr = errors.New("code: 497, message: Not enough privileges (ACCESS_DENIED)")
				return c
			}(),
			wantUnified: true, wantReason: "koşamadı",
			wantZK:   []string{legacyZK},
			wantWarn: "Keeper muhafızı KOŞAMADI",
		},
		{
			// Küme okuması HATA verdi → gözlem yalnız yerel, yine eksik → muhafız çalışır.
			name: "küme okuması hata (yalnız yerel) → Keeper'a bakılır",
			conn: func() *probeConn {
				c := kpConn(4, 1)
				c.clusterErr = errors.New("code: 279, message: All connection tries failed")
				c.zk = map[string][]string{legacyZK: {"01-2"}}
				return c
			}(),
			wantUnified: false, wantReason: statePathKeeperLegacyReason,
			wantZK: []string{legacyZK, unifiedZK},
		},
		{
			// Gözlenen eski yol YOK (hepsi birleşik) → shard dizinleri {shard}
			// makrolarından; ikinci shard'ın eski yolunda replika bulunur (01'de yok,
			// döngü 02'de durur, birleşik yine okunur).
			name: "eksik, gözlenen eski yol yok → {shard} makroları; 02'de replika → ESKİ",
			conn: func() *probeConn {
				c := kpConn(4, 2)
				c.local = [][]any{{"users", kpPfx + "/state/users"}}
				c.cluster = c.local
				c.macros = [][]any{
					{"host-1", "shard", "01"}, {"host-1", "replica", "host-1"},
					{"host-3", "shard", "02"}, {"host-3", "replica", "host-3"},
				}
				c.zk = map[string][]string{keeperReplicasPath(kpPfx, "02", kpTable): {"02-1"}}
				return c
			}(),
			wantUnified: false, wantReason: kpPfx + "/02/" + kpTable,
			wantZK:     []string{legacyZK, keeperReplicasPath(kpPfx, "02", kpTable), unifiedZK},
			wantMacros: true,
		},
		{
			name: "eksik, gözlenen eski yol yok, makrolar okunamadı → BİRLEŞİK + ⚠ (Keeper'a bakılmadı)",
			conn: func() *probeConn {
				c := kpConn(4, 2)
				c.local = [][]any{{"users", kpPfx + "/state/users"}}
				c.cluster = c.local
				c.macrosErr = errors.New("code: 159, Timeout exceeded")
				return c
			}(),
			wantUnified: true, wantReason: "koşamadı",
			wantMacros: true, wantWarn: "Keeper muhafızı KOŞAMADI",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf := captureLog(t)
			s := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}, conn: c.conn}
			s.resolveStateReplicaPaths(context.Background())
			if !s.stateObs.ok {
				t.Fatalf("yerel okuma başarılı, probe koşmuş sayılmalı; log:\n%s", buf.String())
			}
			if len(c.conn.zkPaths) != 0 {
				t.Fatalf("probe'un kendisi Keeper'a baktı (%q) — muhafız yalnız CREATE kararında konuşulur", c.conn.zkPaths)
			}

			got, reason := useUnifiedStatePath(s.stateObs, kpPfx, kpTable)
			logged := buf.String()
			if got != c.wantUnified {
				t.Errorf("unified = %v (%s), beklenen %v", got, reason, c.wantUnified)
			}
			if !strings.Contains(reason, c.wantReason) {
				t.Errorf("gerekçe %q, %q içermeli", reason, c.wantReason)
			}
			if strings.Join(c.conn.zkPaths, "\n") != strings.Join(c.wantZK, "\n") {
				t.Errorf("Keeper'da bakılan yollar:\n%q\nbeklenen:\n%q", c.conn.zkPaths, c.wantZK)
			}
			for _, q := range c.conn.queries {
				if strings.Contains(q, "system.zookeeper") && q != stateKeeperReplicasSQL {
					t.Errorf("Keeper sorgusu şekil dışı: %s", q)
				}
			}
			if c.conn.macrosRead != c.wantMacros {
				t.Errorf("{shard} makroları okundu = %v, beklenen %v", c.conn.macrosRead, c.wantMacros)
			}
			if c.wantWarn == "" {
				if strings.Contains(logged, "KOŞAMADI") {
					t.Errorf("muhafız ⚠'sı olmamalıydı:\n%s", logged)
				}
			} else if !strings.Contains(logged, "⚠") || !strings.Contains(logged, c.wantWarn) {
				t.Errorf("⚠ %q loglanmalıydı:\n%s", c.wantWarn, logged)
			}
			if c.wantLog != "" && !strings.Contains(logged, c.wantLog) {
				t.Errorf("log %q içermeli:\n%s", c.wantLog, logged)
			}

			// Uçtan uca: boot'un CREATE'i (cluster.go adaptDDL) aynı hükmü alır.
			ddl := "CREATE TABLE IF NOT EXISTS " + kpTable + " (\n\t`id` String,\n\t`version` UInt64\n) ENGINE = ReplacingMergeTree(version)\nORDER BY id"
			out := strings.Join(s.adaptDDL(ddl), "\n")
			want := replicatedArgs(kpPfx, kpTable, c.wantUnified)
			if !strings.Contains(out, want) {
				t.Errorf("adaptDDL %q içermeli:\n%s", want, out)
			}

			// Gözlenen HER tablo için muhafız SUSAR (kural 2), eksik gözlemde bile:
			// gözlenen yol neyse hüküm odur, Keeper'a bakılmaz.
			before := len(c.conn.zkPaths)
			if len(s.stateObs.paths) == 0 {
				t.Fatal("sahte gözlem en az bir tablo taşımalı")
			}
			for tbl, p := range s.stateObs.paths {
				if u, r := useUnifiedStatePath(s.stateObs, kpPfx, tbl); u != (p == unifiedStatePath(kpPfx, tbl)) {
					t.Errorf("gözlenen %s (%s) = %v (%s) — kural 2 gözlenen yolu vermeli", tbl, p, u, r)
				}
			}
			if len(c.conn.zkPaths) != before {
				t.Errorf("gözlenen tablo için Keeper'a bakıldı: %q", c.conn.zkPaths[before:])
			}
		})
	}
}

// TestStateKeeperGuardMacrosReadOnce — v0.10.978 — {shard} makro okuması
// (küme geneli, 10 s tavanlı) gözlem başına BİR kezdir: taze kurulumda ~55
// gözlenmemiş state tablosu aynı kapanıştan geçer; tablo başına tekrar eden
// okuma senkron migrate()'i 55 küme turu bekletirdi. Hata dalı da bir kez.
// Keeper okumaları ise tablo başına kalır (her tablonun znode'u ayrı).
func TestStateKeeperGuardMacrosReadOnce(t *testing.T) {
	for _, macrosErr := range []error{nil, errors.New("code: 159, Timeout exceeded")} {
		captureLog(t)
		c := kpConn(4, 2)
		c.local = [][]any{{"users", kpPfx + "/state/users"}}
		c.cluster = c.local
		c.macros = [][]any{{"host-1", "shard", "01"}, {"host-3", "shard", "02"}}
		c.macrosErr = macrosErr
		s := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}, conn: c}
		s.resolveStateReplicaPaths(context.Background())
		tables := []string{kpTable, "teams", "dashboards"}
		for _, tbl := range tables {
			useUnifiedStatePath(s.stateObs, kpPfx, tbl)
		}
		if c.macrosReads != 1 {
			t.Errorf("macrosErr=%v: {shard} makroları %d kez okundu, beklenen 1 (gözlem başına bir)", macrosErr, c.macrosReads)
		}
		wantZK := 0
		if macrosErr == nil {
			wantZK = len(tables) * 3 // 01, 02, birleşik — tablo başına
		}
		if len(c.zkPaths) != wantZK {
			t.Errorf("macrosErr=%v: Keeper okuması %d, beklenen %d (tablo başına kalmalı)", macrosErr, len(c.zkPaths), wantZK)
		}
	}
}

// TestStateKeeperGuardNotBoundWhenComplete — tam gözlemde gözleme muhafız hiç
// BAĞLANMAZ; kararın "sorulmadı" dalı bir tesadüf değil, yapısal.
func TestStateKeeperGuardNotBoundWhenComplete(t *testing.T) {
	captureLog(t)
	s := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}, conn: kpConn(4, 4)}
	s.resolveStateReplicaPaths(context.Background())
	if !s.stateObs.complete || s.stateObs.keeper != nil {
		t.Errorf("tam gözlem: complete=%v keeper bağlı=%v", s.stateObs.complete, s.stateObs.keeper != nil)
	}
	s2 := &Store{cfg: config.CHConfig{ClusterName: "uptrace_all"}, conn: kpConn(4, 1)}
	s2.resolveStateReplicaPaths(context.Background())
	if s2.stateObs.complete || s2.stateObs.keeper == nil {
		t.Errorf("eksik gözlem: complete=%v keeper bağlı=%v", s2.stateObs.complete, s2.stateObs.keeper != nil)
	}
}

// TestStateProbeLogLineIncomplete — boot özet satırı eksik gözlemde muhafızı
// duyurur; "(N birleşik, M eski)" parçası (runbook alıntısı) değişmez.
func TestStateProbeLogLineIncomplete(t *testing.T) {
	full := stateProbeLogLine(47, 47, 0, true)
	if strings.Contains(full, "Keeper") || !strings.Contains(full, "47 tablo gözlendi (47 birleşik, 0 eski)") {
		t.Errorf("tam gözlem: %q", full)
	}
	part := stateProbeLogLine(3, 1, 2, false)
	if !strings.Contains(part, "3 tablo gözlendi (1 birleşik, 2 eski)") || !strings.Contains(part, "gözlem EKSİK") || !strings.Contains(part, "Keeper") {
		t.Errorf("eksik gözlem: %q", part)
	}
}

// TestStateKeeperGuardLogLine — SAF log metni: beş sonucun her biri tabloyu,
// bakılan dizinleri ve hükmü söyler; iki-yol satırı her iki znode'u adıyla
// ve ⚠ ile taşır.
func TestStateKeeperGuardLogLine(t *testing.T) {
	dirs := []string{"01", "02"}
	tests := []struct {
		ev   keeperEvidence
		want []string
	}{
		{keeperEvidence{legacyPath: kpPfx + "/02/x"}, []string{"eski yolda replika kümesi VAR", kpPfx + "/02/x", "komşularına katılıyor"}},
		{keeperEvidence{unified: true}, []string{"birleşik yolda replika kümesi var"}},
		{keeperEvidence{legacyPath: kpPfx + "/02/x", unified: true}, []string{"⚠", "HER İKİ yolda", kpPfx + "/state/x/replicas", kpPfx + "/02/x/replicas", "birleşik yola katılıyor", "0009 ADIM 5"}},
		{keeperEvidence{}, []string{"hiçbir yolda replika kümesi yok", "01, 02", "kural 4"}},
		{keeperEvidence{err: errors.New("boom")}, []string{"⚠", "KOŞAMADI", "boom", "v0.10.971", "BÖLÜNÜR"}},
	}
	for _, tc := range tests {
		line := stateKeeperGuardLogLine(kpPfx, "x", dirs, "gözlenen eski yollar", tc.ev)
		if !strings.Contains(line, "[chstore]") || !strings.Contains(line, " x ") {
			t.Errorf("satır tabloyu ve öneki taşımalı: %q", line)
		}
		for _, w := range tc.want {
			if !strings.Contains(line, w) {
				t.Errorf("%q içermeli: %q", w, line)
			}
		}
	}
}
