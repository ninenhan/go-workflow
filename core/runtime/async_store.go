package wfruntime

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ninenhan/go-workflow/core/executor"
)

var (
	ErrAsyncTaskNotFound = errors.New("async task not found")
	ErrAsyncTaskCancelled = errors.New("async task is cancelled")
	ErrAsyncResultConflict = errors.New("async task already has a different result")
)

func validateAsyncResult(result executor.ExecuteResult) error {
	switch result.NormalizedStatus() {
	case executor.StatusSucceeded, executor.StatusFailed, executor.StatusRetryable:
		return nil
	default:
		return fmt.Errorf("async callback result must be terminal, got %s", result.NormalizedStatus())
	}
}

func asyncResultPayload(result executor.ExecuteResult) ([]byte, string, error) {
	if err := validateAsyncResult(result); err != nil {
		return nil, "", err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return nil, "", fmt.Errorf("marshal async result: %w", err)
	}
	hash := sha256.Sum256(payload)
	return payload, hex.EncodeToString(hash[:]), nil
}

func newAsyncClaimToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate async claim token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
