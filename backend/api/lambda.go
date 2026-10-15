package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

// LambdaHandler adapts an API Gateway REST proxy event to the http.Handler.
// The Cognito authorizer puts the user's claims on the request context.
func LambdaHandler(h http.Handler) func(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	return func(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
		body := []byte(req.Body)
		if req.IsBase64Encoded {
			body, _ = base64.StdEncoding.DecodeString(req.Body)
		}
		u := url.URL{Path: req.Path}
		q := url.Values{}
		for k, v := range req.QueryStringParameters {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		r, err := http.NewRequestWithContext(ctx, req.HTTPMethod, u.String(), bytes.NewReader(body))
		if err != nil {
			return events.APIGatewayProxyResponse{StatusCode: 400, Body: `{"error":"bad request"}`}, nil
		}
		for k, v := range req.Headers {
			r.Header.Set(k, v)
		}
		if claims, ok := req.RequestContext.Authorizer["claims"].(map[string]any); ok {
			if u, ok := claims["cognito:username"].(string); ok && u != "" {
				r = r.WithContext(WithUser(r.Context(), u))
			} else if sub, ok := claims["sub"].(string); ok {
				r = r.WithContext(WithUser(r.Context(), sub))
			}
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		out, _ := io.ReadAll(rec.Body)
		headers := map[string]string{}
		for k, v := range rec.Header() {
			headers[k] = strings.Join(v, ",")
		}
		return events.APIGatewayProxyResponse{StatusCode: rec.Code, Headers: headers, Body: string(out)}, nil
	}
}
