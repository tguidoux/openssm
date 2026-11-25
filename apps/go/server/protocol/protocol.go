// Package protocol implements the AWS JSON 1.1 wire protocol used by SSM:
// requests are POSTed with "X-Amz-Target: AmazonSSM.<Action>" and a JSON
// body; errors are returned as {"__type": "<Code>", "message": "..."}.
package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tguidoux/openssm/pkgs/v1/ssm/types"
)

// TargetHeader is the header carrying the SSM action name.
const TargetHeader = "X-Amz-Target"

// ParseAction extracts the action name from an X-Amz-Target header value.
func ParseAction(target string) (string, error) {
	if !strings.HasPrefix(target, types.TargetPrefix) || len(target) == len(types.TargetPrefix) {
		return "", types.NewError("UnknownOperationException", "Missing or invalid X-Amz-Target header %q", target)
	}
	return strings.TrimPrefix(target, types.TargetPrefix), nil
}

// ErrorBody is the AWS JSON 1.1 error shape.
type ErrorBody struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

// WriteJSON writes a successful AWS JSON 1.1 response.
func WriteJSON(w http.ResponseWriter, requestID string, body []byte) {
	w.Header().Set("Content-Type", types.JSONContentType)
	w.Header().Set("X-Amzn-RequestId", requestID)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// WriteError writes err as an AWS JSON 1.1 error response.
func WriteError(w http.ResponseWriter, requestID string, err error) {
	e := types.AsSSMError(err)
	data, mErr := json.Marshal(ErrorBody{Type: e.Code(), Message: e.Message()})
	if mErr != nil {
		http.Error(w, fmt.Sprintf("%s: %s", e.Code(), e.Message()), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", types.JSONContentType)
	w.Header().Set("X-Amzn-RequestId", requestID)
	w.Header().Set("X-Amzn-ErrorType", e.Code())
	w.WriteHeader(e.HTTPStatusCode())
	_, _ = w.Write(data)
}

// Decode strictly-enough unmarshals a request body. An empty body is
// treated as "{}" since every SSM input member is optional at the wire level.
func Decode(body []byte, out any) error {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return types.NewError("SerializationException", "Unable to parse request body: %v", err)
	}
	return nil
}
