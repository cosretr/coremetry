package chstore

import (
	"context"
	"time"
)

// LogTemplate is one Drain-extracted log shape with its running
// observation stats. Persisted via ReplacingMergeTree(version)
// so repeated batch upserts from the templater puller fold
// into one row keyed by template_id.
type LogTemplate struct {
	ID            string   `json:"id"`
	Template      string   `json:"template"`
	FirstSeen     int64    `json:"firstSeen"` // unix ns
	LastSeen      int64    `json:"lastSeen"`
	TotalCount    uint64   `json:"totalCount"`
	Services      []string `json:"services"`
	ExceptionType string   `json:"exceptionType,omitempty"`
	Sample        string   `json:"sample"`
}

// UpsertLogTemplate writes (or refreshes) one template row.
// ReplacingMergeTree(version) picks the highest version on
// merge so the latest batch wins. Caller supplies template_id;
// we keep first_seen sticky (never decrease) by reading the
// existing row before writing, similar to how
// UpsertAnomalyEvent guards started_at.
func (s *Store) UpsertLogTemplate(ctx context.Context, t LogTemplate) error {
	// Sticky first_seen — read the existing row's first_seen so
	// a re-observation of an old template doesn't reset its
	// "since when" marker. ReplacingMergeTree wouldn't on its
	// own protect against this — we'd just keep whichever value
	// the latest version carried. The lookup is keyed on the
	// indexed column so it's cheap.
	var existingFirst time.Time
	_ = s.conn.QueryRow(ctx,
		`SELECT first_seen FROM log_templates FINAL WHERE id = ? LIMIT 1`,
		t.ID,
	).Scan(&existingFirst)
	first := time.Unix(0, t.FirstSeen).UTC()
	if !existingFirst.IsZero() && existingFirst.Before(first) {
		first = existingFirst
	}
	last := time.Unix(0, t.LastSeen).UTC()

	batch, err := s.conn.PrepareBatch(ctx,
		`INSERT INTO log_templates (id, template, first_seen, last_seen,
			total_count, services, exception_type, sample, version)`)
	if err != nil {
		return err
	}
	services := t.Services
	if services == nil {
		services = []string{}
	}
	if err := batch.Append(
		t.ID, t.Template, first, last, t.TotalCount,
		services, t.ExceptionType, t.Sample,
		uint64(time.Now().UnixNano()),
	); err != nil {
		return err
	}
	return batch.Send()
}

// ListLogTemplatesFilter narrows the read by recency and sort
// order. SinceNs filters last_seen ≥ X; Limit caps the response.
type ListLogTemplatesFilter struct {
	SinceNs int64
	// "first_seen" | "first_seen_asc" (v0.10.1030) | "last_seen" | "count"
	// (default "count").
	SortBy string
	Limit  int
	// Service — v0.10.310 (/logs Şablonlar sekmesi): has(services, ?).
	// Boş = tüm servisler.
	Service string

	// ── v0.10.1030 ("yeni log şablonu" seli) — dedektörün aday ve
	// bilinen-şablon okumaları. Hepsinin sıfır değeri = kısıt yok, yani
	// mevcut çağıranlar bayt-bayt eski SQL'i üretir. Filtreler SQL'de, çünkü
	// sel anında pencere içi doğumlar yüzlerce satır olabilir; Go'da süzmek
	// LIMIT-sonrası süzgeç sınıfı (audit CHECK 8).
	//
	// FirstSeenSinceNs / FirstSeenBeforeNs: first_seen >= X / first_seen < X.
	// Bind NANOSANİYE kesin (fromUnixTimestamp64Nano, kolon DateTime64(9)):
	// clickhouse-go konumsal time.Time'ı SANİYEYE keserdi, aynı X ile kurulan
	// aday ve bilinen kümeleri o zaman tam tümleyen olmazdı.
	FirstSeenSinceNs  int64
	FirstSeenBeforeNs int64
	// MinTotalCount: total_count >= X (0 = kısıt yok).
	MinTotalCount uint64
	// AnyServices: hasAny(services, [...]) — tik başına TEK okumada birden
	// çok servisin şablonları. IncludeServiceless: servissiz satırları da al
	// (AnyServices ile VEYA'lanır; AnyServices boşken YALNIZ servissiz
	// satırlar). İkisi de sıfırsa servis kısıtı yok (ListAnomalyEventsFilter
	// .Services'in "boş = hiçbir şey" sözleşmesinden FARKLI).
	AnyServices        []string
	IncludeServiceless bool
	// TokenCounts: şablonun belirteç sayısı bu kümede. KESİN: saklanan şablon
	// her zaman strings.Join(belirteçler, " ") (templater puller; Tokenize ve
	// TemplateString v0.5.244'ten beri aynı, tek yazıcı UpsertLogTemplate) ve
	// belirteçler boş değil, boşluk/sekme içermez → boşluk sayısı + 1 =
	// belirteç sayısı (templater/family_test.go sabitler). countSubstrings,
	// splitByChar'ın aksine dizi kurmaz.
	TokenCounts []int
}

