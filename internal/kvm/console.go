package kvm

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strconv"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/gorilla/websocket"
)

// bmcWebPort is the HTTPS port of the Supermicro BMC web UI / KVM WebSocket.
const bmcWebPort = "443"

// insecureTransport reaches BMC web UIs (self-signed certs). Shared, concurrent-safe.
var insecureTransport = &http.Transport{
	//nolint:gosec // BMC web UIs use self-signed certificates.
	TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
	ForceAttemptHTTP2:   false,
	MaxIdleConnsPerHost: 4,
}

// headInjectRe finds the opening <head> tag to splice the WebSocket-redirect script.
var headInjectRe = regexp.MustCompile(`(?i)<head[^>]*>`)

// bootstrapWSDialer connects to the BMC's KVM WebSocket as a client.
var bootstrapWSDialer = &websocket.Dialer{
	//nolint:gosec // BMC web UIs use self-signed certificates.
	TLSClientConfig:  &tls.Config{InsecureSkipVerify: true},
	Subprotocols:     []string{"binary", "base64"},
	HandshakeTimeout: 12 * time.Second,
}

var kvmUpgrader = websocket.Upgrader{
	Subprotocols:    []string{"binary"},
	ReadBufferSize:  16 * 1024,
	WriteBufferSize: 16 * 1024,
	CheckOrigin:     func(*http.Request) bool { return true }, // same-origin via the gateway proxy
}

