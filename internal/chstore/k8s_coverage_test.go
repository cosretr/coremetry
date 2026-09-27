package chstore

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// v0.10.964 — Rollouts v2 P5.3 (docs/rollouts/v2-audit.md §9, §12.1 P5.3):
// K8s kapsama kartına üç sayaç — service.version, container.image.tag,
// deployment.environment.name.
//
// SÖZLEŞME:
//   - Sorgu bugünkü gibi ÖRNEKLEMELİ ve SINIRLI kalır (zaman-sınırlı WHERE,
//     servis × zaman dilimi kotası, dış LIMIT, max_execution_time).
//   - Üç sayaç da res_keys üzerinde has() — diğer sayaçlar ve §11.8 T1 ile
//     aynı "anahtar VAR mı" anlamı. İç örneklem yalnız service_name +
//     res_keys taşır; ek kolon okunmaz:
//       · imaj etiketi terfi kolonundan (container_image_tag) OKUNMAZ:
//         res_keys zaten okunuyor (ek bayt yok); kolon probe durumuna ve
//         replika tutarlılığına bağlı olurdu (v0.10.339 sınıfı: yanlış ✗).
//       · env YALNIZ deployment.environment.name; deploy_env kolonu eski
//         deployment.environment'ı da kabul ettiği için iki yazımı
//         birleştirirdi (§9.3.2: eski env ⇒ türetilen cluster boş).
//   - Hiçbir dalda res_values okunmaz: kalın dizi kolonunu taramaya katmak
//     küçük filoda (dış tavan ısırmıyorsa pencerenin tamamı taranır) okunan
//     baytı katlar.
//   - SELECT takma adı sırası ↔ Scan sırası ↔ JSON alanı tek tabloda
//     çivili: bir kayma kartı YANLIŞ alanı "yayılıyor" diye gösterir ve
//     sayılar makul göründüğü için hata sessiz kalır.

// TestK8sCoverageSQLImageTagSource — imaj etiketi her zaman res_keys has()
// çifti; terfi kolonu sorguya hiç girmez (süreç durumundan bağımsız yük).
func TestK8sCoverageSQLImageTagSource(t *testing.T) {
	q := flatWSCH(k8sCoverageSQL(3600))
	want := "countIf(has(res_keys, 'container.image.tag') OR has(res_keys, 'k8s.container.image.tag')) AS img_tag"
	if !strings.Contains(q, want) {
		t.Errorf("imaj etiketi sayacı %q bekleniyordu:\n%s", want, q)
	}
	if strings.Contains(q, "container_image_tag") {
		t.Errorf("terfi kolonu okunmamalı (ek bayt + v0.10.339 yanlış ✗ riski):\n%s", q)
	}
}

// TestK8sCoverageSQLEnvCountsNewKeyOnly — env sayacı P5.3 satırının
// istediği anahtarı sayar: deployment.environment.name, §11.8 T1 gibi.
// Eski deployment.environment'ı da kabul eden deploy_env kolonu kullanılırsa
// yalnız eski yazımı yayan servis ✓ görünür ama env'den türetilen cluster
// boş kalır (§9.3.2) — kart T1'in yerini tutmaz.
func TestK8sCoverageSQLEnvCountsNewKeyOnly(t *testing.T) {
	q := flatWSCH(k8sCoverageSQL(3600))
	if !strings.Contains(q, "countIf(has(res_keys, 'deployment.environment.name')) AS env_name") {
		t.Errorf("env sayacı res_keys'te deployment.environment.name saymalı:\n%s", q)
	}
	if strings.Contains(q, "deploy_env") {
		t.Errorf("deploy_env iki yazımı birleştirir; sorguda olmamalı:\n%s", q)
	}
	if strings.Contains(q, "'deployment.environment'") {
		t.Errorf("eski deployment.environment sayılmamalı:\n%s", q)
	}
}

