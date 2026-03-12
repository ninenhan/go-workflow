package runner

import (
	"errors"
	"sync"
)

type RunCommand string

const (
	RunCommandNone   RunCommand = ""
	RunCommandPause  RunCommand = "pause"
	RunCommandCancel RunCommand = "cancel"
)

var (
	ErrRunPaused    = errors.New("run paused")
	ErrRunCancelled = errors.New("run cancelled")
)

type RunController interface {
	Get(runID string) RunCommand
	Set(runID string, cmd RunCommand)
	Clear(runID string)
}

type MemoryRunController struct {
	mu       sync.RWMutex
	commands map[string]RunCommand
}

func NewMemoryRunController() *MemoryRunController {
	return &MemoryRunController{commands: make(map[string]RunCommand)}
}

func (c *MemoryRunController) Get(runID string) RunCommand {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.commands[runID]
}

func (c *MemoryRunController) Set(runID string, cmd RunCommand) {
	if runID == "" || cmd == RunCommandNone {
		return
	}
	c.mu.Lock()
	c.commands[runID] = cmd
	c.mu.Unlock()
}

func (c *MemoryRunController) Clear(runID string) {
	c.mu.Lock()
	delete(c.commands, runID)
	c.mu.Unlock()
}
