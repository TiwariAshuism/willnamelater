// Package llm is the Claude advisory layer. This file only anchors the module's
// external dependency so `go mod tidy` does not drop it before the
// implementation that uses it lands. It is replaced by real code in the llm
// build task and carries no logic.
package llm

import _ "github.com/anthropics/anthropic-sdk-go"
