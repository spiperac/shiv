package proxy

import (
	"fmt"
	"net/http"
	"time"

	internalhttp "github.com/shiv/internal/http"
)

var upstreamClient = &http.Client{
	Transport: &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func forward(req *http.Request) (*http.Response, error) {
	out, err := http.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), req.Body)
	if err != nil {
		return nil, fmt.Errorf("forward: build request: %w", err)
	}
	for k, vals := range req.Header {
		if internalhttp.HopByHop[k] {
			continue
		}
		out.Header[k] = vals
	}
	internalhttp.StripRequestCacheHeaders(out.Header)

	resp, err := upstreamClient.Do(out)
	if err != nil {
		return nil, fmt.Errorf("forward: upstream request: %w", err)
	}
	internalhttp.StripResponseCacheHeaders(resp.Header)
	return resp, nil
}
