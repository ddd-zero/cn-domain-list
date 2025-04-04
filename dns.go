package main

import (
	"context"
	"io"
	"net/http"
	"net/url"

	"github.com/tidwall/gjson"
)

func dnsHttp(ctx context.Context, domain string) (string, error) {
	u, err := url.Parse("https://dns.alidns.com/resolve?name=example.com&type=1")
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("name", domain)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return "", err
	}
	reps, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer reps.Body.Close()

	b, err := io.ReadAll(reps.Body)
	if err != nil {
		return "", err
	}
	return gjson.GetBytes(b, "Answer.0.data").String(), nil
}
