package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpAPIRequest is a constrained proxy request. It can reach every published
// REST operation, while routing through the same authentication, authorization
// and tenant checks as an ordinary HTTP client.
type mcpAPIRequest struct {
	Method      string         `json:"method" jsonschema:"HTTP method, for example GET POST PUT PATCH or DELETE"`
	Path        string         `json:"path" jsonschema:"API path beginning with /api/v1/ and without a query string"`
	AccessToken string         `json:"access_token,omitempty" jsonschema:"optional ServiceOps access token, without the Bearer prefix"`
	Query       map[string]any `json:"query,omitempty" jsonschema:"optional query parameters; values must be strings"`
	Body        map[string]any `json:"body,omitempty" jsonschema:"optional JSON request body"`
}

type mcpAPIResponse struct {
	Status int            `json:"status"`
	Body   map[string]any `json:"body"`
}

func newMCPHandler(api http.Handler, logger *slog.Logger) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "serviceops360-api", Version: "v1"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "serviceops_api_request",
		Description: "Call any ServiceOps360 REST endpoint. Use its documented /api/v1/ path, method, optional query/body, and an access token where the endpoint requires authentication. The response includes the original HTTP status and JSON body.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input mcpAPIRequest) (*mcp.CallToolResult, mcpAPIResponse, error) {
		return callAPI(ctx, api, input)
	})

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		Logger:                       logger,
		MaxRequestBodyBytes:          maxBodyBytes,
		PropagateRequestCancellation: true,
	})
}

func callAPI(ctx context.Context, api http.Handler, input mcpAPIRequest) (*mcp.CallToolResult, mcpAPIResponse, error) {
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch && method != http.MethodDelete {
		return nil, mcpAPIResponse{}, fmt.Errorf("method must be GET, HEAD, POST, PUT, PATCH, or DELETE")
	}
	if !strings.HasPrefix(input.Path, "/api/v1/") || strings.Contains(input.Path, "?") {
		return nil, mcpAPIResponse{}, fmt.Errorf("path must begin with /api/v1/ and must not contain a query string")
	}
	query := url.Values{}
	for key, value := range input.Query {
		text, ok := value.(string)
		if !ok {
			return nil, mcpAPIResponse{}, fmt.Errorf("query parameter %q must be a string", key)
		}
		query.Set(key, text)
	}
	var body []byte
	var err error
	if input.Body != nil {
		body, err = json.Marshal(input.Body)
		if err != nil {
			return nil, mcpAPIResponse{}, fmt.Errorf("encode request body: %w", err)
		}
	}
	requestURL := "http://serviceops.internal" + input.Path
	if encoded := query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, mcpAPIResponse{}, err
	}
	if input.Body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if input.AccessToken != "" {
		request.Header.Set("Authorization", "Bearer "+input.AccessToken)
	}
	recorder := newResponseRecorder()
	api.ServeHTTP(recorder, request)
	responseBody := map[string]any{}
	if len(recorder.Body.Bytes()) > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &responseBody); err != nil {
			responseBody = map[string]any{"raw": recorder.Body.String()}
		}
	}
	return nil, mcpAPIResponse{Status: recorder.Code, Body: responseBody}, nil
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{header: make(http.Header), Code: http.StatusOK}
}

type responseRecorder struct {
	header      http.Header
	Body        bytes.Buffer
	Code        int
	wroteHeader bool
}

func (r *responseRecorder) Header() http.Header { return r.header }

func (r *responseRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.Code = status
	r.wroteHeader = true
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.Body.Write(data)
}
