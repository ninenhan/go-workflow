package wfruntime

func CanTransition(from, to Status) bool {
	if from == to {
		return true
	}
	allowed := map[Status]map[Status]bool{
		StatusPending: {
			StatusRunning:   true,
			StatusPaused:    true,
			StatusCancelled: true,
		},
		StatusRunning: {
			StatusSuccess:   true,
			StatusFailed:    true,
			StatusRetry:     true,
			StatusTimeout:   true,
			StatusPaused:    true,
			StatusCancelled: true,
		},
		StatusRetry: {
			StatusPending:   true,
			StatusRunning:   true,
			StatusFailed:    true,
			StatusCancelled: true,
		},
		StatusPaused: {
			StatusRunning:   true,
			StatusCancelled: true,
		},
		StatusFailed: {
			StatusRetry: true,
		},
	}
	return allowed[from][to]
}

func IsTerminal(status Status) bool {
	switch status {
	case StatusSuccess, StatusFailed, StatusCancelled, StatusTimeout:
		return true
	default:
		return false
	}
}
