// Command preflight checks a fresh AWS account before `npm run deploy`:
// Go version, region support, CDK bootstrap, and one tiny Claude call.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Regions where AgentCore Memory is available (spec §3; verified 2026-10-09).
var memoryRegions = []string{"us-east-1", "us-west-2", "eu-central-1", "ap-southeast-2", "us-east-2", "eu-west-1", "ap-south-1", "ap-southeast-1", "ap-northeast-1", "eu-west-3", "ap-northeast-2", "eu-north-1", "eu-west-2", "ca-central-1", "sa-east-1"}

func main() {
	fails := 0
	check := func(name string, ok bool, fix string) {
		if ok {
			fmt.Printf("  ok    %s\n", name)
		} else {
			fails++
			fmt.Printf("  FAIL  %s\n        fix: %s\n", name, fix)
		}
	}
	fmt.Println("helper-pay preflight")

	// Go
	out, _ := exec.Command("go", "version").Output()
	m := regexp.MustCompile(`go(\d+)\.(\d+)`).FindStringSubmatch(string(out))
	goOK := false
	if len(m) == 3 {
		maj, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		goOK = maj > 1 || (maj == 1 && min >= 26)
	}
	check("Go 1.26+ ("+strings.TrimSpace(string(out))+")", goOK, "install Go 1.26 or newer from https://go.dev/dl/")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg, err := config.LoadDefaultConfig(ctx)
	check("AWS credentials load", err == nil, "run `aws configure` or export AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY (and AWS_REGION)")
	if err != nil {
		os.Exit(1)
	}
	region := cfg.Region
	if r := os.Getenv("CDK_DEFAULT_REGION"); r != "" {
		region = r
	}
	if region == "" {
		region = "us-east-1"
	}
	cfg.Region = region
	fmt.Printf("  region %s\n", region)
	regionOK := false
	for _, r := range memoryRegions {
		if r == region {
			regionOK = true
		}
	}
	check("region supports AgentCore Memory", regionOK, "set AWS_REGION to one of "+strings.Join(memoryRegions, ", "))

	id, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	check("credentials valid", err == nil, "check the access key; "+errStr(err))
	if err == nil {
		fmt.Printf("  account %s as %s\n", aws.ToString(id.Account), aws.ToString(id.Arn))
	}

	_, err = cloudformation.NewFromConfig(cfg).DescribeStacks(ctx, &cloudformation.DescribeStacksInput{StackName: aws.String("CDKToolkit")})
	check("CDK bootstrapped in "+region, err == nil, "run `npm run bootstrap` from the repo root (once per account+region)")

	model := os.Getenv("MODEL_ID")
	if model == "" {
		model = "global.anthropic.claude-sonnet-4-6"
	}
	br := bedrockruntime.NewFromConfig(cfg)
	in := &bedrockruntime.ConverseInput{ModelId: aws.String(model),
		Messages:        []types.Message{{Role: types.ConversationRoleUser, Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: "Reply with the single word: ok"}}}},
		InferenceConfig: &types.InferenceConfiguration{MaxTokens: aws.Int32(64)}}
	_, err = br.Converse(ctx, in)
	check("Claude on Bedrock answers ("+model+")", err == nil, "open the Bedrock console → Model access → Anthropic, submit the one-time use-case form, wait a few minutes; "+errStr(err))
	if err == nil {
		in.AdditionalModelRequestFields = document.NewLazyDocument(map[string]any{"thinking": map[string]any{"type": "adaptive"}, "output_config": map[string]any{"effort": "low"}})
		_, err = br.Converse(ctx, in)
		if err != nil {
			fmt.Printf("  note  model rejects the thinking-effort fields; the agent will run without them (%s)\n", short(errStr(err)))
		} else {
			fmt.Println("  ok    thinking-effort fields accepted")
		}
	}

	if fails > 0 {
		fmt.Printf("\n%d check(s) failed. Fix them, then run `npm run deploy`.\n", fails)
		os.Exit(1)
	}
	fmt.Println("\nAll good. Run `npm run deploy`.")
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
func short(s string) string {
	if len(s) > 140 {
		return s[:140] + "…"
	}
	return s
}
