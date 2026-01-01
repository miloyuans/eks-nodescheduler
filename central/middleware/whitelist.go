// central/middleware/whitelist.go
package middleware

import (
	"net"
	"net/http"

	"golang.org/x/net/ipnet"
)

type Whitelist struct {
	nets []*ipnet.IPNet
}

func NewWhitelist(cidrs []string) *Whitelist {
	w := &Whitelist{}
	for _, c := range cidrs {
		_, n, err := ipnet.ParseCIDR(c)
		if err != nil {
			continue
		}
		w.nets = append(w.nets, n)
	}
	return w
}

func (w *Whitelist) Allow(ip net.IP) bool {
	if len(w.nets) == 0 {
		return true // 空列表允许所有
	}
	for _, n := range w.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (w *Whitelist) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		ipStr, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(ipStr)
		if !w.Allow(ip) {
			http.Error(rw, "IP not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(rw, r)
	})
}