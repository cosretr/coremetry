package argocd

// put_test.go — v0.10.974 — Argo CD ayar sekmesi onayının (operatör,
// 2026-09-27, "Onay rollout için önerin") PUT kurallarının SAF seam'i:
// ParsePut (istek-yalnız clearTokenRef bayrağı; düz token / eski anahtar
// reddi ÖNCE) ve ApplyPut (a) BE4 kayıtlı hub'ı instance'ları bağlıyken
// kaldırma ön denetimi → (b) Validate aynen → (c) clearTokenRef + dolu
// tokenRef çakışması → (d) BE3 kayıtlı kimliği değiştirme reddi → (e) BE1 boş
// tokenRef kayıtlıyı korur. Hata metinleri apiDelta'daki 400 gövdeleriyle
// birebir (FE yalnız "<field>: " önekini soyar). Canlı sistem YOK.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// putClusters — hub adları apiDelta örnekleriyle aynı (hub-1, hub-2).
var putClusters = []ClusterRef{
	{ID: "c-aaaa0001", Name: "hub-1", Enabled: true},
	{ID: "c-bbbb0002", Name: "hub-2", Enabled: true},
	{ID: "c-cccc0003", Name: "cluster-c", Enabled: true},
}

// putStored — kayıtlı blob: iki hub, üç instance, üçünün de ref'i var.
func putStored() Settings {
	return Settings{
		Enabled: true,
		Hubs:    []Hub{{ClusterID: "c-aaaa0001"}, {ClusterID: "c-bbbb0002"}},
		Instances: []Instance{
			{ID: "team-a-prod", HubClusterID: "c-aaaa0001", HubNamespace: "team-a-prod", TokenRef: "env:ARGOCD_TEAM_A_TOKEN", Enabled: true},
			{ID: "team-b-uat", HubClusterID: "c-aaaa0001", HubNamespace: "team-b-uat", TokenRef: "file:/var/run/secrets/argocd/team-b", Enabled: true},
			{ID: "team-c-prod", HubClusterID: "c-bbbb0002", HubNamespace: "team-c-prod", TokenRef: "env:ARGOCD_TEAM_C_TOKEN", Enabled: true},
		},
		UpdatedAt: 42,
	}
}

// putIncoming — FE'nin dokunulmamış satırları gönderdiği biçim: aynı blob,
// tokenRef'ler BOŞ (sunucu kayıtlıyı korur).
func putIncoming() Settings {
	s := putStored()
	s.UpdatedAt = 0
	s.Instances = append([]Instance(nil), s.Instances...)
	for i := range s.Instances {
		s.Instances[i].TokenRef = ""
	}
	return s
}

func refsByID(s Settings) map[string]string {
	out := map[string]string{}
	for _, inst := range s.Instances {
		out[inst.ID] = inst.TokenRef
	}
	return out
}

func clearAt(idx ...int) PutOptions {
	o := PutOptions{ClearTokenRef: map[int]bool{}}
	for _, i := range idx {
		o.ClearTokenRef[i] = true
	}
	return o
}

