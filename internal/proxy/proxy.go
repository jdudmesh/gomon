package proxy

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

type Proxy struct {
	config.Config
	httpServer *http.Server
	sseServer  *sse.Server
	injectCode string
}

func New(config config.Config) (*Proxy, error) {
	proxy := &Proxy{
		Config: config,
	}

	err := proxy.initProxy()
	if err != nil {
		return nil, err
	}

	return proxy, nil
}

func (p *Proxy) initProxy() error {
	if !p.Proxy.Enabled && p.Proxy.Port == 0 {
		return nil
	}

	if p.Proxy.Port == 0 {
		p.Proxy.Port = 4000
		p.Proxy.Enabled = true
	}

	if p.Proxy.Downstream.Host == "" {
		return errors.New("downstream host:port is required")
	}

	if p.Proxy.Downstream.Timeout == 0 {
		p.Proxy.Downstream.Timeout = 5
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
		Addr:    fmt.Sprintf(":%d", p.Proxy.Port),
		Handler: mux,
	}

	return nil
}

func (p *Proxy) Start() error {
	if !p.Proxy.Enabled {
		return nil
	}

	log.Infof("proxy server running on http://localhost:%d", p.Proxy.Port)
	go func() {
		err := p.httpServer.ListenAndServe()
		log.Infof("shutting down proxy server: %v", err)
	}()

	return nil
}

func (p *Proxy) handleReload(res http.ResponseWriter, req *http.Request) {
	log.Infof("reloading proxy")
	res.WriteHeader(http.StatusOK)
}

func (p *Proxy) handleDefault(res http.ResponseWriter, req *http.Request) {
	duration := time.Duration(p.Proxy.Downstream.Timeout) * time.Second // TODO: calculate at startup
	p.proxyRequest(res, req, p.Proxy.Downstream.Host, duration, p.injectCode)
}

func (p *Proxy) proxyRequest(res http.ResponseWriter, req *http.Request, host string, timeout time.Duration, injectCode string) {
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

	nextRes, err := http.DefaultClient.Do(nextReq)
	if err != nil {
		log.Errorf("proxying request: %v", err)
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}
	defer nextRes.Body.Close()

	nextRes.Header.Del("Content-Length")
	for k, v := range nextRes.Header {
		res.Header()[k] = v
	}

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
				log.Infof(string(injectCode))
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

func (p *Proxy) Stop() error {
	if p.sseServer != nil {
		p.sseServer.Close()
	}
	return p.httpServer.Shutdown(context.Background())
}

func (p *Proxy) Notify(msg string) {
	log.Infof("notifying browser: %s", msg)
	p.sseServer.Publish("hmr", &sse.Event{
		Data: []byte(msg),
	})
}
