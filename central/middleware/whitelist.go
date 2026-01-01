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
	nets []*net.IPNet  // 使用标准库 net.IPNet
}

func New(cidrs []string) *Whitelist {
	w := &Whitelist{}
	for _, c := range cidrs {
		// 使用标准库 net.ParseCIDR，无需 x/net/ipnet！
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
		return true  // 空白名单允许所有
	}
	for _, n := range w.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// HTTP 中间件
func (w *Whitelist) HTTP(next http.Handler) http.Handler {
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

// gRPC 拦截器
func (w *Whitelist) GRPCUnary(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.PermissionDenied, "No peer info")
	}
	ipStr, _, _ := net.SplitHostPort(p.Addr.String())
	ip := net.ParseIP(ipStr)
	if !w.Allow(ip) {
		return nil, status.Error(codes.PermissionDenied, "IP not allowed")
	}
	return handler(ctx, req)
}