func TestParsePutClearTokenRef(t *testing.T) {
	ok := []struct {
		name, body string
		want       map[int]bool
	}{
		{"bayrak yok", `{"instances":[{"id":"a","hubNamespace":"a"}]}`, map[int]bool{}},
		{"true sent dizinine bağlanır", `{"instances":[{"id":"a"},{"id":"b","clearTokenRef":true},{"id":"c","clearTokenRef":false}]}`, map[int]bool{1: true}},
		{"üçte iki", `{"instances":[{"id":"a","clearTokenRef":true},{"id":"b"},{"id":"c","clearTokenRef":true}]}`, map[int]bool{0: true, 2: true}},
		// encoding/json alan adlarını büyük/küçük harf duyarsız eşler; bayrak da öyle
		// (aksi hâlde "ClearTokenRef" sessizce atılır, operatör kaldırdığını sanardı).
		{"büyük harfli anahtar", `{"instances":[{"id":"a","ClearTokenRef":true}]}`, map[int]bool{0: true}},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			s, opts, err := ParsePut([]byte(tc.body))
			if err != nil {
				t.Fatalf("geçerli gövde: %v", err)
			}
			if len(opts.ClearTokenRef) != len(tc.want) {
				t.Fatalf("bayraklar %v, beklenen %v", opts.ClearTokenRef, tc.want)
			}
			for i := range tc.want {
				if !opts.ClearTokenRef[i] {
					t.Fatalf("bayraklar %v, beklenen %v", opts.ClearTokenRef, tc.want)
				}
			}
			// İstek-yalnız: Settings'e / JSON'a asla girmez.
			raw, _ := json.Marshal(s)
			if strings.Contains(strings.ToLower(string(raw)), "cleartokenref") {
				t.Fatalf("clearTokenRef blob'a girdi: %s", raw)
			}
		})
	}
	bad := []struct {
		name, body, path, msg string
	}{
		{"dize", `{"instances":[{"id":"a"},{"id":"b"},{"id":"c","clearTokenRef":"yes"}]}`, "instances[2].clearTokenRef",
			"instances[2].clearTokenRef: true ya da false olmalı"},
		{"sayı", `{"instances":[{"id":"a","clearTokenRef":1}]}`, "instances[0].clearTokenRef", "instances[0].clearTokenRef: true ya da false olmalı"},
		{"null", `{"instances":[{"id":"a","clearTokenRef":null}]}`, "instances[0].clearTokenRef", "instances[0].clearTokenRef: true ya da false olmalı"},
		// ParseInput ÖNCE: düz token ve eski tek-hub anahtarı reddi korunur.
		{"düz token önce", `{"instances":[{"id":"a","token":"eyJ","clearTokenRef":"x"}]}`, "instances[0].token", ""},
		{"eski üst düzey anahtar önce", `{"hubClusterId":"c-1","instances":[{"id":"a","clearTokenRef":"x"}]}`, "hubClusterId", ""},
		{"bozuk JSON", `{"instances":[`, "", ""},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParsePut([]byte(tc.body))
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Path != tc.path {
				t.Fatalf("FieldError{%q} beklenirdi: %#v", tc.path, err)
			}
			if tc.msg != "" && err.Error() != tc.msg {
				t.Fatalf("metin:\n got %q\nwant %q", err.Error(), tc.msg)
			}
		})
	}
}

