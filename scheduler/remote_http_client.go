package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
)

type RemoteHTTPClient struct {
	client *http.Client
}

type RemoteProtocolError struct {
	StatusCode int
	Response   workerproto.ErrorResponse
}

func (e *RemoteProtocolError) Error() string {
	if e == nil {
		return "remote worker protocol request failed"
	}
	return fmt.Sprintf("remote worker protocol request failed: status=%d code=%s error=%s", e.StatusCode, e.Response.Code, e.Response.Error)
}

func NewRemoteHTTPClient(client *http.Client) *RemoteHTTPClient {
	if client == nil {
		client = &http.Client{}
	}
	return &RemoteHTTPClient{client: client}
}

func (c *RemoteHTTPClient) Execute(ctx context.Context, worker workerproto.WorkerDescriptor, task executor.ExecuteTask) (executor.ExecuteResult, error) {
	var resp workerproto.ExecuteResponse
	retryTransport := workerproto.SupportsExecuteReplay(worker.ProtocolVersion) && task.DispatchID != ""
	err := c.postJSON(ctx, worker.Endpoint, workerproto.DefaultExecutePath, workerproto.ExecuteRequest{Task: task}, &resp, retryTransport)
	return resp.Result, err
}

func (c *RemoteHTTPClient) Poll(ctx context.Context, worker workerproto.WorkerDescriptor, task executor.ExecuteTask, externalTaskID string) (executor.ExecuteResult, error) {
	var resp workerproto.PollResponse
	err := c.postJSON(ctx, worker.Endpoint, workerproto.DefaultPollPath, workerproto.PollRequest{
		Task:           task,
		ExternalTaskID: externalTaskID,
	}, &resp, false)
	return resp.Result, err
}

func (c *RemoteHTTPClient) Cancel(ctx context.Context, worker workerproto.WorkerDescriptor, task executor.ExecuteTask, externalTaskID string) error {
	var resp workerproto.CancelResponse
	err := c.postJSON(ctx, worker.Endpoint, workerproto.DefaultCancelPath, workerproto.CancelRequest{
		Task:           task,
		ExternalTaskID: externalTaskID,
	}, &resp, false)
	if err != nil {
		return err
	}
	if !resp.Cancelled {
		return fmt.Errorf("remote cancel rejected: %s", resp.Message)
	}
	return nil
}

func (c *RemoteHTTPClient) postJSON(ctx context.Context, endpoint, path string, reqBody any, out any, retryTransport bool) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	url := strings.TrimRight(endpoint, "/") + path
	var (
		resp *http.Response
		raw  []byte
	)
	maxAttempts := 1
	if retryTransport {
		maxAttempts = 2
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(workerproto.ProtocolHeader, workerproto.ProtocolVersion)
		resp, err = c.client.Do(req)
		if err != nil {
			if ctx.Err() != nil || attempt+1 == maxAttempts {
				return err
			}
			continue
		}
		raw, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			if ctx.Err() != nil || attempt+1 == maxAttempts {
				return err
			}
			continue
		}
		break
	}
	if resp.StatusCode >= 300 {
		var protocolError workerproto.ErrorResponse
		if err := json.Unmarshal(raw, &protocolError); err == nil && protocolError.Error != "" {
			return &RemoteProtocolError{StatusCode: resp.StatusCode, Response: protocolError}
		}
		return fmt.Errorf("remote call %s failed: status=%d body=%s", path, resp.StatusCode, string(raw))
	}
	if version := resp.Header.Get(workerproto.ProtocolHeader); !workerproto.AcceptsProtocolVersion(version) {
		return fmt.Errorf("remote worker protocol version mismatch: %s", version)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
