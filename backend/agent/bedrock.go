package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// Bedrock is the LLM adapter over the Converse API.
type Bedrock struct {
	Client    *bedrockruntime.Client
	ModelID   string
	MaxTokens int32
	// Extra holds model-specific request fields (thinking effort). If the
	// model rejects them, the adapter retries once without and remembers.
	Extra map[string]any

	mu        sync.Mutex
	dropExtra bool
}

// DefaultExtra asks for low thinking effort: extraction is cheap and the
// engine does the arithmetic.
var DefaultExtra = map[string]any{"thinking": map[string]any{"type": "adaptive"}, "output_config": map[string]any{"effort": "low"}}

func (b *Bedrock) Converse(ctx context.Context, req ConverseRequest) (ConverseResponse, error) {
	in := &bedrockruntime.ConverseInput{
		ModelId:         aws.String(b.ModelID),
		System:          []types.SystemContentBlock{&types.SystemContentBlockMemberText{Value: req.System}},
		Messages:        toSDKMessages(req.Messages),
		InferenceConfig: &types.InferenceConfiguration{MaxTokens: aws.Int32(b.maxTokens())},
	}
	if len(req.Tools) > 0 {
		tc := &types.ToolConfiguration{}
		for _, t := range req.Tools {
			tc.Tools = append(tc.Tools, &types.ToolMemberToolSpec{Value: types.ToolSpecification{
				Name: aws.String(t.Name), Description: aws.String(t.Description),
				InputSchema: &types.ToolInputSchemaMemberJson{Value: document.NewLazyDocument(t.Schema)},
			}})
		}
		in.ToolConfig = tc
	}
	b.mu.Lock()
	extra := b.Extra
	if b.dropExtra {
		extra = nil
	}
	b.mu.Unlock()
	if len(extra) > 0 {
		in.AdditionalModelRequestFields = document.NewLazyDocument(extra)
	}

	var out *bedrockruntime.ConverseOutput
	var err error
	backoff := 2 * time.Second
	for attempt := 0; attempt < 5; attempt++ {
		out, err = b.Client.Converse(ctx, in)
		if err == nil {
			break
		}
		msg := err.Error()
		if len(extra) > 0 && strings.Contains(msg, "ValidationException") && (strings.Contains(msg, "thinking") || strings.Contains(msg, "effort") || strings.Contains(msg, "output_config") || strings.Contains(msg, "additionalModelRequestFields")) {
			log.Printf("model rejected extra request fields (%v); retrying without", err)
			b.mu.Lock()
			b.dropExtra = true
			b.mu.Unlock()
			in.AdditionalModelRequestFields = nil
			extra = nil
			continue
		}
		if strings.Contains(msg, "ThrottlingException") || strings.Contains(msg, "ServiceUnavailable") || strings.Contains(msg, "ModelNotReady") {
			select {
			case <-ctx.Done():
				return ConverseResponse{}, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			continue
		}
		return ConverseResponse{}, err
	}
	if err != nil {
		return ConverseResponse{}, err
	}
	msgOut, ok := out.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return ConverseResponse{}, errors.New("no message in model output")
	}
	return ConverseResponse{Message: fromSDKMessage(msgOut.Value), StopReason: string(out.StopReason)}, nil
}

func (b *Bedrock) maxTokens() int32 {
	if b.MaxTokens > 0 {
		return b.MaxTokens
	}
	return 16000
}

func toSDKMessages(msgs []Message) []types.Message {
	out := make([]types.Message, 0, len(msgs))
	for _, m := range msgs {
		sm := types.Message{Role: types.ConversationRole(m.Role)}
		for _, c := range m.Content {
			switch {
			case c.Reasoning != nil:
				if len(c.Reasoning.Redacted) > 0 {
					sm.Content = append(sm.Content, &types.ContentBlockMemberReasoningContent{Value: &types.ReasoningContentBlockMemberRedactedContent{Value: c.Reasoning.Redacted}})
				} else {
					sm.Content = append(sm.Content, &types.ContentBlockMemberReasoningContent{Value: &types.ReasoningContentBlockMemberReasoningText{Value: types.ReasoningTextBlock{Text: aws.String(c.Reasoning.Text), Signature: aws.String(c.Reasoning.Signature)}}})
				}
			case c.ToolUse != nil:
				sm.Content = append(sm.Content, &types.ContentBlockMemberToolUse{Value: types.ToolUseBlock{ToolUseId: aws.String(c.ToolUse.ID), Name: aws.String(c.ToolUse.Name), Input: document.NewLazyDocument(c.ToolUse.Input)}})
			case c.ToolResult != nil:
				st := types.ToolResultStatusSuccess
				if c.ToolResult.IsError {
					st = types.ToolResultStatusError
				}
				sm.Content = append(sm.Content, &types.ContentBlockMemberToolResult{Value: types.ToolResultBlock{ToolUseId: aws.String(c.ToolResult.ToolUseID), Status: st,
					Content: []types.ToolResultContentBlock{&types.ToolResultContentBlockMemberText{Value: c.ToolResult.Text}}}})
			case len(c.ImageBytes) > 0:
				sm.Content = append(sm.Content, &types.ContentBlockMemberImage{Value: types.ImageBlock{Format: types.ImageFormat(c.ImageType), Source: &types.ImageSourceMemberBytes{Value: c.ImageBytes}}})
			case c.Text != "":
				sm.Content = append(sm.Content, &types.ContentBlockMemberText{Value: c.Text})
			}
		}
		if len(sm.Content) == 0 {
			continue
		}
		out = append(out, sm)
	}
	return out
}

func fromSDKMessage(m types.Message) Message {
	out := Message{Role: string(m.Role)}
	for _, c := range m.Content {
		switch v := c.(type) {
		case *types.ContentBlockMemberText:
			out.Content = append(out.Content, ContentBlock{Text: v.Value})
		case *types.ContentBlockMemberToolUse:
			var input map[string]any
			if v.Value.Input != nil {
				_ = v.Value.Input.UnmarshalSmithyDocument(&input)
			}
			if input == nil {
				input = map[string]any{}
			}
			out.Content = append(out.Content, ContentBlock{ToolUse: &ToolUse{ID: aws.ToString(v.Value.ToolUseId), Name: aws.ToString(v.Value.Name), Input: normalizeNumbers(input)}})
		case *types.ContentBlockMemberReasoningContent:
			switch r := v.Value.(type) {
			case *types.ReasoningContentBlockMemberReasoningText:
				out.Content = append(out.Content, ContentBlock{Reasoning: &ReasoningBlock{Text: aws.ToString(r.Value.Text), Signature: aws.ToString(r.Value.Signature)}})
			case *types.ReasoningContentBlockMemberRedactedContent:
				out.Content = append(out.Content, ContentBlock{Reasoning: &ReasoningBlock{Redacted: r.Value}})
			}
		}
	}
	return out
}

// normalizeNumbers turns smithy document numbers into plain float64/strings
// so json.Marshal of the tool input behaves.
func normalizeNumbers(v map[string]any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}
