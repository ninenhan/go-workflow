package workflow

import "fmt"

type ControlSignal string

const (
	ControlBreak    ControlSignal = "break"
	ControlContinue ControlSignal = "continue"
	ControlGoto     ControlSignal = "goto"
)

type ControlSignalError struct {
	Signal ControlSignal
	Target string
}

func (e *ControlSignalError) Error() string {
	if e == nil {
		return ""
	}
	if e.Target != "" {
		return fmt.Sprintf("control signal: %s -> %s", e.Signal, e.Target)
	}
	return fmt.Sprintf("control signal: %s", e.Signal)
}

