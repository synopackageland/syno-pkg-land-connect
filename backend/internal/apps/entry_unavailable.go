package apps

import (
	"fmt"
	"html"
	"net/http"
	"strings"
)

// A failed package route is an actionable App error, never an upstream 502
// that looks like a Cloudflare outage. It exposes no private NAS addresses.
func writeEntryUnavailable(w http.ResponseWriter, r *http.Request, entryPath, reason string) {
	messages := map[string][4]string{
		"en":    {"App entry changed", "App is unavailable", "Open Syno Connect Center → App URLs to review the current entry", "Retry"},
		"zh-TW": {"App 入口已變更", "App 目前無法連線", "請開啟 Syno Connect Center → App 網址，確認目前的入口", "重試"},
		"zh-CN": {"App 入口已变更", "App 目前无法连接", "请打开 Syno Connect Center → App 网址，确认当前入口", "重试"},
		"ja":    {"App の入口が変わりました", "App に接続できません", "Syno Connect Center → App URL で現在の入口を確認してください", "再試行"},
		"ko":    {"앱 진입점이 변경됨", "앱에 연결할 수 없음", "Syno Connect Center → 앱 URL에서 현재 진입점을 확인하세요", "다시 시도"},
		"de":    {"App-Zugang geändert", "App nicht erreichbar", "Aktuellen Zugang unter Syno Connect Center → App-URLs prüfen", "Erneut versuchen"},
		"fr":    {"Accès à l’app modifié", "App indisponible", "Ouvrez Syno Connect Center → URL des apps pour vérifier l’accès actuel", "Réessayer"},
		"es":    {"La entrada de la app cambió", "App no disponible", "Abre Syno Connect Center → URL de apps para revisar la entrada actual", "Reintentar"},
		"it":    {"Accesso all’app cambiato", "App non disponibile", "Apri Syno Connect Center → URL app per verificare l’accesso attuale", "Riprova"},
	}
	locale := "en"
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		lang := strings.TrimSpace(strings.Split(part, ";")[0])
		if _, ok := messages[lang]; ok {
			locale = lang
			break
		}
		if strings.HasPrefix(lang, "zh") {
			if strings.Contains(lang, "TW") || strings.Contains(lang, "Hant") {
				locale = "zh-TW"
			} else {
				locale = "zh-CN"
			}
			break
		}
		if len(lang) >= 2 {
			if _, ok := messages[lang[:2]]; ok {
				locale = lang[:2]
				break
			}
		}
	}
	m := messages[locale]
	title := m[1]
	if reason == "entry_changed" || reason == "port_changed" {
		title = m[0]
	}
	retry := "/"
	if strings.HasPrefix(entryPath, "/") {
		retry = entryPath
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", locale)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, `<!doctype html><html lang="%s"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><style>body{font:16px system-ui;margin:40px;line-height:1.6}main{max-width:36rem;margin:auto}button{font:inherit;padding:8px 20px}</style><main><h1>%s</h1><p>%s</p><form method="get" action="%s"><button>%s</button></form></main></html>`, locale, html.EscapeString(title), html.EscapeString(title), html.EscapeString(m[2]), html.EscapeString(retry), html.EscapeString(m[3]))
}
