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
		return fmt.Errorf("worker registration request failed: status=%d body=%s", resp.StatusCode, string(raw))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
