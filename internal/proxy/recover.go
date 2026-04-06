package proxy

import (
	"runtime/debug"

	"github.com/shiv/internal/logger"
)

func recoverPanic(context string) {
	if r := recover(); r != nil {
		logger.Error("panic in %s: %v\n%s", context, r, debug.Stack())
	}
}
