// Package types holds SSM-wide types shared by every domain package:
// the SSMError type, error codes, API constants, and wire helpers.
// It has no dependencies on other OpenSSM packages to avoid import cycles.
package types

import (
	"errors"
	"fmt"
	"net/http"
)

// SSMError is an AWS-compatible error. Code is serialized as "__type" in
// AWS JSON 1.1 error responses.
type SSMError struct {
	code       string
	message    string
	httpStatus int
}

// Error implements error.
func (e *SSMError) Error() string { return fmt.Sprintf("%s: %s", e.code, e.message) }

// Code returns the AWS error code (e.g. "ParameterNotFound").
func (e *SSMError) Code() string { return e.code }

// Message returns the human-readable error message.
func (e *SSMError) Message() string { return e.message }

// HTTPStatusCode returns the HTTP status for the error.
func (e *SSMError) HTTPStatusCode() int { return e.httpStatus }

// NewError builds an SSMError with HTTP 400. Prefer the specific factories.
func NewError(code, format string, args ...any) *SSMError {
	return &SSMError{code: code, message: fmt.Sprintf(format, args...), httpStatus: http.StatusBadRequest}
}

// AsSSMError extracts an *SSMError from err, wrapping unknown errors as
// InternalServerError so callers always get a well-formed AWS error.
func AsSSMError(err error) *SSMError {
	var e *SSMError
	if errors.As(err, &e) {
		return e
	}
	return &SSMError{code: ErrInternalServerError, message: err.Error(), httpStatus: http.StatusInternalServerError}
}

// IsCode reports whether err is an SSMError with the given code.
func IsCode(err error, code string) bool {
	var e *SSMError
	return errors.As(err, &e) && e.code == code
}

// AWS SSM error codes.
const (
	ErrInternalServerError                = "InternalServerError"
	ErrValidation                         = "ValidationException"
	ErrInvalidAction                      = "InvalidAction"
	ErrParameterNotFound                  = "ParameterNotFound"
	ErrParameterAlreadyExists             = "ParameterAlreadyExists"
	ErrParameterVersionNotFound           = "ParameterVersionNotFound"
	ErrParameterVersionLabelLimitExceeded = "ParameterVersionLabelLimitExceeded"
	ErrParameterMaxVersionLimitExceeded   = "ParameterMaxVersionLimitExceeded"
	ErrParameterLimitExceeded             = "ParameterLimitExceeded"
	ErrParameterPatternMismatch           = "ParameterPatternMismatchException"
	ErrHierarchyLevelLimitExceeded        = "HierarchyLevelLimitExceededException"
	ErrHierarchyTypeMismatch              = "HierarchyTypeMismatchException"
	ErrInvalidAllowedPattern              = "InvalidAllowedPatternException"
	ErrIncompatiblePolicy                 = "IncompatiblePolicyException"
	ErrInvalidKeyID                       = "InvalidKeyId"
	ErrInvalidFilterKey                   = "InvalidFilterKey"
	ErrInvalidFilterOption                = "InvalidFilterOption"
	ErrInvalidFilterValue                 = "InvalidFilterValue"
	ErrInvalidNextToken                   = "InvalidNextToken"
	ErrInvalidResourceID                  = "InvalidResourceId"
	ErrInvalidResourceType                = "InvalidResourceType"
	ErrTooManyTagsError                   = "TooManyTagsError"
	ErrUnsupportedParameterType           = "UnsupportedParameterType"
	ErrInvalidDocument                    = "InvalidDocument"
	ErrInvalidDocumentContent             = "InvalidDocumentContent"
	ErrInvalidDocumentVersion             = "InvalidDocumentVersion"
	ErrInvalidDocumentSchemaVersion       = "InvalidDocumentSchemaVersion"
	ErrInvalidDocumentOperation           = "InvalidDocumentOperation"
	ErrInvalidDocumentType                = "InvalidDocumentType"
	ErrDocumentAlreadyExists              = "DocumentAlreadyExists"
	ErrDuplicateDocumentContent           = "DuplicateDocumentContent"
	ErrDuplicateDocumentVersionName       = "DuplicateDocumentVersionName"
	ErrDocumentVersionLimitExceeded       = "DocumentVersionLimitExceeded"
	ErrMaxDocumentSizeExceeded            = "MaxDocumentSizeExceeded"
	ErrInvalidParameters                  = "InvalidParameters"
	ErrInvalidInstanceID                  = "InvalidInstanceId"
	ErrInvalidCommandID                   = "InvalidCommandId"
	ErrInvocationDoesNotExist             = "InvocationDoesNotExist"
	ErrInvalidActivation                  = "InvalidActivation"
	ErrInvalidActivationID                = "InvalidActivationId"
	ErrTargetNotConnected                 = "TargetNotConnected"
	ErrDoesNotExist                       = "DoesNotExistException"
	ErrAccessDenied                       = "AccessDeniedException"
	ErrUnsupportedPlatformType            = "UnsupportedPlatformType"
	ErrInvalidInstanceInformationFilter   = "InvalidInstanceInformationFilterValue"
	ErrInvalidDeliveryStatus              = "InvalidDeliveryStatus"
)

