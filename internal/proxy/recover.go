package proxy

import "github.com/shiv/internal/logger"

// recoverPanic logs a panic and its stack without crashing the process.
// Use as: defer recoverPanic("context description")
func recoverPanic(context string) {
	if r := recover(); r != nil {
		logger.Error("panic in %s: %v", context, r)
	}
}
