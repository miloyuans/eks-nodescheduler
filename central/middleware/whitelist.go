// central/middleware/whitelist.go
package middleware

import (
	"context"
	"log"
	"net"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type Whitelist struct {
	nets []*net.IPNet
}

func New(cidrs []string) *Whitelist {
	w := &Whitelist{}
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			log.Printf("Invalid CIDR %s: %v", c, err)
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

func (w *Whitelist) GRPC(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.PermissionDenied, "No peer")
	}
	ipStr, _, _ := net.SplitHostPort(p.Addr.String())
	ip := net.ParseIP(ipStr)
	if !w.Allow(ip) {
		return nil, status.Error(codes.PermissionDenied, "Forbidden")
	}
	return handler(ctx, req)
}