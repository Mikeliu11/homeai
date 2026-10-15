package api

import (
	"context"
	"encoding/json"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

// LambdaInvoker starts the agent function asynchronously with {"turnId"}.
type LambdaInvoker struct {
	Client   *lambda.Client
	Function string
}

func (l *LambdaInvoker) Invoke(ctx context.Context, turnID string) error {
	payload, _ := json.Marshal(map[string]string{"turnId": turnID})
	_, err := l.Client.Invoke(ctx, &lambda.InvokeInput{FunctionName: aws.String(l.Function), InvocationType: types.InvocationTypeEvent, Payload: payload})
	return err
}
