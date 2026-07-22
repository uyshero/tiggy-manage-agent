package tools

import (
	"errors"
	"fmt"
	"strings"
)

func newToolContractErrorf(code, format string, args ...any) *ToolContractError {
	return NewToolContractError(code, fmt.Errorf(format, args...))
}

// ToolContractError identifies a Registry, Schema, or Policy contract defect
// without coupling the tools package to the Agent Core runtime.
type ToolContractError struct {
	code  string
	cause error
}

func NewToolContractError(code string, cause error) *ToolContractError {
	code = strings.TrimSpace(code)
	if code == "" {
		code = "invalid_tool_contract"
	}
	if cause == nil {
		cause = errors.New("tool contract is invalid")
	}
	return &ToolContractError{code: code, cause: cause}
}

func (e *ToolContractError) Error() string {
	if e == nil || e.cause == nil {
		return "tool contract is invalid"
	}
	return e.cause.Error()
}

func (e *ToolContractError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *ToolContractError) ErrorCode() string {
	if e == nil || e.code == "" {
		return "invalid_tool_contract"
	}
	return e.code
}
