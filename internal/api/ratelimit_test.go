package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		xri        string
		want       string
	}{
		{
			name:       "direct connection, no headers",
			remoteAddr: "1.2.3.4:50000",
			want:       "1.2.3.4",
		},
		{
			name:       "direct connection ignores XFF (spoof attempt)",
			remoteAddr: "1.2.3.4:50000",
			xff:        "9.9.9.9",
			want:       "1.2.3.4",
		},
		{
			name:       "direct connection ignores X-Real-IP (spoof attempt)",
			remoteAddr: "1.2.3.4:50000",
			xri:        "9.9.9.9",
			want:       "1.2.3.4",
		},
		{
			name:       "loopback proxy trusts XFF single IP",
			remoteAddr: "127.0.0.1:12345",
			xff:        "5.6.7.8",
			want:       "5.6.7.8",
		},
		{
			name:       "loopback proxy takes first IP from XFF list",
			remoteAddr: "127.0.0.1:12345",
			xff:        "5.6.7.8, 10.0.0.1, 172.16.0.1",
			want:       "5.6.7.8",
		},
		{
			name:       "loopback proxy trusts X-Real-IP when no XFF",
			remoteAddr: "127.0.0.1:12345",
			xri:        "5.6.7.8",
			want:       "5.6.7.8",
		},
		{
			name:       "loopback proxy prefers XFF over X-Real-IP",
			remoteAddr: "127.0.0.1:12345",
			xff:        "5.6.7.8",
			xri:        "9.9.9.9",
			want:       "5.6.7.8",
		},
		{
			name:       "loopback proxy with no headers falls back to loopback",
			remoteAddr: "127.0.0.1:12345",
			want:       "127.0.0.1",
		},
		{
			name:       "IPv6 loopback proxy trusts XFF",
			remoteAddr: "[::1]:12345",
			xff:        "5.6.7.8",
			want:       "5.6.7.8",
		},
		{
			name:       "IPv6 client ignores XFF",
			remoteAddr: "[2001:db8::1]:50000",
			xff:        "9.9.9.9",
			want:       "2001:db8::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.xri != "" {
				r.Header.Set("X-Real-IP", tt.xri)
			}
			got := clientIP(r)
			if got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
