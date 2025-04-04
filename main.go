package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/oschwald/geoip2-golang"
	"github.com/samber/lo"
	"github.com/schollz/progressbar/v3"
	"github.com/tidwall/gjson"
	"golang.org/x/sync/errgroup"
)

func main() {
	b := lo.Must(os.ReadFile("cloudflare-radar_top-1000000-domains.csv"))
	set := geosite()

	db := lo.Must(geoip2.Open("GeoLite2-Country.mmdb"))
	defer db.Close()

	ctx := context.Background()

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(runtime.GOMAXPROCS(0))

	dl := []string{}
	dlLock := &sync.Mutex{}

	sl := strings.Split(string(b), "\n")
	donmainLen := len(sl)

	bar := progressbar.Default(int64(donmainLen))
	for _, txt := range sl {
		if _, ok := set[txt]; ok {
			continue
		}
		g.Go(func() error {
			defer bar.Add(1)
			return retry.Do(func() error {
				ctx, cancel := context.WithTimeout(gCtx, 10*time.Second)
				defer cancel()
				addr, err := dnsHttp(ctx, txt)
				if err != nil {
					return err
				}
				if addr == "" {
					return nil
				}
				netip := net.ParseIP(addr)
				if netip == nil {
					return nil
				}
				c := lo.Must(db.Country(netip))
				if c.Country.IsoCode == "CN" {
					dlLock.Lock()
					dl = append(dl, txt)
					dlLock.Unlock()
				}
				return nil
			}, retryOpts...)
		})
	}
	lo.Must0(g.Wait())

	geo := geo{
		Version: 1,
		Rules: rule{
			DomainSuffix: dl,
		},
	}
	nf := lo.Must(os.Create("ext-cn-list.json"))
	defer nf.Close()
	e := json.NewEncoder(nf)
	e.SetIndent("", "    ")
	e.SetEscapeHTML(false)
	lo.Must0(e.Encode(geo))
}

func geosite() map[string]struct{} {
	b := lo.Must(os.ReadFile("geosite-geolocation-cn.json"))
	r := gjson.ParseBytes(b)
	dl := r.Get("rules.0.domain")
	m := map[string]struct{}{}
	dl.ForEach(func(key, value gjson.Result) bool {
		m[value.String()] = struct{}{}
		return true
	})
	return m
}

var retryOpts = []retry.Option{
	retry.Attempts(0),
	retry.LastErrorOnly(true),
	retry.OnRetry(func(n uint, err error) {
		log.Printf("#%d: %s\n", n, err)
	}),
}

type geo struct {
	Version int  `json:"version"`
	Rules   rule `json:"rules"`
}

type rule struct {
	DomainSuffix []string `json:"domain_suffix"`
}