// NewValidationError reports a malformed request.
func NewValidationError(format string, args ...any) *SSMError {
	return NewError(ErrValidation, format, args...)
}

// NewInternalError reports an unexpected server-side failure.
func NewInternalError(format string, args ...any) *SSMError {
	e := NewError(ErrInternalServerError, format, args...)
	e.httpStatus = http.StatusInternalServerError
	return e
}

// NewInvalidAction reports an unknown X-Amz-Target action.
func NewInvalidAction(action string) *SSMError {
	return NewError(ErrInvalidAction, "The action %s is not valid for this web service.", action)
}

// NewParameterNotFound reports a missing parameter.
func NewParameterNotFound(name string) *SSMError {
	return NewError(ErrParameterNotFound, "Parameter %s not found.", name)
}

// NewParameterAlreadyExists reports a conflicting PutParameter.
func NewParameterAlreadyExists() *SSMError {
	return NewError(ErrParameterAlreadyExists, "The parameter already exists. To overwrite this value, set the overwrite option in the request to true.")
}

// NewParameterVersionNotFound reports a missing version or label.
func NewParameterVersionNotFound(selector string) *SSMError {
	return NewError(ErrParameterVersionNotFound, "Systems Manager could not find version %s of parameter.", selector)
}

// NewInvalidKeyID reports an unknown KMS key.
func NewInvalidKeyID(keyID string) *SSMError {
	return NewError(ErrInvalidKeyID, "Invalid KeyId: %s", keyID)
}

// NewInvalidResourceID reports a missing tag target.
func NewInvalidResourceID() *SSMError {
	return NewError(ErrInvalidResourceID, "The resource ID is not valid. Verify that you entered the correct ID and try again.")
}

// NewInvalidNextToken reports a bad pagination token.
func NewInvalidNextToken() *SSMError {
	return NewError(ErrInvalidNextToken, "The specified token is not valid.")
}

// NewInvalidDocument reports a missing document.
func NewInvalidDocument(name string) *SSMError {
	return NewError(ErrInvalidDocument, "Document with name %s does not exist.", name)
}

// NewInvalidInstanceID reports a missing or unavailable managed instance.
func NewInvalidInstanceID(id string) *SSMError {
	return NewError(ErrInvalidInstanceID, "Instances [[%s]] not in a valid state for account", id)
}

// NewInvalidCommandID reports a missing command.
func NewInvalidCommandID() *SSMError {
	return NewError(ErrInvalidCommandID, "The specified command ID is not valid.")
}

// NewDoesNotExist reports a missing session/activation/resource.
func NewDoesNotExist(format string, args ...any) *SSMError {
	return NewError(ErrDoesNotExist, format, args...)
}
