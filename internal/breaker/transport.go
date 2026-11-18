package breaker

import (
	"context"
	"errors"
	"net/http"
)

type Transport struct {
	next    http.RoundTripper
	breaker *Breaker
}

func NewTransport(next http.RoundTripper, b *Breaker) Transport {
	if next == nil {
		next = http.DefaultTransport
	}

	return Transport{next: next, breaker: b}
}

func (t Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := t.breaker.Allow(); err != nil {
		return nil, err
	}

	response, err := t.next.RoundTrip(request)
	t.breaker.Record(brokenCall(response, err))

	return response, err
}

func brokenCall(response *http.Response, err error) bool {
	if err != nil {
		return !errors.Is(err, context.Canceled)
	}

	return response.StatusCode >= http.StatusInternalServerError
}
