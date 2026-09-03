// Copyright Red Hat, Inc.
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"net/http"
	"strings"
)

// debugPprofMiddleware rejects Go net/http/pprof debug endpoints before delegating
// to the wrapped handler.
type debugPprofMiddleware struct {
	next http.Handler
}

// ServeHTTP implements http.Handler.
func (m debugPprofMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/debug/pprof") {
		http.NotFound(w, r)
		return
	}
	m.next.ServeHTTP(w, r)
}

// BlockDebugPprof returns middleware that rejects /debug/pprof on operator-owned
// HTTP servers. Custom ServeMux handlers do not register pprof routes, but
// transitive dependencies may link net/http/pprof; this middleware ensures those
// endpoints are never served on operator listen ports.
func BlockDebugPprof(next http.Handler) http.Handler {
	return debugPprofMiddleware{next: next}
}
