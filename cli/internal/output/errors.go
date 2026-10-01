package output

import (
	"fmt"
	"strings"
)

// CLI exit codes (spec §15).
const (
	ExitOK          = 0
	ExitGeneric     = 1
	ExitAuth        = 2
	ExitNotFound    = 3
	ExitForbidden   = 4
	ExitValidation  = 5
	ExitConflict    = 6
	ExitUnavailable = 7
	ExitTimeout     = 8
)

// Error codes produced locally by the CLI. Server codes are passed through.
const (
	CodeTokenMissing       = "TOKEN_MISSING"
	CodeValidation         = "VALIDATION_ERROR"
	CodeUnknownCommand     = "UNKNOWN_COMMAND"
	CodeConfigInvalid      = "CONFIG_INVALID"
	CodeConfigSecret       = "CONFIG_CONTAINS_SECRET"
	CodeAPIURLMissing      = "API_URL_MISSING"
	CodeAPIURLInsecure     = "API_URL_INSECURE"
	CodeGit                = "GIT_ERROR"
	CodeNotGitRepo         = "NOT_A_GIT_REPOSITORY"
	CodeKeychain           = "KEYCHAIN_ERROR"
	CodeTimeout            = "TIMEOUT"
	CodeServiceUnavailable = "SERVICE_UNAVAILABLE"
	CodeRateLimited        = "RATE_LIMITED"
	CodeInvalidResponse    = "INVALID_RESPONSE"
	CodeResponseTooLarge   = "RESPONSE_TOO_LARGE"
	CodeUnexpectedRedirect = "UNEXPECTED_REDIRECT"
	CodeInternal           = "INTERNAL_ERROR"
)

// CLIError carries a normalized error and the process exit code.
type CLIError struct {
	Err  Error
	Exit int
}

func (e *CLIError) Error() string { return fmt.Sprintf("%s: %s", e.Err.Code, e.Err.Message) }

// NewError builds a CLIError whose exit code is derived from the code.
func NewError(code, message string, details map[string]any) *CLIError {
	exit, ok := ExitForCode(code)
	if !ok {
		exit = ExitGeneric
	}
	return &CLIError{Err: Error{Code: code, Message: message, Retryable: IsRetryableExit(exit), Details: details}, Exit: exit}
}

// Validationf is a shorthand for argument validation failures.
func Validationf(format string, a ...any) *CLIError {
	return NewError(CodeValidation, fmt.Sprintf(format, a...), nil)
}

var codeExits = map[string]int{
	CodeTokenMissing:           ExitAuth,
	"TOKEN_EXPIRED":            ExitAuth,
	"TOKEN_INVALID":            ExitAuth,
	"SCOPE_REQUIRED":           ExitForbidden,
	"ROLE_REQUIRED":            ExitForbidden,
	"SESSION_PRIVATE":          ExitForbidden,
	"RESOURCE_FORBIDDEN":       ExitForbidden,
	"INVALID_STATE_TRANSITION": ExitConflict,
	"IDEMPOTENCY_CONFLICT":     ExitConflict,
	CodeValidation:             ExitValidation,
	CodeUnknownCommand:         ExitValidation,
	CodeConfigInvalid:          ExitValidation,
	CodeConfigSecret:           ExitValidation,
	CodeAPIURLMissing:          ExitValidation,
	CodeAPIURLInsecure:         ExitValidation,
	CodeNotGitRepo:             ExitValidation,
	"REPOSITORY_AMBIGUOUS":     ExitValidation,
	"PROJECT_AMBIGUOUS":        ExitValidation,
	"PROJECT_REQUIRED":         ExitValidation,
	"WRITE_NOT_CONFIRMED":      ExitValidation,
	"DESTRUCTIVE_NOT_ALLOWED":  ExitForbidden,
	CodeTimeout:                ExitTimeout,
	CodeServiceUnavailable:     ExitUnavailable,
	CodeRateLimited:            ExitUnavailable,
}

// ExitForCode maps a known error code to an exit code.
func ExitForCode(code string) (int, bool) {
	code = strings.ToUpper(code)
	if exit, ok := codeExits[code]; ok {
		return exit, true
	}
	if strings.HasSuffix(code, "_NOT_FOUND") {
		return ExitNotFound, true
	}
	return 0, false
}

// ExitForStatus maps an HTTP status to an exit code.
func ExitForStatus(status int) int {
	switch {
	case status == 401:
		return ExitAuth
	case status == 403:
		return ExitForbidden
	case status == 404:
		return ExitNotFound
	case status == 400 || status == 422:
		return ExitValidation
	case status == 409:
		return ExitConflict
	case status == 408 || status == 504:
		return ExitTimeout
	case status == 429 || status >= 500:
		return ExitUnavailable
	default:
		return ExitGeneric
	}
}

// IsRetryableExit reports whether failures in this exit class may succeed on retry.
func IsRetryableExit(exit int) bool {
	return exit == ExitUnavailable || exit == ExitTimeout
}