// BE1 — boş tokenRef kayıtlı referansı korur (id'ye göre; BE3 sayesinde güvenli).
func TestApplyPutTokenRefMerge(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Settings)
		opts PutOptions
		want map[string]string
	}{
		{"koru: boş ref + bayrak yok → kayıtlı", func(*Settings) {}, PutOptions{},
			map[string]string{"team-a-prod": "env:ARGOCD_TEAM_A_TOKEN", "team-b-uat": "file:/var/run/secrets/argocd/team-b", "team-c-prod": "env:ARGOCD_TEAM_C_TOKEN"}},
		{"kaldır: boş ref + clearTokenRef", func(*Settings) {}, clearAt(1),
			map[string]string{"team-a-prod": "env:ARGOCD_TEAM_A_TOKEN", "team-b-uat": "", "team-c-prod": "env:ARGOCD_TEAM_C_TOKEN"}},
		{"değiştir: dolu ref", func(s *Settings) { s.Instances[2].TokenRef = " env:ARGOCD_TEAM_C_NEW " }, PutOptions{},
			map[string]string{"team-a-prod": "env:ARGOCD_TEAM_A_TOKEN", "team-b-uat": "file:/var/run/secrets/argocd/team-b", "team-c-prod": "env:ARGOCD_TEAM_C_NEW"}},
		{"yeni id + boş ref → ref yok", func(s *Settings) {
			s.Instances = append(s.Instances, Instance{ID: "team-d-prod", HubClusterID: "c-bbbb0002", HubNamespace: "team-d-prod"})
		}, PutOptions{},
			map[string]string{"team-a-prod": "env:ARGOCD_TEAM_A_TOKEN", "team-b-uat": "file:/var/run/secrets/argocd/team-b", "team-c-prod": "env:ARGOCD_TEAM_C_TOKEN", "team-d-prod": ""}},
		{"bilinmeyen id'de clearTokenRef etkisiz (hata yok)", func(s *Settings) {
			s.Instances = append(s.Instances, Instance{ID: "team-d-prod", HubClusterID: "c-bbbb0002", HubNamespace: "team-d-prod"})
		}, clearAt(3),
			map[string]string{"team-a-prod": "env:ARGOCD_TEAM_A_TOKEN", "team-b-uat": "file:/var/run/secrets/argocd/team-b", "team-c-prod": "env:ARGOCD_TEAM_C_TOKEN", "team-d-prod": ""}},
		// Dizin GÖNDERİLEN sıradır (kayıtlı sıra değil): 3 instance ters sırada,
		// bayrak 0 ve 2'de → yalnız o iki satır.
		{"dizin eşlemesi: ters sıra, bayrak 0 ve 2", func(s *Settings) {
			s.Instances[0], s.Instances[2] = s.Instances[2], s.Instances[0]
		}, clearAt(0, 2),
			map[string]string{"team-c-prod": "", "team-b-uat": "file:/var/run/secrets/argocd/team-b", "team-a-prod": ""}},
		{"hub taşıması ref'i korur (id anahtar)", func(s *Settings) { s.Instances[1].HubClusterID = "c-bbbb0002" }, PutOptions{},
			map[string]string{"team-a-prod": "env:ARGOCD_TEAM_A_TOKEN", "team-b-uat": "file:/var/run/secrets/argocd/team-b", "team-c-prod": "env:ARGOCD_TEAM_C_TOKEN"}},
		{"id boşluklu gelir, kırpılmış id ile eşlenir", func(s *Settings) { s.Instances[0].ID = " team-a-prod " }, PutOptions{},
			map[string]string{"team-a-prod": "env:ARGOCD_TEAM_A_TOKEN", "team-b-uat": "file:/var/run/secrets/argocd/team-b", "team-c-prod": "env:ARGOCD_TEAM_C_TOKEN"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := putIncoming()
			tc.mut(&in)
			out, err := ApplyPut(in, tc.opts, putStored(), putClusters)
			if err != nil {
				t.Fatalf("ApplyPut: %v", err)
			}
			got := refsByID(out)
			if len(got) != len(tc.want) {
				t.Fatalf("instance'lar %v, beklenen %v", got, tc.want)
			}
			for id, ref := range tc.want {
				if got[id] != ref {
					t.Errorf("%s ref %q, beklenen %q", id, got[id], ref)
				}
			}
			if out.UpdatedAt != 0 {
				t.Errorf("çıktı Validate'ten geçmiş olmalı (updatedAt sunucu sahipli): %d", out.UpdatedAt)
			}
		})
	}
}

func TestApplyPutClearTokenRefConflict(t *testing.T) {
	in := putIncoming()
	in.Instances[2].TokenRef = "env:ARGOCD_TEAM_C_NEW"
	_, err := ApplyPut(in, clearAt(2), putStored(), putClusters)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Path != "instances[2].clearTokenRef" {
		t.Fatalf("FieldError{instances[2].clearTokenRef} beklenirdi: %v", err)
	}
	const want = "instances[2].clearTokenRef: tokenRef doluyken clearTokenRef gönderilemez — yeni referansı yazın ya da kaldırın, ikisi birden değil"
	if err.Error() != want {
		t.Fatalf("metin:\n got %q\nwant %q", err.Error(), want)
	}
	// Validate ÖNCE koşar: geçersiz blobda çakışma değil doğrulama hatası döner.
	in.Instances[0].HubNamespace = "Bad_NS"
	if _, err := ApplyPut(in, clearAt(2), putStored(), putClusters); !errors.As(err, &fe) || fe.Path != "instances[0].hubNamespace" {
		t.Fatalf("Validate hatası önce gelmeli: %v", err)
	}
}

