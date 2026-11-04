package providers

import (
	"net/http"
	"net/url"
	"time"
)

func HTTPClient(c Config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if c.ProxyURL != "" {
		if u, err := url.Parse(c.ProxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 60 * time.Second
	seconds := c.TimeoutSeconds
	if seconds == 0 {
		seconds = 180
	}
	return &http.Client{Transport: transport, Timeout: time.Duration(seconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
