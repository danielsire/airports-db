// Command lambda runs the loader commands inside AWS Lambda.
//
// Two ways to invoke it:
//
//	API Gateway (REST proxy):  GET ?cmd=status | ?cmd=load | ?cmd=rollback | ?cmd=use&day=20261107
//	Direct / EventBridge:      {"cmd": "load"}  or  {"cmd": "use", "day": "20261107"}
//
// Reading CSVs from a local folder (-local) is deliberately not available here.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"airports-db/internal/loadercmd"
)

// Request is the direct-invoke / EventBridge payload.
type Request struct {
	Cmd string `json:"cmd"`
	Day string `json:"day,omitempty"`
}

// Response is what both invocation styles return.
type Response struct {
	Cmd     string `json:"cmd"`
	Message string `json:"message"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

// execute runs one command. Validation errors wrap loadercmd.ErrInvalid.
func execute(ctx context.Context, req Request) (Response, error) {
	cmd := loadercmd.Command{Name: req.Cmd, Day: req.Day} // Local stays false: always download

	var out bytes.Buffer
	if err := loadercmd.Run(ctx, cmd, &out); err != nil {
		return Response{Cmd: req.Cmd, Message: "failed", Error: err.Error()}, err
	}
	return Response{Cmd: req.Cmd, Message: "ok", Output: out.String()}, nil
}

func handleAPI(ctx context.Context, req events.APIGatewayProxyRequest) events.APIGatewayProxyResponse {
	res, err := execute(ctx, Request{
		Cmd: req.QueryStringParameters["cmd"],
		Day: req.QueryStringParameters["day"],
	})

	status := http.StatusOK
	switch {
	case errors.Is(err, loadercmd.ErrInvalid):
		status = http.StatusBadRequest
	case err != nil:
		log.Printf("cmd %q failed: %v", res.Cmd, err)
		status = http.StatusInternalServerError
	}
	return jsonResponse(status, res)
}

func jsonResponse(status int, v any) events.APIGatewayProxyResponse {
	body, err := json.Marshal(v)
	if err != nil {
		return events.APIGatewayProxyResponse{StatusCode: http.StatusInternalServerError}
	}
	return events.APIGatewayProxyResponse{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(body),
	}
}

// handle tells the two event shapes apart. API Gateway events always carry an
// httpMethod; anything else is treated as a direct {"cmd": ...} payload.
func handle(ctx context.Context, raw json.RawMessage) (any, error) {
	var apiReq events.APIGatewayProxyRequest
	if err := json.Unmarshal(raw, &apiReq); err == nil && apiReq.HTTPMethod != "" {
		return handleAPI(ctx, apiReq), nil
	}

	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("invalid event: %w", err)
	}
	// Returning the error marks the invocation as failed, so EventBridge
	// retries it and alarms on the Errors metric fire.
	return execute(ctx, req)
}

func main() {
	lambda.Start(handle)
}