// Kayıtlı ref config import'la bozuk gelmiş olabilir (import tam değiştirir,
// doğrulamaz): PUT yolu bozuk ref'i sessizce yeniden kalıcılaştırmaz ve
// değeri mesajda yankılamaz.
func TestApplyPutKeepRejectsInvalidStoredRef(t *testing.T) {
	stored := putStored()
	stored.Instances[0].TokenRef = "eyJhbGciOi-plaintext"
	_, err := ApplyPut(putIncoming(), PutOptions{}, stored, putClusters)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Path != "instances[0].tokenRef" {
		t.Fatalf("FieldError{instances[0].tokenRef} beklenirdi: %v", err)
	}
	if strings.Contains(err.Error(), "eyJhbGciOi") {
		t.Fatalf("kayıtlı değer mesajda yankılandı: %v", err)
	}
	// Kaldırmak ya da yenisini yazmak kurtarır.
	if _, err := ApplyPut(putIncoming(), clearAt(0), stored, putClusters); err != nil {
		t.Fatalf("clearTokenRef bozuk kayıtlıyı kaldırabilmeli: %v", err)
	}
}

// BE3 — kayıtlı instance kimliği salt okunur (Faz 3'te CH instance_id).
func TestApplyPutRenameRejected(t *testing.T) {
	in := putIncoming()
	in.Instances[0].ID = "team-a-gitops" // aynı yuva (hub-1/team-a-prod), eski id gövdede yok
	_, err := ApplyPut(in, PutOptions{}, putStored(), putClusters)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Path != "instances[0].id" {
		t.Fatalf("FieldError{instances[0].id} beklenirdi: %v", err)
	}
	const want = `instances[0].id: "team-a-gitops" kayıtlı "team-a-prod" instance'ının yerini alıyor (hub-1/team-a-prod): ` +
		`kayıtlı kimlik değiştirilemez (Faz 3'te ClickHouse instance_id). Önce "team-a-prod" satırını kaldırıp kaydedin, sonra "team-a-gitops" ile ekleyin.`
	if err.Error() != want {
		t.Fatalf("metin:\n got %q\nwant %q", err.Error(), want)
	}
}

func TestApplyPutRenameAllowedCases(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Settings)
	}{
		{"aynı blob", func(*Settings) {}},
		{"sil + ilgisiz yuvaya ekle", func(s *Settings) {
			s.Instances = append(s.Instances[:1], s.Instances[2:]...) // team-b-uat silindi
			s.Instances = append(s.Instances, Instance{ID: "team-d-prod", HubClusterID: "c-aaaa0001", HubNamespace: "team-d-prod"})
		}},
		{"yalnız sil", func(s *Settings) { s.Instances = s.Instances[:2] }},
		{"kayıtlı id'yi başka hub'a taşı", func(s *Settings) { s.Instances[1].HubClusterID = "c-bbbb0002" }},
		{"taşınan id + boşalan yuvaya yeni id", func(s *Settings) {
			s.Instances[1].HubClusterID = "c-bbbb0002"
			s.Instances = append(s.Instances, Instance{ID: "team-b-uat-2", HubClusterID: "c-aaaa0001", HubNamespace: "team-b-uat"})
		}},
		// namespace de değişen "yeniden adlandırma" sil+ekle'den ayırt edilemez →
		// izinli (pins[i].instanceId pinleri korur).
		{"id + namespace birlikte değişir", func(s *Settings) {
			s.Instances[0] = Instance{ID: "team-a-gitops", HubClusterID: "c-aaaa0001", HubNamespace: "team-a-gitops"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := putIncoming()
			tc.mut(&in)
			if _, err := ApplyPut(in, PutOptions{}, putStored(), putClusters); err != nil {
				t.Fatalf("izinli olmalı: %v", err)
			}
		})
	}
}

