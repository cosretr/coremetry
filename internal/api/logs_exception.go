package api

// logs_exception.go — v0.10.1880 (Log Search Faz 3 dilim 3.3). /logs satırlarına
// exception GÖSTERİM özeti (logstore.SummarizeException): yalnız ES log yolunda
// (operatör 2026-11-06: prod logları ES'te; CH log yolu bugünkü gibi — özet yok).
// Gruplama kimliği / exception_groups / dedektörler DEĞİŞMEZ. Rota yok.

import "github.com/cilcenk/coremetry/internal/logstore"

func (s *Server) attachLogExceptions(page *logstore.Page) {
	if page == nil || s.logs == nil || s.logs.Backend() != "elasticsearch" {
		return
	}
	logstore.AttachExceptions(page.Logs)
}
