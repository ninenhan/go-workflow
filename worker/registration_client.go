package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ninenhan/go-workflow/core/workerproto"
)

type RegistrationClient struct {
	client *http.Client
}

type ProtocolError struct {
	StatusCode int
	Response   workerproto.ErrorResponse
}

func (e *ProtocolError) Error() string {
	if e == nil {
		return "worker protocol request failed"
	}
	return fmt.Sprintf("worker protocol request failed: status=%d code=%s error=%s", e.StatusCode, e.Response.Code, e.Response.Error)
}

func NewRegistrationClient(client *http.Client) *RegistrationClient {
	if client == nil {
		client = &http.Client{}
	}
	return &RegistrationClient{client: client}
}

func (c *RegistrationClient) Register(ctx context.Context, schedulerEndpoint string, worker workerproto.WorkerDescriptor) error {
	var resp workerproto.RegisterResponse
	if err := c.postJSON(ctx, schedulerEndpoint, workerproto.DefaultRegisterPath, workerproto.RegisterRequest{Worker: worker}, &resp); err != nil {
		return err
	}
	if !resp.Accepted {
		return fmt.Errorf("scheduler rejected worker registration: %s", resp.Message)
	}
	return nil
}

func (c *RegistrationClient) Heartbeat(ctx context.Context, schedulerEndpoint, workerID string) error {
	return c.postJSON(ctx, schedulerEndpoint, workerproto.DefaultHeartbeatPath, workerproto.HeartbeatRequest{
		WorkerID: workerID,
	}, nil)
}

func (c *RegistrationClient) Pull(ctx context.Context, schedulerEndpoint, workerID string) (*workerproto.Command, error) {
	var resp workerproto.PullResponse
	if err := c.postJSON(ctx, schedulerEndpoint, workerproto.DefaultPullPath, workerproto.PullRequest{WorkerID: workerID}, &resp); err != nil {
		return nil, err
	}
	return resp.Command, nil
}

func (c *RegistrationClient) Complete(ctx context.Context, schedulerEndpoint string, completion workerproto.CompleteRequest) error {
	var resp workerproto.CompleteResponse
	if err := c.postJSON(ctx, schedulerEndpoint, workerproto.DefaultCompletePath, completion, &resp); err != nil {
		return err
	}
	if !resp.Accepted {
		return fmt.Errorf("scheduler rejected worker completion")
	}
	return nil
}

func (c *RegistrationClient) postJSON(ctx context.Context, endpoint, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := strings.TrimRight(endpoint, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(workerproto.ProtocolHeader, workerproto.ProtocolVersion)
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
		var protocolError workerproto.ErrorResponse
		if err := json.Unmarshal(raw, &protocolError); err == nil && protocolError.Error != "" {
			return &ProtocolError{StatusCode: resp.StatusCode, Response: protocolError}
		}
		return fmt.Errorf("worker protocol request failed: status=%d body=%s", resp.StatusCode, string(raw))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
