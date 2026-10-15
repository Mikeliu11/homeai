// Command api is the Lambda behind API Gateway. It owns every write to the
// Records table; the agent never gets that permission.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	lambdasvc "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"helperpay/agent"
	"helperpay/api"
	"helperpay/pricing"
	"helperpay/records"
)

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatal(err)
	}
	store := &records.DynamoStore{Client: dynamodb.NewFromConfig(cfg), Records: os.Getenv("TABLE_RECORDS"), Proposals: os.Getenv("TABLE_PROPOSALS")}
	loc := pricing.NY()
	srv := &api.Server{
		Store:       store,
		Machine:     &records.Machine{Store: store, Loc: loc, Now: time.Now},
		Invoker:     &api.LambdaInvoker{Client: lambdasvc.NewFromConfig(cfg), Function: os.Getenv("AGENT_FUNCTION")},
		Uploads:     &agent.S3Blobs{Client: s3.NewFromConfig(cfg), Bucket: os.Getenv("UPLOADS_BUCKET")},
		Memory:      &agent.AgentCoreMemory{Client: bedrockagentcore.NewFromConfig(cfg), MemoryID: os.Getenv("MEMORY_ID"), ActorID: os.Getenv("ACTOR_ID")},
		Now:         time.Now,
		StaleAfter:  330 * time.Second,
		AllowOrigin: os.Getenv("ALLOW_ORIGIN"),
	}
	lambda.Start(api.LambdaHandler(srv.Handler()))
}
