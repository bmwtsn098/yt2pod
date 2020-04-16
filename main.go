package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"google.golang.org/api/googleapi/transport"
	"google.golang.org/api/youtube/v3"
)

const (
	dataSubdirEpisodes = "ep"
	dataSubdirMetadata = "meta"

	downloadCmdName = "youtube-dl"

	hitLoggingPeriod       = 24 * time.Hour
	websrvClientReadTimout = 15 * time.Second
	ytAPIRespiteUnit       = 5 * time.Minute
)

var (
	useSyslog = flag.Bool("syslog", false,
		"send log statements to syslog rather than writing them to stderr")

	configPath = flag.String("config", "config.json",
		"path to config file")

	dataPath = flag.String("data", "data",
		"path to directory to change into and write data (created if needed)")

	dataClean = flag.Bool("dataclean", false,
		"during initialisation, remove files in the data directory that are irrelevant given the current config")

	printVersion = flag.Bool("version", false,
		"print version information then exit")

	// These get set by the Magefile
	stampedBuildVersion = "unknown-version"
	stampedBuildTime    = "unknown-time"
)

func main() {
	cfg, err := setup()
	if err != nil {
		log.Fatal(err)
	}

	err = run(cfg)
	if err != nil {
		log.Fatal(err)
	}
}

func run(cfg *config) error {
	apiKey := cfg.YTDataAPIKey
	log.Printf("Using YouTube Data API key ending %s", apiKey[len(apiKey)-5:])

	var cleanc chan *cleaningWhitelist
	if *dataClean {
		cleanc = make(chan *cleaningWhitelist)
	}

	for i := range cfg.Podcasts {
		ytAPI, err := youtube.New(&http.Client{
			Transport: &transport.APIKey{Key: apiKey},
		})
		if err != nil {
			return err
		}
		wat, err := newWatcher(
			ytAPI, cfg, &cfg.Podcasts[i], cleanc)
		if err != nil {
			log.Fatal(err)
		}
		go wat.watch()
	}

	if *dataClean {
		n, err := clean(len(cfg.Podcasts), cleanc)
		if err != nil {
			return err
		}
		log.Printf("Clean removed %d files", n)
	}

	// Run a webserver to serve the episode and metadata files.

	mux := http.NewServeMux()

	files := newHitLoggingFsys(http.Dir("."), hitLoggingPeriod, cfg.ServeDirectoryListings)
	mux.Handle("/", http.FileServer(files))

	mux.HandleFunc(httpHealthPrefix, healthHandler)

	websrv := http.Server{
		Addr:    fmt.Sprint(cfg.ServeHost, ":", cfg.ServePort),
		Handler: mux,
		// Conserve # open FDs by pruning persistent (keep-alive) HTTP conns.
		ReadTimeout: websrvClientReadTimout,
	}
	err := websrv.ListenAndServe()
	// @todo #0 When listening on cfg.ServeHost fails and an alternative address is listened
	//  on, cfg.ServeHost should not be used in watcher#buildURL.
	//  How about instead of automatically falling back to trying to listen on all
	//  interfaces, add a serve_host_fallback:"localhost" to config? Then if
	//  neither serve_host or serve_host_fallback work, it's a fatal error.
	if err != nil {
		samePortAllInterfaces := fmt.Sprint(":", cfg.ServePort)
		log.Printf("Web server could not listen on %v, trying %v instead",
			websrv.Addr, samePortAllInterfaces)
		websrv.Addr = samePortAllInterfaces
		err = websrv.ListenAndServe()
	}
	return err
}
