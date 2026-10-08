package chstore

// problem_count_scope.go — v0.10.1131 (operatör-bildirimli): /inbox şerit
// çipi ("Dış kaynak (N)", "Veritabanı (N)") ve atlanan tür çipi ("Problems
// N") ortam seçiliyken listeyle AYRIŞIYORDU.
//
// Kök neden: inbox liste derlemesi env'i Go'da, birleştirilmiş satırlar
// üstünde EnvScopeKeepsRow ile uyguluyor (dış kaynak `ext:` öznesi hiçbir
// env'in üyesi değil → satır gizli). Sayım ise CountProblemsBySubject'e
// yalnız TAKIM kümesini geçiriyordu; env sayıma HİÇ inmiyordu. Sonuç:
// `?env=prod` seçiliyken çip 14 dış kaynak problemi sayarken şerit 0 satır
// gösteriyordu.
//
// Düzeltme: sayımın kapsamı tek yapı (ProblemCountScope) ve env ekseni
// listenin kullandığı AYNI yazımdan (envScopeConjunct — Go ikizi
// EnvScopeKeepsRow, eşitlik TestEnvScopeSQLAndGoAgree ile kanıtlı) gelir.
// İkinci bir env kuralı YAZILMADI.
//
// Ortam semantiği (değişmedi, burada açıkça): problemlerin kendi env
// boyutu yok; satır env'e SERVİSİ üzerinden girer. Dış kaynak problemi
// gerçek bir servise çözülmüşse (Subject çözücü → Kind service) o servisin
// env üyeliğiyle eşleşir; çözülmemiş `ext:` öznesi env'e atfedilemez ve
// YALNIZ env seçili değilken görünür — liste, rozet ve çipler aynı cevabı
// verir.

// ProblemCountScope — problems sayımlarının kapsamı.
//
// nil dilim = o eksenden kısıt YOK; non-nil BOŞ dilim = eksen "hiçbir
// servise çözüldü" → yine kısıt (v0.9.219 nil/boş ayrımı).
type ProblemCountScope struct {
	// Exclude — dışlanacak statüler (status NOT IN).
	Exclude []string
	// Team — takım süzgecinin servis kümesi. Yazım v0.9.1358'den beri
	// envScopeConjunct (servissiz + db kaçışı); listede kesin eşleşme Go'da
	// yapılır, sayım bu eksende belgeli olarak hafif şişkin olabilir.
	Team []string
	// Env — global ?env= seçicisinin üye servisleri. Listenin
	// EnvScopeKeepsRow kuralıyla BİREBİR (envScopeConjunct).
	Env []string
}

// problemCountScopeWhere — ProblemCountScope'un TAM WHERE gövdesi ve bağ
// argümanları. Argüman SIRASI sözleşme: statüler, takım üyeleri, env
// üyeleri — yazım sırasıyla aynı. Saf (yalnız probe okunur); table-tested.
func (s *Store) problemCountScopeWhere(sc ProblemCountScope) (string, []any) {
	sql, args := s.problemCountWhere(sc.Exclude, sc.Team)
	if sc.Env != nil {
		sql += " AND " + envScopeConjunct(len(sc.Env), s.hasProblemKindCol)
		args = append(args, toAnySlice(sc.Env)...)
	}
	return sql, args
}
