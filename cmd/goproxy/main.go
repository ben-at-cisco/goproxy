package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/goproxy/goproxy"
)

var (
	address          = flag.String("address", "localhost:8080", "TCP address that the HTTP server listens on")
	tlsCertFile      = flag.String("tls-cert-file", "", "path to the TLS certificate file")
	tlsKeyFile       = flag.String("tls-key-file", "", "path to the TLS key file")
	pathPrefix       = flag.String("path-prefix", "", "prefix for all request paths")
	goBinName        = flag.String("go-bin-name", "go", "name of the Go binary that is used to execute direct fetches")
	maxDirectFetches = flag.Int("max-direct-fetches", 0, "maximum number (0 means no limit) of concurrent direct fetches")
	proxiedSUMDBs    = flag.String("proxied-sumdbs", "", "comma-separated list of proxied checksum databases")
	cacheDir         = flag.String("cache-dir", "caches", "directory that used to cache module files")
	tempDir          = flag.String("temp-dir", os.TempDir(), "directory for storing temporary files")
	insecure         = flag.Bool("insecure", false, "allow insecure TLS connections")
	connectTimeout   = flag.Duration("connect-timeout", 30*time.Second, "maximum amount of time (0 means no limit) will wait for an outgoing connection to establish")
	fetchTimeout     = flag.Duration("fetch-timeout", 10*time.Minute, "maximum amount of time (0 means no limit) will wait for a fetch to complete")
)

func main() {
	flag.Parse()

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: *connectTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: *insecure}
	transport.RegisterProtocol("file", http.NewFileTransport(httpDirFS{}))
	g := &goproxy.Goproxy{
		GoBinName:        *goBinName,
		MaxDirectFetches: *maxDirectFetches,
		ProxiedSUMDBs:    strings.Split(*proxiedSUMDBs, ","),
		Cacher:           goproxy.DirCacher(*cacheDir),
		TempDir:          *tempDir,
		Transport:        transport,
	}

	handler := http.Handler(g)
	if *pathPrefix != "" {
		handler = http.StripPrefix(*pathPrefix, handler)
	}
	if *fetchTimeout > 0 {
		handler = func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				ctx, cancel := context.WithTimeout(req.Context(), *fetchTimeout)
				h.ServeHTTP(rw, req.WithContext(ctx))
				cancel()
			})
		}(handler)
	}

	server := &http.Server{Addr: *address, Handler: handler}
	var err error
	if *tlsCertFile != "" && *tlsKeyFile != "" {
		err = server.ListenAndServeTLS(*tlsCertFile, *tlsKeyFile)
	} else {
		err = server.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("http server error: %v\n", err)
		return
	}
}

type httpDirFS struct{}

func (fs httpDirFS) Open(name string) (http.File, error) {
	name = filepath.FromSlash(name)
	if filepath.Separator == '\\' {
		name = name[1:]
		volumeName := filepath.VolumeName(name)
		if volumeName == "" || strings.HasPrefix(volumeName, `\\`) {
			return nil, errors.New("file URL missing drive letter")
		}
	}
	if !filepath.IsAbs(name) {
		return nil, errors.New("path is not absolute")
	}
	return os.Open(name)
}