// BE4 — instance'ları bağlı kayıtlı hub kaldırılamaz; yol settings_test.go'nun
// pinlediği instances[i].hubClusterId, mesaj hub'ı, sayıyı ve çareyi söyler.
func TestApplyPutHubRemovalBlocked(t *testing.T) {
	in := putIncoming()
	in.Hubs = in.Hubs[:1] // hub-2 çıkarılıyor
	in.Instances = []Instance{
		in.Instances[0], in.Instances[1],
		{ID: "team-x-prod", HubClusterID: "c-aaaa0001", HubNamespace: "team-x-prod"},
		in.Instances[2], // team-c-prod, hub-2'de → ilk sorunlu dizin 3
		{ID: "team-e-prod", HubClusterID: " c-bbbb0002 ", HubNamespace: "team-e-prod"}, // kırpılmış hali sayılır
	}
	_, err := ApplyPut(in, PutOptions{}, putStored(), putClusters)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Path != "instances[3].hubClusterId" {
		t.Fatalf("FieldError{instances[3].hubClusterId} beklenirdi: %v", err)
	}
	const want = `instances[3].hubClusterId: hub "hub-2" (c-bbbb0002) listeden çıkarılıyor ama 2 instance ona bağlı — ` +
		`instance'ları başka hub'a taşıyın ya da hub'ı listede bırakın`
	if err.Error() != want {
		t.Fatalf("metin:\n got %q\nwant %q", err.Error(), want)
	}
}

func TestApplyPutHubRemovalAllowed(t *testing.T) {
	// Aynı PUT'ta instance'ları taşımak hub'ı serbest bırakır.
	in := putIncoming()
	in.Hubs = in.Hubs[:1]
	in.Instances[2].HubClusterID = "c-aaaa0001"
	out, err := ApplyPut(in, PutOptions{}, putStored(), putClusters)
	if err != nil {
		t.Fatalf("taşıma + kaldırma izinli: %v", err)
	}
	if len(out.Hubs) != 1 || refsByID(out)["team-c-prod"] != "env:ARGOCD_TEAM_C_TOKEN" {
		t.Fatalf("hub kalkmalı, taşınan instance ref'ini korumalı: %+v", out)
	}
	// Bağlı instance'ı olmayan hub serbestçe kalkar.
	in = putIncoming()
	in.Instances = in.Instances[:2]
	in.Hubs = in.Hubs[:1]
	if _, err := ApplyPut(in, PutOptions{}, putStored(), putClusters); err != nil {
		t.Fatalf("boş hub kaldırılabilir: %v", err)
	}
}

// Kayıtlı OLMAYAN bir hub'a bağlı instance BE4 değil, Validate'in genel
// kuralıdır: yol aynı, mesaj artık çareyi de söyler (v0.10.974).
func TestApplyPutUnknownHubGenericMessage(t *testing.T) {
	in := putIncoming()
	in.Instances[0].HubClusterID = "c-cccc0003" // var olan küme, hubs'ta yok, hiç hub olmadı
	_, err := ApplyPut(in, PutOptions{}, putStored(), putClusters)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Path != "instances[0].hubClusterId" {
		t.Fatalf("FieldError{instances[0].hubClusterId} beklenirdi: %v", err)
	}
	if !strings.Contains(err.Error(), "hubs listesinde değil") || !strings.Contains(err.Error(), "hubs listesine ekleyin") {
		t.Fatalf("genel mesaj çareyi söylemeli: %v", err)
	}
}

