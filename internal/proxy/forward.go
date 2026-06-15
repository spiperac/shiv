package proxy

import (
	"fmt"
	"net/http"

	internalhttp "github.com/shiv/internal/http"
)

func forward(req *http.Request, client *http.Client) (*http.Response, error) {
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

	resp, err := client.Do(out)
	if err != nil {
		return nil, fmt.Errorf("forward: upstream request: %w", err)
	}
	internalhttp.StripResponseCacheHeaders(resp.Header)
	return resp, nil
}
