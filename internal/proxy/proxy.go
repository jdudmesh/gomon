package proxy

// gomon is a simple command line tool that watches your files and automatically restarts the application when it detects any changes in the working directory.
// Copyright (C) 2023 John Dudmesh

// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/notification"
	"github.com/r3labs/sse/v2"
	log "github.com/sirupsen/logrus"
)

const gomonInjectCode = `
<script>
	const source = new EventSource('/__gomon__/events?stream=hmr');
	source.onmessage = function (event) {
		console.log('reloading...', event);
		source.close();
		window.location.reload();
	};
</script>`

const headTag = `<head>`

type webProxy struct {
	isEnabled         bool
	port              int
	downstreamHost    string
	downstreamTimeout time.Duration
	httpServer        *http.Server
	sseServer         *sse.Server
	sseServerLock     sync.Mutex
	injectCode        string
}

func New(cfg config.Config) (*webProxy, error) {
	proxy := &webProxy{
		isEnabled:         cfg.Proxy.Enabled,
		port:              cfg.Proxy.Port,
		downstreamHost:    cfg.Proxy.Downstream.Host,
		downstreamTimeout: time.Duration(cfg.Proxy.Downstream.Timeout) * time.Second,
		sseServerLock:     sync.Mutex{},
	}

	err := proxy.initProxy()
	if err != nil {
		return nil, err
	}

	return proxy, nil
}
func (p *webProxy) Enabled() bool {
	return p.isEnabled
}

func (p *webProxy) initProxy() error {
	if !p.isEnabled && p.port == 0 {
		return nil
	}

	if p.port == 0 {
		p.port = 4000
		p.isEnabled = true
	}

	if p.downstreamHost == "" {
		return errors.New("downstream host:port is required")
	}

	if !strings.HasPrefix(p.downstreamHost, "http") {
		p.downstreamHost = "http://" + p.downstreamHost
	}

	if p.downstreamTimeout == 0 {
		p.downstreamTimeout = 5
	}

	p.injectCode = gomonInjectCode

	p.sseServer = sse.New()
	p.sseServer.AutoReplay = false
	p.sseServer.CreateStream("hmr")

	mux := http.NewServeMux()
	mux.HandleFunc("/__gomon__/reload", p.handleReload)
	mux.HandleFunc("/__gomon__/events", p.sseServer.ServeHTTP)

	downstreamURL, err := url.Parse(p.downstreamHost)
	if err != nil {
		return fmt.Errorf("downstream host: %v", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(downstreamURL)
	proxy.ModifyResponse = p.proxyRequest

	mux.HandleFunc("/", proxy.ServeHTTP)

	p.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", p.port),
		Handler: mux,
	}

	return nil
}

func (p *webProxy) Start() error {
	log.Infof("proxy server running on http://localhost:%d", p.port)
	err := p.httpServer.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		panic(fmt.Sprintf("proxy server shut down unexpectedly: %v", err))
	}
	return nil
}

func (p *webProxy) Close() error {
	if p.sseServer != nil {
		p.sseServer.Close()
	}

	if p.httpServer != nil {
		return p.httpServer.Shutdown(context.Background())
	}

	return nil
}

func (p *webProxy) Notify(n notification.Notification) {
	if !p.isEnabled {
		return
	}
	p.sseServerLock.Lock()
	defer p.sseServerLock.Unlock()

	switch n.Type {
	case notification.NotificationTypeHardRestart, notification.NotificationTypeSoftRestart, notification.NotificationTypeIPC:
		log.Infof("notifying browser: %s", n.Message)
		p.sseServer.Publish("hmr", &sse.Event{
			Data: []byte(n.Message),
		})
	}
}

func (p *webProxy) handleReload(res http.ResponseWriter, req *http.Request) {
	log.Infof("reloading proxy")
	res.WriteHeader(http.StatusOK)
}

func (p *webProxy) proxyRequest(res *http.Response) error {
	isHtml := strings.HasPrefix(res.Header.Get("Content-Type"), "text/html")
	if !isHtml {
		return nil
	}

	outBuf := bytes.Buffer{}
	inBuf, err := io.ReadAll(res.Body)
	if err != nil {
		log.Errorf("reading request body: %v", err)
		return err
	}

	ix := 0
	match := false
	for {
		if ix >= len(inBuf) {
			break
		}

		if inBuf[ix] == '<' {
			// check if we have a match
			match = true
			for jx := 0; jx < len(headTag); jx++ {
				if ix+jx >= len(inBuf) || inBuf[ix+jx] != headTag[jx] {
					match = false
					break
				}
			}

			if match {
				cutPos := ix + len(headTag)
				// we have a match, inject the code
				_, err = outBuf.Write(inBuf[:cutPos])
				if err != nil {
					log.Errorf("writing response: %v", err)
					return err
				}
				_, err = outBuf.Write([]byte(p.injectCode))
				if err != nil {
					log.Errorf("writing response: %v", err)
					return err
				}
				_, err = outBuf.Write(inBuf[cutPos:])
				if err != nil {
					log.Errorf("writing response: %v", err)
					return err
				}
				break
			}
		}

		ix++
	}

	if !match {
		_, err = outBuf.Write(inBuf)
		if err != nil {
			log.Errorf("writing response: %v", err)
			return err
		}
	}

	res.Body = io.NopCloser(&outBuf)
	res.Header["Content-Length"] = []string{fmt.Sprint(outBuf.Len())}

	return nil
}
