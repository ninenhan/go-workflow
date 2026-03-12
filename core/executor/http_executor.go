package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type HTTPExecutor struct {
	client *http.Client
}

func NewHTTPExecutor(client *http.Client) *HTTPExecutor {
	if client == nil {
		client = &http.Client{}
	}
	return &HTTPExecutor{client: client}
}

func (e *HTTPExecutor) Type() Type { return TypeHTTP }

func (e *HTTPExecutor) Execute(ctx context.Context, req Request) (Result, error) {
	url, _ := req.Params["url"].(string)
	if strings.TrimSpace(url) == "" {
		return Result{}, fmt.Errorf("http executor missing url")
	}
	method, _ := req.Params["method"].(string)
	if method == "" {
		method = http.MethodPost
	}
	body, _ := req.Params["body"].(string)
	httpReq, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBufferString(body))
	if err != nil {
		return Result{}, err
	}
	if headers, ok := req.Params["headers"].(map[string]any); ok {
		for k, v := range headers {
			httpReq.Header.Set(k, fmt.Sprint(v))
		}
	}
	resp, err := e.client.Do(httpReq)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Status: StatusSucceeded,
		Output: string(data),
		Metadata: map[string]any{
			"status_code": resp.StatusCode,
		},
	}, nil
}
