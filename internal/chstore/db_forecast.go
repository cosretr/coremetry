package chstore

// db_forecast.go — v0.10.909 (Dynatrace paritesi #6 dilim 3; spec Onay
// 2026-09-24): DB kapasite gösterge kartının "kaç saat kaldı" verisi. Düz veri
// tipi; hesap api katmanında istek anında (internal/api/db_capacity_forecast.go,
// forecast.Fit) — şema değişikliği yok, Problem açık olmasa da görünür.

// DBForecast — bir (usage, limit) göstergesinin projeksiyonu.
type DBForecast struct {
	// Status — ok | at_limit | none.
	Status string `json:"status"`
	// Hours — limite kalan saat (ok'ta); band LoHours..HiHours, HiOpen üst
	// sınır yok ("N+"); Wide band dürüst kısa hâli ister.
	Hours   float64 `json:"hours,omitempty"`
	LoHours float64 `json:"loHours,omitempty"`
	HiHours float64 `json:"hiHours,omitempty"`
	HiOpen  bool    `json:"hiOpen,omitempty"`
	Wide    bool    `json:"wide,omitempty"`
	R2      float64 `json:"r2,omitempty"`
	Points  int     `json:"points"`
	// Reason — none'da neden ("düz", "uyum zayıf (R² 0.41)", "24 saatten uzak").
	Reason  string `json:"reason,omitempty"`
	Source  string `json:"source"`  // vm | ch
	WindowH int    `json:"windowH"` // okunan pencere (saat)
}