func TestK8sCoverageSQLIsSampledAndBounded(t *testing.T) {
	q := flatWSCH(k8sCoverageSQL(3600))
	for _, must := range []string{
		"FROM spans",
		"WHERE time >= ? AND time <= ?",
		// 1 saat → 12 dilim × 300 s, dilim başına ⌈400/12⌉ = 34 (sample_slices.go).
		"LIMIT 34 BY service_name, toStartOfInterval(time, INTERVAL 300 SECOND)",
		"LIMIT 200000 )",
		"GROUP BY service_name",
		"LIMIT ?",
		"SETTINGS max_execution_time = 25",
		// Yeni sayaçlar.
		"countIf(has(res_keys, 'service.version'))",
		"countIf(has(res_keys, 'deployment.environment.name'))",
		// İç örneklem yalnız service_name + res_keys taşır: yeni sayaçlar
		// ek kolon okumaz.
		"SELECT service_name, res_keys FROM spans",
		// Mevcut sayaçlar yerinde.
		"countIf(has(res_keys, 'k8s.pod.uid'))",
		"countIf(has(res_keys, 'container.image.name'))",
	} {
		if !strings.Contains(q, must) {
			t.Errorf("sorguda eksik: %q\n%s", must, q)
		}
	}
	// Bind argümanları sabit: (from, to, limit).
	if n := strings.Count(q, "?"); n != 3 {
		t.Errorf("bind yer tutucu sayısı %d, 3 bekleniyordu (from, to, limit)", n)
	}
	for _, bad := range []string{"res_values", "arrayJoin", "FINAL", "deploy_env", "container_image_tag"} {
		if strings.Contains(q, bad) {
			t.Errorf("sorguda %q olmamalı:\n%s", bad, q)
		}
	}
}

// TestK8sCoverageAliasScanJSONAgree — takma ad sırası = Scan sırası = JSON.
func TestK8sCoverageAliasScanJSONAgree(t *testing.T) {
	q := k8sCoverageSQL(3600)
	outer := q[:strings.Index(q, "FROM (")]
	var aliases []string
	for _, m := range regexp.MustCompile(`\bAS (\w+)`).FindAllStringSubmatch(outer, -1) {
		aliases = append(aliases, m[1])
	}
	wantJSON := map[string]string{
		"sampled": "sampled", "ns": "namespace", "depl": "deployment", "pod": "pod",
		"uid": "podUid", "node": "node", "cont": "container", "clus": "cluster",
		"rs": "replicaset", "img": "image", "clus_k8s": "clusterK8s", "clus_ocp": "clusterOpenshift",
		"svc_version": "serviceVersion", "img_tag": "imageTag", "env_name": "envName",
	}
	if len(aliases) != len(wantJSON) {
		t.Fatalf("dış SELECT'te %d takma ad var, %d bekleniyordu: %v", len(aliases), len(wantJSON), aliases)
	}
	var r K8sCoverageRow
	targets := k8sCoverageScanTargets(&r)
	if len(targets) != 1+len(aliases) {
		t.Fatalf("Scan hedefi %d, SELECT kolonu %d (service_name + %d sayaç)", len(targets), 1+len(aliases), len(aliases))
	}
	svc, ok := targets[0].(*string)
	if !ok {
		t.Fatalf("ilk Scan hedefi service_name (*string) olmalı, %T", targets[0])
	}
	*svc = "svc-synthetic"
	for i := range aliases {
		p, ok := targets[i+1].(*uint64)
		if !ok {
			// count()/countIf() UInt64 döner (v0.9.595 dersi).
			t.Fatalf("hedef %d (%s) *uint64 olmalı, %T", i+1, aliases[i], targets[i+1])
		}
		*p = uint64(i + 1)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["service"] != "svc-synthetic" {
		t.Errorf("service alanı %v", got["service"])
	}
	for i, a := range aliases {
		field, ok := wantJSON[a]
		if !ok {
			t.Errorf("beklenmeyen takma ad %q", a)
			continue
		}
		if v, _ := got[field].(float64); int(v) != i+1 {
			t.Errorf("takma ad %q (sıra %d) JSON %q alanına düşmeli; alan değeri %v", a, i+1, field, got[field])
		}
	}
}

// TestK8sCoverageImageTagFallbackMirrorsColumn — has() çifti terfi
// kolonunun coalesce ettiği yazımlarla AYNI olmalı; biri değişip öbürü
// kalırsa kart ile kolonu okuyan yüzeyler (v1 rollout, sürüm zinciri)
// farklı şeyi sayar.
func TestK8sCoverageImageTagFallbackMirrorsColumn(t *testing.T) {
	q := flatWSCH(k8sCoverageSQL(3600))
	for _, a := range promotedAttrs {
		if a.col != "container_image_tag" {
			continue
		}
		for _, k := range a.keys {
			if !strings.Contains(k8sCoverageImageTagExpr, "has(res_keys, '"+k+"')") {
				t.Errorf("imaj etiketi ifadesi %q yazımını saymıyor; kolon sayıyor: %s", k, k8sCoverageImageTagExpr)
			}
			if !strings.Contains(q, "has(res_keys, '"+k+"')") {
				t.Errorf("sorgu %q yazımını saymıyor", k)
			}
		}
		if !a.res {
			t.Error("container_image_tag resource kapsamlı olmalı (sayaç res_keys okuyor)")
		}
		return
	}
	t.Fatal("promotedAttrs'ta container_image_tag yok")
}
