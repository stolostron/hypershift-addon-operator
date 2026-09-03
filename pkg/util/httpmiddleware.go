package util

import (
	"net/http"
	"strings"
)

// BlockDebugPprof rejects Go net/http/pprof debug endpoints on operator-owned
// HTTP servers. Custom ServeMux handlers do not register pprof routes, but
// transitive dependencies may link net/http/pprof; this middleware ensures
// those endpoints are never served on operator listen ports.
func BlockDebugPprof(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/debug/pprof") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
