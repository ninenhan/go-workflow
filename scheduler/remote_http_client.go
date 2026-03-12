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

func NewRemoteHTTPClient(client *http.Client) *RemoteHTTPClient {
	if client == nil {
		client = &http.Client{}
	}
	return &RemoteHTTPClient{client: client}
}

func (c *RemoteHTTPClient) Execute(ctx context.Context, worker workerproto.WorkerDescriptor, task executor.ExecuteTask) (executor.ExecuteResult, error) {
	var resp workerproto.ExecuteResponse
	err := c.postJSON(ctx, worker.Endpoint, workerproto.DefaultExecutePath, workerproto.ExecuteRequest{Task: task}, &resp)
	return resp.Result, err
}

func (c *RemoteHTTPClient) Poll(ctx context.Context, worker workerproto.WorkerDescriptor, task executor.ExecuteTask, externalTaskID string) (executor.ExecuteResult, error) {
	var resp workerproto.PollResponse
	err := c.postJSON(ctx, worker.Endpoint, workerproto.DefaultPollPath, workerproto.PollRequest{
		Task:           task,
		ExternalTaskID: externalTaskID,
	}, &resp)
	return resp.Result, err
}

func (c *RemoteHTTPClient) Cancel(ctx context.Context, worker workerproto.WorkerDescriptor, task executor.ExecuteTask, externalTaskID string) error {
	var resp workerproto.CancelResponse
	err := c.postJSON(ctx, worker.Endpoint, workerproto.DefaultCancelPath, workerproto.CancelRequest{
		Task:           task,
		ExternalTaskID: externalTaskID,
	}, &resp)
	if err != nil {
		return err
	}
	if !resp.Cancelled {
		return fmt.Errorf("remote cancel rejected: %s", resp.Message)
	}
	return nil
}

func (c *RemoteHTTPClient) postJSON(ctx context.Context, endpoint, path string, reqBody any, out any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	url := strings.TrimRight(endpoint, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("remote call %s failed: status=%d body=%s", path, resp.StatusCode, string(raw))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