// handleConsoleProxy reverse-proxies the BMC's HTML5 KVM assets under our origin
// at /bmc/{id}/..., injecting the authenticated SID cookie. The H5Viewer uses
// relative asset paths, so everything resolves back through this prefix.
func (s *Service) handleConsoleProxy(w http.ResponseWriter, r *http.Request) {
	kt, ok := s.tokenFor(r)
	if !ok {
		http.Error(w, "invalid or expired KVM session", http.StatusForbidden)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	sid, err := s.sessions.SID(ctx, kt.target)
	if err != nil {
		s.log.Warnf("console login %s: %v", kt.target.Host, err)
		http.Error(w, "could not log in to BMC", http.StatusBadGateway)
		return
	}

	// Persist the token as a cookie so the viewer's relative asset/WS requests
	// (which carry no query param) stay authorized.
	if r.URL.Query().Get("kvmtoken") != "" {
		http.SetCookie(w, &http.Cookie{
			Name: kvmCookie, Value: r.URL.Query().Get("kvmtoken"),
			Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})
	}

	upstreamPath := "/" + r.PathValue("path")
	host := kt.target.Host

	proxy := &httputil.ReverseProxy{
		Transport: insecureTransport,
		Director: func(req *http.Request) {
			req.URL.Scheme = "https"
			req.URL.Host = host
			req.URL.Path = upstreamPath
			req.Host = host
			req.Header.Set("Cookie", "SID="+sid)
			req.Header.Set("Accept-Encoding", "identity") // uncompressed so we can splice
			req.Header.Del("Referer")
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("X-Frame-Options")
			if isBootstrapResponse(resp) {
				return injectWSRedirect(resp)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			s.log.Warnf("console proxy %s: %v", host, err)
			http.Error(w, "BMC console unreachable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// isBootstrapResponse reports whether this is the H5Viewer bootstrap HTML.
func isBootstrapResponse(resp *http.Response) bool {
	if resp.Request == nil {
		return false
	}
	return resp.Request.URL.Path == "/cgi/url_redirect.cgi" &&
		bytes.Contains([]byte(resp.Request.URL.RawQuery), []byte("man_ikvm_html5_bootstrap"))
}

// injectWSRedirect rewrites the H5Viewer's WebSocket so it connects back through
// our proxy's __kvmws endpoint, computed *relative to the current page* so it is
// agnostic to the gateway's public path prefix (/modules/ipam/...).
func injectWSRedirect(resp *http.Response) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()

	const script = `<head><script>(function(){var O=window.WebSocket;` +
		`var dir=location.pathname.replace(/[^/]*$/,'');` + // .../bmc/{id}/cgi/
		`var wsPath=new URL('../__kvmws',location.origin+dir).pathname;` + // .../bmc/{id}/__kvmws
		`var P=(location.protocol==='https:'?'wss:':'ws:')+'//'+location.host+wsPath;` +
		`function W(u,p){return new O(P,p);}W.prototype=O.prototype;` +
		`W.CONNECTING=O.CONNECTING;W.OPEN=O.OPEN;W.CLOSING=O.CLOSING;W.CLOSED=O.CLOSED;` +
		`window.WebSocket=W;})();</script>`

	if headInjectRe.Match(body) {
		body = headInjectRe.ReplaceAll(body, []byte(script))
	} else {
		body = append([]byte(script), body...)
	}

	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Header.Del("Content-Encoding")
	return nil
}

// handleConsoleWS proxies the browser's KVM WebSocket to the BMC's wss://host:443/
// endpoint, attaching the authenticated SID cookie. The RFB/AST2100 stream is
// relayed transparently both ways.
func (s *Service) handleConsoleWS(w http.ResponseWriter, r *http.Request) {
	kt, ok := s.tokenFor(r)
	if !ok {
		http.Error(w, "invalid or expired KVM session", http.StatusForbidden)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 14*time.Second)
	defer cancel()
	sid, err := s.sessions.SID(ctx, kt.target)
	if err != nil {
		s.log.Warnf("console ws login %s: %v", kt.target.Host, err)
		http.Error(w, "could not log in to BMC", http.StatusBadGateway)
		return
	}

	upstreamURL := "wss://" + kt.target.Host + ":" + bmcWebPort + "/"
	hdr := http.Header{
		"Cookie": {"SID=" + sid},
		"Origin": {"https://" + kt.target.Host},
	}
	upstream, resp, err := bootstrapWSDialer.DialContext(ctx, upstreamURL, hdr)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			s.sessions.Invalidate(kt.target)
		}
		s.log.Warnf("console ws dial %s: %v", kt.target.Host, err)
		http.Error(w, "could not open BMC console stream", http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	client, err := kvmUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warnf("console ws upgrade %s: %v", kt.target.Host, err)
		return
	}

	// Clear any deadline the http.Server set on the hijacked connections so the
	// long-lived KVM stream is not killed by an inherited read/write timeout.
	for _, c := range []*websocket.Conn{client, upstream} {
		_ = c.SetReadDeadline(time.Time{})
		_ = c.SetWriteDeadline(time.Time{})
	}

	s.log.Infof("KVM console opened for device %s (%s)", kt.deviceID, kt.target.Host)
	s.proxyWebSocket(client, upstream)
	s.log.Infof("KVM console closed for device %s", kt.deviceID)
}

// proxyWebSocket relays messages between the browser and the BMC until either
// closes, keeping the browser leg alive with periodic pings (it traverses
// nginx + the admin gateway, which would otherwise idle it out).
func (s *Service) proxyWebSocket(browser, bmc *websocket.Conn) {
	done := make(chan string, 2)
	go copyWebSocket(bmc, browser, "browser→bmc", done, s.log)
	go copyWebSocket(browser, bmc, "bmc→browser", done, s.log)

	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				// WriteControl is safe concurrently with WriteMessage.
				_ = browser.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			}
		}
	}()

	first := <-done
	close(stop)
	_ = browser.Close()
	_ = bmc.Close()
	<-done
	s.log.Infof("KVM relay closed (first side to error: %s)", first)
}

func copyWebSocket(dst, src *websocket.Conn, label string, done chan string, log *log.Helper) {
	for {
		mt, data, err := src.ReadMessage()
		if err != nil {
			log.Infof("KVM relay %s read end: %v", label, err)
			done <- label
			return
		}
		if err := dst.WriteMessage(mt, data); err != nil {
			log.Infof("KVM relay %s write end: %v", label, err)
			done <- label
			return
		}
	}
}
