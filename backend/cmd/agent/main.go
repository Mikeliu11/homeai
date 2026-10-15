// Command agent is the Lambda that runs one chat turn. It is invoked
// asynchronously by the api with {"turnId": "..."} and can write only
// proposals, turns and memory.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"helperpay/agent"
	"helperpay/pricing"
	"helperpay/records"
)

type event struct {
	TurnID string `json:"turnId"`
}

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatal(err)
	}
	store := &records.DynamoStore{Client: dynamodb.NewFromConfig(cfg), Records: os.Getenv("TABLE_RECORDS"), Proposals: os.Getenv("TABLE_PROPOSALS")}
	loc := pricing.NY()
	deps := agent.Deps{
		Store:   store,
		Machine: &records.Machine{Store: store, Loc: loc, Now: time.Now},
		Blobs:   &agent.S3Blobs{Client: s3.NewFromConfig(cfg), Bucket: os.Getenv("UPLOADS_BUCKET")},
		Memory:  &agent.AgentCoreMemory{Client: bedrockagentcore.NewFromConfig(cfg), MemoryID: os.Getenv("MEMORY_ID"), ActorID: os.Getenv("ACTOR_ID")},
		LLM:     &agent.Bedrock{Client: bedrockruntime.NewFromConfig(cfg), ModelID: os.Getenv("MODEL_ID"), Extra: agent.DefaultExtra},
		Loc:     loc, Now: time.Now, Actor: os.Getenv("ACTOR_ID"),
	}
	lambda.Start(func(ctx context.Context, e event) error {
		// Finish (or fail) before Lambda kills us, so the turn never sticks at "running".
		if dl, ok := ctx.Deadline(); ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, dl.Add(-20*time.Second))
			defer cancel()
		}
		if lc, ok := lambdacontext.FromContext(ctx); ok {
			log.Printf("turn %s request %s", e.TurnID, lc.AwsRequestID)
		}
		r := &agent.Runner{Deps: deps}
		if err := r.RunTurn(ctx, e.TurnID); err != nil {
			log.Printf("turn %s failed: %v", e.TurnID, err)
		}
		return nil // never let Lambda retry: the turn record carries the failure
	})
}
