package identity

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

func proxyNetwork(v string) (*net.IPNet, error) {
	if ip := net.ParseIP(v); ip != nil {
		bits := 128
		if ip.To4() != nil {
			ip = ip.To4()
			bits = 32
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
	}
	_, network, err := net.ParseCIDR(v)
	return network, err
}
func (s *Service) validateTrustedProxies() error {
	for _, v := range s.config.TrustedProxies {
		if _, err := proxyNetwork(v); err != nil {
			return fmt.Errorf("invalid trusted proxy %q", v)
		}
	}
	return nil
}
func (s *Service) trustedProxy(ip net.IP) bool {
	for _, v := range s.config.TrustedProxies {
		n, err := proxyNetwork(v)
		if err == nil && n.Contains(ip) {
			return true
		}
	}
	return false
}
func (s *Service) clientIP(r *http.Request) string {
	value := r.RemoteAddr
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	peer := net.ParseIP(value)
	if peer == nil {
		return ""
	}
	if !s.trustedProxy(peer) {
		return peer.String()
	}
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
			return ip.String()
		}
		return peer.String()
	}
	parts := strings.Split(forwarded, ",")
	if len(forwarded) > 1024 || len(parts) > 16 {
		return peer.String()
	}
	ips := make([]net.IP, len(parts))
	for i, v := range parts {
		ips[i] = net.ParseIP(strings.TrimSpace(v))
		if ips[i] == nil {
			return peer.String()
		}
	}
	current := peer
	for i := len(ips) - 1; i >= 0 && s.trustedProxy(current); i-- {
		current = ips[i]
	}
	return current.String()
}

func (s *Service) allowAccountAttempt(r *http.Request) bool {
	// Reuse the storage limiter with the same validated client IP as the
	// registration record. A trusted proxy must not group all users together.
	if ip := s.clientIP(r); ip != "" {
		request := r.Clone(r.Context())
		request.RemoteAddr = net.JoinHostPort(ip, "0")
		return s.store.Allow(request)
	}
	return s.store.Allow(r)
}
