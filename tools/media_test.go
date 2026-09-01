package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
)

type inProcessTestServer struct {
	URL    string
	Config struct {
		Handler http.Handler
	}
}

func newInProcessTestServer(handler http.Handler) *inProcessTestServer {
	server := &inProcessTestServer{URL: "http://127.0.0.1:1"}
	server.Config.Handler = handler
	return server
}

func (*inProcessTestServer) Close() {}

func mediaTestContext(registry *Registry, handlers ...http.Handler) context.Context {
	ctx := context.Background()
	if len(handlers) == 0 {
		return ctx
	}
	handler := handlers[0]
	return context.WithValue(ctx, mediaTransportContextKey{registry: registry}, http.RoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if handler == nil {
			return nil, errors.New("invalid media test handler")
		}
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Result(), nil
	})))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, errors.New("invalid media test request")
	}
	return f(request)
}
