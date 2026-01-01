// central/middleware/whitelist.go
package middleware

import (
	"net"
	"net/http"
)

type Whitelist struct {
	nets []*net.IPNet
}

func New(cidrs []string) *Whitelist {
	w := &Whitelist{}
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			continue
		}
		w.nets = append(w.nets, n)
	}
	return w
}

func (w *Whitelist) Allow(ip net.IP) bool {
	if len(w.nets) == 0 {
		return true
	}
	for _, n := range w.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (w *Whitelist) HTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		ipStr, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(ipStr)
		if !w.Allow(ip) {
			http.Error(rw, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(rw, r)
	})
}