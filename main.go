package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"os"
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
	g.SetLimit(32)

	dl, last := readLog(set)
	dlLock := &sync.Mutex{}

	sl := strings.Split(string(b), "\n")
	donmainLen := len(sl)

	f := lo.Must(os.OpenFile("domain.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644))
	defer f.Close()

	bar := progressbar.Default(int64(donmainLen))
	skip := true
	for _, txt := range sl {
		if skip && (last == txt || last == "") {
			bar.Add(1)
			skip = false
			continue
		}
		if skip {
			bar.Add(1)
			continue
		}
		if _, ok := set[txt]; ok {
			bar.Add(1)
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
					f.WriteString(txt + "\n")
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
			DomainSuffix: lo.Uniq(dl),
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
	m := map[string]struct{}{}
	readGeoSite("geosite-geolocation-cn.json", m)
	readGeoSite("geosite-geolocation-!cn.json", m)
	return m
}

func readGeoSite(filename string, set map[string]struct{}) {
	b := lo.Must(os.ReadFile(filename))
	r := gjson.ParseBytes(b)
	dl := r.Get("rules.0.domain")
	dl.ForEach(func(key, value gjson.Result) bool {
		set[value.String()] = struct{}{}
		return true
	})
}

func readLog(set map[string]struct{}) ([]string, string) {
	b, err := os.ReadFile("domain.log")
	if err != nil {
		return []string{}, ""
	}
	list := strings.Split(string(b), "\n")
	var last string
	return lo.Filter(list, func(item string, index int) bool {
		_, ok := set[item]
		if !ok && item != "" {
			last = item
		}
		return !ok
	}), last
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