// v0.10.974 — eski blob: kayıtlı instance'ın hubClusterId'si BOŞ ve tek
// kayıtlı hub var → yuva o hub sayılır; aynı yuvada kimlik değiştirmek yine
// BE3 reddi (Validate gelen satırın boş hub'ını tek hub'la tamamlar, kayıtlı
// taraf da aynı hub'a eşlenmezse yeniden adlandırma sessizce geçerdi).
func TestApplyPutRenameRejectedLegacyEmptyHub(t *testing.T) {
	stored := Settings{
		Hubs:      []Hub{{ClusterID: "c-aaaa0001"}},
		Instances: []Instance{{ID: "team-a-prod", HubNamespace: "team-a-prod", Enabled: true}},
	}
	in := Settings{
		Hubs:      []Hub{{ClusterID: "c-aaaa0001"}},
		Instances: []Instance{{ID: "team-a-new", HubClusterID: "c-aaaa0001", HubNamespace: "team-a-prod", Enabled: true}},
	}
	_, err := ApplyPut(in, PutOptions{}, stored, putClusters)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Path != "instances[0].id" {
		t.Fatalf("FieldError{instances[0].id} beklenirdi: %v", err)
	}
}

// ── v0.10.978 — iyimser ön koşul (expectedUpdatedAt) ───────────────────────
//
// İki admin aynı anda düzenlerken bütün blob değiştirildiğinden son yazan
// kazanıyordu (v0.10.974'te ertelenen karar). İstek-yalnız expectedUpdatedAt
// (GET settings.updatedAt) kayıtlı damgayla karşılaştırılır: tutmazsa
// StaleError (409), gönderilmemişse kabul (API/token çağıranlar, eski bundle).
// Karşılaştırma float64 üzerinden: tarayıcı JSON sayısını çift duyarlıkla
// okur ve 2^53 üstündeki ns damgasını 256'nın katına yuvarlayıp o değeri
// gönderir; tam eşitlik her tarayıcı PUT'unu 409'a düşürürdü.

func TestParsePutExpectedUpdatedAt(t *testing.T) {
	ok := []struct {
		name, body string
		want       *int64
	}{
		{"yok → nil", `{"instances":[{"id":"a","hubNamespace":"a"}]}`, nil},
		{"0 (hiç kaydedilmemiş blob)", `{"expectedUpdatedAt":0}`, i64(0)},
		{"tam sayı", `{"expectedUpdatedAt":42}`, i64(42)},
		{"tarayıcının yuvarladığı ns damgası", `{"expectedUpdatedAt":1758979200123456800}`, i64(1758979200123456800)},
		{"büyük/küçük harf duyarsız (encoding/json gibi)", `{"ExpectedUpdatedAt":7}`, i64(7)},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			s, opts, err := ParsePut([]byte(tc.body))
			if err != nil {
				t.Fatalf("geçerli gövde: %v", err)
			}
			switch {
			case tc.want == nil && opts.ExpectedUpdatedAt != nil:
				t.Fatalf("nil beklenirdi: %d", *opts.ExpectedUpdatedAt)
			case tc.want != nil && (opts.ExpectedUpdatedAt == nil || *opts.ExpectedUpdatedAt != *tc.want):
				t.Fatalf("beklenen %d, gelen %v", *tc.want, opts.ExpectedUpdatedAt)
			}
			// İstek-yalnız: Settings.UpdatedAt'e (sunucu sahipli) hiç dokunmaz.
			if s.UpdatedAt != 0 {
				t.Fatalf("expectedUpdatedAt bloba girdi: %d", s.UpdatedAt)
			}
		})
	}
	bad := []struct{ name, body string }{
		{"dize", `{"expectedUpdatedAt":"42"}`},
		{"null", `{"expectedUpdatedAt":null}`},
		{"bool", `{"expectedUpdatedAt":true}`},
		{"ondalık", `{"expectedUpdatedAt":1.5}`},
		{"üstel", `{"expectedUpdatedAt":1e18}`},
		{"negatif", `{"expectedUpdatedAt":-1}`},
	}
	const wantMsg = "expectedUpdatedAt: 0 ya da pozitif tam sayı olmalı (GET settings.updatedAt)"
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParsePut([]byte(tc.body))
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Path != "expectedUpdatedAt" || err.Error() != wantMsg {
				t.Fatalf("FieldError{expectedUpdatedAt} beklenirdi:\n got %v\nwant %q", err, wantMsg)
			}
		})
	}
	// ParseInput ÖNCE: düz token reddi ön koşul ayrıştırmasını beklemez.
	_, _, err := ParsePut([]byte(`{"expectedUpdatedAt":"x","instances":[{"id":"a","token":"eyJ"}]}`))
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Path != "instances[0].token" {
		t.Fatalf("düz token önce: %v", err)
	}
}

