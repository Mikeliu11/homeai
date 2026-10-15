package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"helperpay/records"
)

const (
	maxRounds    = 10
	recentEvents = 20
)

// Runner is the real Agent: tool loop over an LLM with AgentCore Memory.
type Runner struct {
	Deps
	proposals []string
}

func (r *Runner) addProposal(id string) {
	for _, p := range r.proposals {
		if p == id {
			return
		}
	}
	r.proposals = append(r.proposals, id)
}

// RunTurn claims the turn, runs the loop, and always leaves the turn done or failed.
func (r *Runner) RunTurn(ctx context.Context, turnID string) (err error) {
	now := func() string { return r.Now().UTC().Format(time.RFC3339) }
	ok, err := r.Store.ClaimTurn(ctx, turnID, now())
	if err != nil {
		return err
	}
	if !ok {
		return nil // duplicate delivery or already handled
	}
	t, err := r.Store.GetTurn(ctx, turnID)
	if err != nil {
		return err
	}
	t.Status = records.TurnRunning
	r.proposals = nil
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v", rec)
		}
		if err != nil {
			t.Status, t.Error, t.Progress, t.UpdatedAt = records.TurnFailed, friendly(err), "failed", now()
			_ = r.Store.UpdateTurn(context.WithoutCancel(ctx), t)
		}
	}()
	progress := func(p string) {
		t.Progress, t.UpdatedAt = p, now()
		_ = r.Store.UpdateTurn(ctx, t)
	}
	progress("reading what you sent")

	// 1. Build the user message from text + sources.
	var blocks []ContentBlock
	var sourceText []string
	if strings.TrimSpace(t.UserText) != "" {
		blocks = append(blocks, ContentBlock{Text: t.UserText})
	}
	for _, id := range t.SourceIDs {
		src, err := r.Store.GetSource(ctx, id)
		if err != nil {
			return fmt.Errorf("source %s: %w", id, err)
		}
		switch {
		case src.Text != "" && strings.TrimSpace(src.Text) == strings.TrimSpace(t.UserText):
			// the UI kept the paste as a source for provenance; the model sees it once
			blocks[0].Text = fmt.Sprintf("[Source %s, pasted text]\n%s", src.ID, src.Text)
			sourceText = append(sourceText, src.Text)
		case src.Text != "":
			blocks = append(blocks, ContentBlock{Text: fmt.Sprintf("[Source %s, pasted %s]\n%s", src.ID, src.Kind, src.Text)})
			sourceText = append(sourceText, src.Text)
		case src.S3Key != "":
			data, ct, err := r.Blobs.Get(ctx, src.S3Key)
			if err != nil {
				return fmt.Errorf("reading screenshot: %w", err)
			}
			blocks = append(blocks, ContentBlock{Text: fmt.Sprintf("[Source %s, screenshot %s]", src.ID, fileName(src.S3Key))})
			blocks = append(blocks, ContentBlock{ImageBytes: data, ImageType: imageType(ct)})
			sourceText = append(sourceText, fileName(src.S3Key))
		}
	}
	if len(blocks) == 0 {
		return fmt.Errorf("nothing to read")
	}

	// 2. Memory: recent events for continuity, long-term records for context.
	recent, _ := r.Memory.RecentEvents(ctx, t.SessionID, recentEvents)
	query := strings.TrimSpace(t.UserText + " " + strings.Join(sourceText, " "))
	if len(query) > 500 {
		query = query[:500]
	}
	// Household facts are few; when there are 40 or fewer, show them all so
	// nothing is missed by ranking. Beyond that, semantic search.
	memories, _ := r.Memory.List(ctx)
	if len(memories) > 40 && query != "" {
		memories, _ = r.Memory.Retrieve(ctx, query, 12)
	}
	memories = withoutMoney(memories)
	userEvent := t.UserText
	if len(sourceText) > 0 {
		userEvent = strings.TrimSpace(userEvent + "\n[attached: " + strings.Join(truncateAll(sourceText, 300), " | ") + "]")
	}
	_ = r.Memory.AddEvent(ctx, t.SessionID, t.ID+":user", "user", userEvent)

	// 3. Messages: replayed history, then this turn.
	var msgs []Message
	for _, e := range recent {
		if e.Text == "" {
			continue
		}
		msgs = append(msgs, Message{Role: e.Role, Content: []ContentBlock{{Text: e.Text}}})
	}
	msgs = normalizeRoles(msgs)
	msgs = append(msgs, Message{Role: "user", Content: blocks})
	today := r.Now().In(r.Loc).Format("Monday, January 2, 2006")
	system := SystemPrompt(today, r.helpersSummary(ctx), memories)

	// 4. Tool loop.
	var reply string
	var toolErrors []string
	toolIdx := 0
	for round := 0; round < maxRounds; round++ {
		progress(progressText(round))
		resp, err := r.LLM.Converse(ctx, ConverseRequest{System: system, Messages: msgs, Tools: Tools})
		if err != nil {
			return err
		}
		msgs = append(msgs, resp.Message) // includes reasoning blocks, passed back unchanged
		var results []ContentBlock
		for _, c := range resp.Message.Content {
			if c.Text != "" {
				reply = c.Text
			}
			if c.ToolUse != nil {
				progress("using " + strings.ReplaceAll(c.ToolUse.Name, "_", " "))
				out, err := r.execTool(ctx, t, toolIdx, *c.ToolUse)
				toolIdx++
				if err != nil {
					log.Printf("tool %s failed: %v", c.ToolUse.Name, err)
					toolErrors = append(toolErrors, err.Error())
					results = append(results, ContentBlock{ToolResult: &ToolResult{ToolUseID: c.ToolUse.ID, Text: "Error: " + err.Error(), IsError: true}})
				} else {
					results = append(results, ContentBlock{ToolResult: &ToolResult{ToolUseID: c.ToolUse.ID, Text: out}})
				}
			}
		}
		if resp.StopReason == "max_tokens" && len(results) == 0 {
			if len(r.proposals) == 0 {
				return fmt.Errorf("the reply was cut off before anything was proposed; try sending fewer screenshots at once")
			}
			reply = strings.TrimSpace(reply + "\n\n(My reply was cut short, but the cards above are complete.)")
			break
		}
		if resp.StopReason != "tool_use" || len(results) == 0 {
			break
		}
		msgs = append(msgs, Message{Role: "user", Content: results})
	}
	if strings.TrimSpace(reply) == "" {
		reply = "I've read what you sent. Have a look at the cards above and confirm what's right."
	}
	if len(r.proposals) == 0 && len(toolErrors) > 0 {
		// no card was created; make sure the reason is visible whatever the model wrote
		reply = strings.TrimSpace(reply) + "\n\nNo new card was created: " + toolErrors[len(toolErrors)-1]
	}
	_ = r.Memory.AddEvent(ctx, t.SessionID, t.ID+":assistant", "assistant", reply)
	t.Status, t.Reply, t.ProposalIDs, t.Progress, t.UpdatedAt = records.TurnDone, reply, r.proposals, "done", now()
	return r.Store.UpdateTurn(ctx, t)
}

