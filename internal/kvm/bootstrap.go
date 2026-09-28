package kvm

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// consoleEntry is where a console starts: the BMC's HTML5 KVM viewer (the
// page its web UI opens for "Launch Console"), past its login page. The
// server-side login already happened.
const consoleEntry = "cgi/url_redirect.cgi?url_name=man_ikvm_html5_bootstrap"

// maxInjectBytes bounds the HTML page buffered for the bootstrap splice; a
// larger page streams through unchanged.
const maxInjectBytes = 4 << 20

// headTag is the opening <head> tag (not <header>).
var headTag = regexp.MustCompile(`(?i)<head(?:\s[^>]*)?>`)

// injectBootstrap splices the console bootstrap script right after <head> of
// an HTML page the BMC served, so it runs before the page's own scripts
// (ported from v3; once per page, only for text/html).
//
// The script
//   - seeds sessionStorage _x_auth / _sess_idx with the Redfish session, the
//     way the BMC's login page does after its own Redfish login: the BMC UI
//     reads the token from there for its client-side Redfish calls. Handing
//     the token to the page is acceptable: it is the BMC's own session token
//     in the BMC's own UI, it lives only on the isolated console origin
//     (portal feature 025, never the portal origin), it is bound to this
//     console's BMC session and deleted with it, and it is not the BMC
//     password (which never leaves the server). The proxy sets the current
//     token on every upstream request anyway, so a stale copy is harmless;
//   - keeps the page's absolute-path XHR/fetch calls ("/redfish/v1/...",
//     which redfish.js builds from location.host) under this console's
//     /bmc/{id}/ prefix, the only paths the console origin serves;
//   - sends the viewer's WebSocket through the /bmc/{id}/__kvmws relay,
//     passing the path it asked for.
func injectBootstrap(resp *http.Response, auth bmcAuth) error {
	if resp.Request != nil && resp.Request.Method == http.MethodHead {
		return nil
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt != "text/html" {
		return nil
	}
	if ce := resp.Header.Get("Content-Encoding"); ce != "" && !strings.EqualFold(ce, "identity") {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxInjectBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxInjectBytes {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), resp.Body), resp.Body}
		return nil
	}
	_ = resp.Body.Close()
	script := []byte(bootstrapScript(auth))
	at := 0
	if loc := headTag.FindIndex(body); loc != nil {
		at = loc[1]
	}
	out := make([]byte, 0, len(body)+len(script))
	out = append(append(append(out, body[:at]...), script...), body[at:]...)
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return nil
}

// bootstrapScript is the injected <script>. String values are JSON-encoded,
// which escapes <, > and & (no way out of the script element).
func bootstrapScript(auth bmcAuth) string {
	seed := ""
	if auth.token != "" {
		tok, _ := json.Marshal(auth.token)
		idx, _ := json.Marshal(auth.sessionID)
		seed = `try{sessionStorage.setItem('_x_auth',` + string(tok) + `);sessionStorage.setItem('_sess_idx',` + string(idx) + `);}catch(e){}`
	}
	return `<script>(function(){` + seed +
		`var m=location.pathname.match(/^(.*?\/bmc\/[^\/]+)\//);if(!m)return;var B=m[1];` +
		`function fix(u){try{var x=new URL(u,location.href);if(x.origin===location.origin&&x.pathname.indexOf(B+'/')!==0){x.pathname=B+x.pathname;return x.href;}}catch(e){}return u;}` +
		`var X=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(){if(arguments.length>1){arguments[1]=fix(String(arguments[1]));}return X.apply(this,arguments);};` +
		`if(window.fetch){var F=window.fetch;window.fetch=function(i,o){if(typeof i==='string'||i instanceof URL){i=fix(String(i));}return F.call(this,i,o);};}` +
		`var O=window.WebSocket;if(O){var W=function(u,p){var t='/';try{var x=new URL(String(u),location.href);t=x.pathname;if(t.indexOf(B+'/')===0){t=t.slice(B.length);}t+=x.search;}catch(e){}` +
		`var P=(location.protocol==='https:'?'wss:':'ws:')+'//'+location.host+B+'/__kvmws?u='+encodeURIComponent(t);return p===undefined?new O(P):new O(P,p);};` +
		`W.prototype=O.prototype;W.CONNECTING=O.CONNECTING;W.OPEN=O.OPEN;W.CLOSING=O.CLOSING;W.CLOSED=O.CLOSED;window.WebSocket=W;}` +
		`})();</script>`
}
