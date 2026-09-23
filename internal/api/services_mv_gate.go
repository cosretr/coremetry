package api

import "time"

// services_mv_gate.go — /api/services MV hızlı-yol kapısı. v0.10.882'de api.go'dan
// buraya taşındı (api.go büyümez; /api-route).
//
// service_summary_5m 5 dakikada bir satır yazar (5 dk altı pencere boş okur)
// ve cluster/env boyutu taşımaz — bu iki filtre v0.5.372/v0.8.385'ten beri
// sınırlı ham-spans yoluna düşerdi. v0.10.882 (paritesi #8, dilim 2):
// service_env_summary_5m boyutlu ikiz; envMV = o MV pencereyi KAPSIYOR
// (chstore.EnvSummaryCovers — MV geriye dolmaz, ilk kovası pencere başını
// geçmemeli). Kapsıyorsa cluster/env filtresi de MV yolundan gider; kapsamıyorsa
// eski ham yol, bayt-bayt. SAF — env_gate_test.go pinler.
func servicesUseMV(window time.Duration, cluster, env string, envMV bool) bool {
	if window < 5*time.Minute {
		return false
	}
	return (cluster == "" && env == "") || envMV
}