func progressText(round int) string {
	switch round {
	case 0:
		return "reading what you sent"
	case 1:
		return "working out the terms and lines"
	default:
		return "checking the details"
	}
}

// normalizeRoles merges consecutive same-role messages so the model sees alternating turns.
func normalizeRoles(msgs []Message) []Message {
	var out []Message
	for _, m := range msgs {
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			out[n-1].Content = append(out[n-1].Content, m.Content...)
			continue
		}
		out = append(out, m)
	}
	if len(out) > 0 && out[0].Role != "user" {
		out = out[1:]
	}
	return out
}

func friendly(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "ThrottlingException") || strings.Contains(s, "TooManyRequests"):
		return "Bedrock is throttling requests right now. Wait a moment and send it again."
	case strings.Contains(s, "AccessDenied") || strings.Contains(s, "model access"):
		return "The Claude model is not enabled in this AWS account/region yet. See the README prerequisites."
	case strings.Contains(s, "context deadline exceeded"):
		return "That took too long. Try sending fewer screenshots at once."
	}
	return s
}

func fileName(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		key = key[i+1:]
	}
	if i := strings.Index(key, "-"); i >= 0 && len(key) > 25 { // strip the id prefix
		key = key[i+1:]
	}
	return key
}

func imageType(ct string) string {
	switch ct {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	}
	return "png"
}

func truncateAll(ss []string, n int) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		s = strings.Join(strings.Fields(s), " ")
		if len(s) > n {
			s = s[:n] + "…"
		}
		out[i] = s
	}
	return out
}

// withoutMoney drops memories that mention amounts: rates and totals live in
// records, and a stale "$140" in memory must never be quoted as agreed.
func withoutMoney(recs []MemoryRecord) []MemoryRecord {
	seen := map[string]bool{}
	var out []MemoryRecord
	for _, m := range recs {
		t := strings.TrimSpace(m.Text)
		if t == "" || strings.Contains(t, "$") || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, m)
	}
	return out
}
