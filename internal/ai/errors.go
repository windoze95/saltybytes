package ai

import "fmt"

// AIFailureKind classifies the type of AI failure for targeted recovery.
type AIFailureKind int

const (
	// FailureTransient is a retryable server/network error (429, 5xx).
	FailureTransient AIFailureKind = iota
	// FailureAuth is an authentication error (401). Do not retry.
	FailureAuth
	// FailureContentEmpty means no tool_use block or text was returned.
	FailureContentEmpty
	// FailureContentParse means the response couldn't be parsed as expected JSON/schema.
	FailureContentParse
	// FailureContentQuality means the response parsed but is missing required fields.
	FailureContentQuality
	// FailureVisionAmbiguous means image analysis returned low-confidence or unclear results.
	FailureVisionAmbiguous
	// FailureQuotaExhausted means rate limits are exhausted for the billing period.
	FailureQuotaExhausted
	// FailureUnknown is an unrecognized error. Do not retry.
	FailureUnknown
)

// AIError wraps an error with classification metadata.
type AIError struct {
	Kind      AIFailureKind
	Err       error
	Retryable bool
	Detail    string // human-readable context for logging
}

func (e *AIError) Error() string {
	return fmt.Sprintf("[%s] %s: %v", e.kindString(), e.Detail, e.Err)
}

func (e *AIError) Unwrap() error {
	return e.Err
}

func (e *AIError) kindString() string {
	switch e.Kind {
	case FailureTransient:
		return "transient"
	case FailureAuth:
		return "auth"
	case FailureContentEmpty:
		return "content_empty"
	case FailureContentParse:
		return "content_parse"
	case FailureContentQuality:
		return "content_quality"
	case FailureVisionAmbiguous:
		return "vision_ambiguous"
	case FailureQuotaExhausted:
		return "quota_exhausted"
	case FailureUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// NewAIError creates a classified AI error.
func NewAIError(kind AIFailureKind, err error, detail string) *AIError {
	retryable := kind == FailureTransient || kind == FailureContentEmpty
	return &AIError{Kind: kind, Err: err, Retryable: retryable, Detail: detail}
}
