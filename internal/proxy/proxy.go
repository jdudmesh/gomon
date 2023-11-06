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
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jdudmesh/gomon/internal/config"
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
	enabled           bool
	port              int
	downstreamHost    string
	downstreamTimeout time.Duration
	eventSink         chan string
	httpServer        *http.Server
	sseServer         *sse.Server
	injectCode        string
}

func New(cfg *config.Config) (*webProxy, error) {
	proxy := &webProxy{
		enabled:           cfg.Proxy.Enabled,
		port:              cfg.Proxy.Port,
		downstreamHost:    cfg.Proxy.Downstream.Host,
		downstreamTimeout: time.Duration(cfg.Proxy.Downstream.Timeout) * time.Second,
		eventSink:         make(chan string),
	}

	err := proxy.initProxy()
	if err != nil {
		return nil, err
	}

	return proxy, nil
}

func (p *webProxy) initProxy() error {
	if !p.enabled && p.port == 0 {
		return nil
	}

	if p.port == 0 {
		p.port = 4000
		p.enabled = true
	}

	if p.downstreamHost == "" {
		return errors.New("downstream host:port is required")
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
	mux.HandleFunc("/", p.handleDefault)

	p.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", p.port),
		Handler: mux,
	}

	return nil
}

func (p *webProxy) Start() error {
	if !p.enabled {
		return nil
	}

	log.Infof("proxy server running on http://localhost:%d", p.port)
	go func() {
		err := p.httpServer.ListenAndServe()
		log.Infof("shutting down proxy server: %v", err)
	}()

	go func() {
		for msg := range p.eventSink {
			log.Infof("notifying browser: %s", msg)
			p.sseServer.Publish("hmr", &sse.Event{
				Data: []byte(msg),
			})
		}
	}()

	return nil
}

func (p *webProxy) Close() error {
	if p.sseServer != nil {
		p.sseServer.Close()
	}

	if p.httpServer != nil {
		return p.httpServer.Shutdown(context.Background())
	}

	close(p.eventSink)

	return nil
}

func (p *webProxy) EventSink() chan string {
	return p.eventSink
}

func (p *webProxy) handleReload(res http.ResponseWriter, req *http.Request) {
	log.Infof("reloading proxy")
	res.WriteHeader(http.StatusOK)
}

func (p *webProxy) handleDefault(res http.ResponseWriter, req *http.Request) {
	duration := time.Duration(p.downstreamTimeout) * time.Second // TODO: calculate at startup
	p.proxyRequest(res, req, p.downstreamHost, duration, p.injectCode)
}

func (p *webProxy) proxyRequest(res http.ResponseWriter, req *http.Request, host string, timeout time.Duration, injectCode string) {
	ctx, closeFn := context.WithTimeout(req.Context(), timeout)
	defer closeFn()

	nextURL := req.URL
	nextURL.Scheme = "http"
	nextURL.Host = host
	nextURL.Path = req.URL.Path
	nextURL.RawQuery = req.URL.RawQuery

	nextReq, err := http.NewRequestWithContext(ctx, req.Method, nextURL.String(), req.Body)
	if err != nil {
		log.Errorf("creating request: %v", err)
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}

	for k, v := range req.Header {
		nextReq.Header.Add(k, strings.Join(v, " "))
	}

	nextRes, err := http.DefaultClient.Do(nextReq)
	if err != nil {
		log.Errorf("proxying request: %v", err)
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}
	defer nextRes.Body.Close()

	nextRes.Header.Del("Content-Length")
	nextRes.Header.Del("Cache-Control")
	for k, v := range nextRes.Header {
		res.Header()[k] = v
	}
	res.Header()["Cache-Control"] = []string{"no-cache, no-store, no-transform, must-revalidate"}

	res.WriteHeader(nextRes.StatusCode)

	// TODO: assuming that we can fit the whole response body into memory, probably not a good idea, fix it later
	buffer, err := io.ReadAll(nextRes.Body)
	if err != nil {
		log.Errorf("reading request body: %v", err)
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}

	isHtml := strings.HasPrefix(nextRes.Header.Get("Content-Type"), "text/html")
	if !isHtml || len(injectCode) == 0 {
		_, err = res.Write(buffer)
		if err != nil {
			log.Errorf("writing response: %v", err)
			http.Error(res, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	ix := 0
	match := false
	for {
		if ix >= len(buffer) {
			break
		}

		if buffer[ix] == '<' {
			// check if we have a match
			match = true
			for jx := 0; jx < len(headTag); jx++ {
				if ix+jx >= len(buffer) || buffer[ix+jx] != headTag[jx] {
					match = false
					break
				}
			}

			if match {
				cutPos := ix + len(headTag)
				// we have a match, inject the code
				_, err = res.Write(buffer[:cutPos])
				if err != nil {
					log.Errorf("writing response: %v", err)
					http.Error(res, err.Error(), http.StatusInternalServerError)
					return
				}
				_, err = res.Write([]byte(injectCode))
				if err != nil {
					log.Errorf("writing response: %v", err)
					http.Error(res, err.Error(), http.StatusInternalServerError)
					return
				}
				_, err = res.Write(buffer[cutPos:])
				if err != nil {
					log.Errorf("writing response: %v", err)
					http.Error(res, err.Error(), http.StatusInternalServerError)
					return
				}
				break
			}
		}

		ix++
	}

	if !match {
		_, err = res.Write(buffer)
		if err != nil {
			log.Errorf("writing response: %v", err)
			http.Error(res, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}
