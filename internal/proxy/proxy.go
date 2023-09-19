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
	log "github.com/sirupsen/logrus"
)

type Proxy struct {
	config.Config
	server *http.Server
}

func New(config config.Config) (*Proxy, error) {
	proxy := &Proxy{
		Config: config,
	}

	if proxy.Proxy.Port == 0 {
		proxy.Proxy.Port = 4000
	}

	if proxy.Proxy.Timeout == 0 {
		proxy.Proxy.Timeout = 5
	}

	if proxy.Proxy.DownstreamPort == 0 {
		return nil, errors.New("downstream port is required")
	}

	proxy.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", config.Proxy.Port),
		Handler: proxy,
	}

	return proxy, nil
}

func (p *Proxy) Start() error {
	if !p.Proxy.Enabled {
		return nil
	}

	log.Infof("starting proxy server on port %d", p.Proxy.Port)
	go func() {
		err := p.server.ListenAndServe()
		log.Infof("shutting down proxy server: %v", err)
	}()

	return nil
}

func (p *Proxy) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	ctx, closeFn := context.WithTimeout(req.Context(), time.Duration(p.Proxy.Timeout)*time.Second)
	defer closeFn()

	nextURL := req.URL
	nextURL.Scheme = "http"
	nextURL.Host = fmt.Sprintf("localhost:%d", p.Proxy.DownstreamPort)
	// TODO: handle query params etc

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

	isHtml := false
	for k, v := range nextRes.Header {
		if !strings.EqualFold(k, "Content-Length") {
			res.Header()[k] = v
		}
		if strings.EqualFold(k, "Content-Type") {
			isHtml = strings.Contains(v[0], "text/html")
		}
	}

	res.WriteHeader(nextRes.StatusCode)

	// TODO: if html, inject reload script
	if isHtml {
		_, err = io.WriteString(res, `
			<script>
				const source = new EventSource('/__gomon__/reload');
				source.onmessage = function (event) {
					console.log('reload');
					window.location.reload();
				};
			</script>
		`)
		if err != nil {
			log.Errorf("writing response body: %v", err)
			http.Error(res, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	_, err = io.Copy(res, nextRes.Body)
	if err != nil {
		log.Errorf("copying response body: %v", err)
		http.Error(res, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (p *Proxy) Stop() error {
	p.server.Shutdown(context.Background())
	return nil
}