// logTemplatesWhere — v0.10.310: WHERE + ORDER BY saf kurucu (tablo testi
// log_templates_test.go). Bilinmeyen sort → total_count (eski davranış).
func logTemplatesWhere(f ListLogTemplatesFilter) (string, []any) {
	order := "total_count DESC"
	switch f.SortBy {
	case "first_seen":
		order = "first_seen DESC"
	case "first_seen_asc":
		// v0.10.1030 — aday okuması: tavanı en ERKEN doğanlar (aile
		// temsilcileri) atlatsın.
		order = "first_seen ASC"
	case "last_seen":
		order = "last_seen DESC"
	}
	args := []any{}
	wc := "WHERE 1"
	if f.SinceNs > 0 {
		wc += " AND last_seen >= ?"
		args = append(args, time.Unix(0, f.SinceNs).UTC())
	}
	if f.Service != "" {
		wc += " AND has(services, ?)"
		args = append(args, f.Service)
	}
	if f.FirstSeenSinceNs > 0 {
		wc += " AND first_seen >= fromUnixTimestamp64Nano(?)"
		args = append(args, f.FirstSeenSinceNs)
	}
	if f.FirstSeenBeforeNs > 0 {
		wc += " AND first_seen < fromUnixTimestamp64Nano(?)"
		args = append(args, f.FirstSeenBeforeNs)
	}
	if f.MinTotalCount > 0 {
		wc += " AND total_count >= ?"
		args = append(args, f.MinTotalCount)
	}
	// Dilim TEK bind argümanıdır: clickhouse-go onu ['a', 'b'] dizi
	// değişmezi olarak biçimler.
	switch {
	case len(f.AnyServices) > 0 && f.IncludeServiceless:
		wc += " AND (hasAny(services, ?) OR empty(services))"
		args = append(args, f.AnyServices)
	case len(f.AnyServices) > 0:
		wc += " AND hasAny(services, ?)"
		args = append(args, f.AnyServices)
	case f.IncludeServiceless:
		wc += " AND empty(services)"
	}
	if len(f.TokenCounts) > 0 {
		wc += " AND (countSubstrings(template, ' ') + 1) IN (" + chPlaceholders(len(f.TokenCounts)) + ")"
		for _, n := range f.TokenCounts {
			args = append(args, n)
		}
	}
	return wc + "\n\t\tORDER BY " + order, args
}

// ListLogTemplates returns the persisted templates ordered by
// the requested signal. The "spike" sort is computed in the
// API layer because it needs both the 1h and 24h counts which
// don't live on the row.
func (s *Store) ListLogTemplates(ctx context.Context, f ListLogTemplatesFilter) ([]LogTemplate, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	wo, args := logTemplatesWhere(f)
	args = append(args, f.Limit)
	rows, err := s.conn.Query(ctx, `
		SELECT id, template,
		       toUnixTimestamp64Nano(first_seen),
		       toUnixTimestamp64Nano(last_seen),
		       total_count, services, exception_type, sample
		FROM log_templates FINAL
		`+wo+`
		LIMIT ?
		SETTINGS max_execution_time = 5`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogTemplate{}
	for rows.Next() {
		var t LogTemplate
		if err := rows.Scan(&t.ID, &t.Template, &t.FirstSeen, &t.LastSeen,
			&t.TotalCount, &t.Services, &t.ExceptionType, &t.Sample); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