func i64(v int64) *int64 { return &v }

func TestApplyPutPrecondition(t *testing.T) {
	const (
		storedNs = int64(1758979200123456789) // Go time.Now().UnixNano()
		jsNs     = int64(1758979200123456800) // tarayıcının JSON.parse → JSON.stringify çıktısı
	)
	cases := []struct {
		name     string
		stored   int64
		expected *int64
		stale    bool
	}{
		{"gönderilmedi → kabul", 42, nil, false},
		{"birebir eşit", 42, i64(42), false},
		{"tarayıcı yuvarlaması (256'nın katı) eşit sayılır", storedNs, i64(jsNs), false},
		{"1000 ns fark → bayat", storedNs, i64(storedNs + 1000), true},
		{"hiç kaydedilmemiş + 0", 0, i64(0), false},
		{"hiç kaydedilmemiş görüldü, arada kaydedildi", 42, i64(0), true},
		{"kayıt görüldü, blob sıfırlandı", 0, i64(42), true},
		{"eski damga", 43, i64(42), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored := putStored()
			stored.UpdatedAt = tc.stored
			out, err := ApplyPut(putIncoming(), PutOptions{ExpectedUpdatedAt: tc.expected}, stored, putClusters)
			var se *StaleError
			if !tc.stale {
				if err != nil {
					t.Fatalf("kabul edilmeli: %v", err)
				}
				if out.UpdatedAt != 0 {
					t.Fatalf("updatedAt sunucu sahipli (SavePersisted damgalar): %d", out.UpdatedAt)
				}
				return
			}
			if !errors.As(err, &se) {
				t.Fatalf("StaleError beklenirdi: %#v", err)
			}
			if se.Current != tc.stored || se.Expected != *tc.expected {
				t.Fatalf("StaleError{Expected:%d, Current:%d}, beklenen {%d, %d}", se.Expected, se.Current, *tc.expected, tc.stored)
			}
			var fe *FieldError
			if errors.As(err, &fe) {
				t.Fatalf("bayatlık alan hatası değil (400 değil 409): %v", err)
			}
		})
	}
	// Metin kısa ve Türkçe; FE kendi kutusunu çizer, bu metin API/token çağıranlara.
	const want = "ayarlar bu sayfa yüklendikten sonra başka biri tarafından değiştirildi — yeniden yükleyin"
	if got := (&StaleError{Expected: 1, Current: 2}).Error(); got != want {
		t.Fatalf("metin:\n got %q\nwant %q", got, want)
	}
	// Ön koşul her şeyden ÖNCE: bayat taban üzerinde doğrulama hatası anlamsız
	// (kullanıcının gördüğü blob artık yok) — geçersiz gövde + bayat damga → 409.
	stored := putStored()
	in := putIncoming()
	in.Instances[0].HubNamespace = "Bad_NS"
	_, err := ApplyPut(in, PutOptions{ExpectedUpdatedAt: i64(41)}, stored, putClusters)
	var se *StaleError
	if !errors.As(err, &se) {
		t.Fatalf("bayatlık doğrulamadan önce gelmeli: %v", err)
	}
	// BE4 ön denetimi de sonra (hub kaldırma + bayat → 409).
	in = putIncoming()
	in.Hubs = in.Hubs[:1]
	if _, err := ApplyPut(in, PutOptions{ExpectedUpdatedAt: i64(41)}, stored, putClusters); !errors.As(err, &se) {
		t.Fatalf("bayatlık BE4'ten önce gelmeli: %v", err)
	}
}
