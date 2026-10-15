package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
)

// AgentCoreMemory is the MemoryClient over Amazon Bedrock AgentCore Memory.
// Short-term: conversation events per (actor, session). Long-term: records
// extracted by the memory's strategies into the facts namespace, plus
// explicit "remember" records written directly.
type AgentCoreMemory struct {
	Client   *bedrockagentcore.Client
	MemoryID string
	ActorID  string
}

func (m *AgentCoreMemory) factsNamespace() string {
	return fmt.Sprintf("/household/%s/facts/", m.ActorID)
}

func (m *AgentCoreMemory) AddEvent(ctx context.Context, sessionID, clientToken, role, text string) error {
	r := types.RoleUser
	if role == "assistant" {
		r = types.RoleAssistant
	}
	_, err := m.Client.CreateEvent(ctx, &bedrockagentcore.CreateEventInput{
		MemoryId: aws.String(m.MemoryID), ActorId: aws.String(m.ActorID), SessionId: aws.String(sessionID),
		EventTimestamp: aws.Time(time.Now()), ClientToken: aws.String(token(clientToken)),
		Payload: []types.PayloadType{&types.PayloadTypeMemberConversational{Value: types.Conversational{Role: r, Content: &types.ContentMemberText{Value: text}}}},
	})
	return err
}

func (m *AgentCoreMemory) RecentEvents(ctx context.Context, sessionID string, n int) ([]Event, error) {
	// Sessions are small; read every page, then sort and take the tail.
	var events []types.Event
	var next *string
	for page := 0; page < 20; page++ {
		out, err := m.Client.ListEvents(ctx, &bedrockagentcore.ListEventsInput{
			MemoryId: aws.String(m.MemoryID), ActorId: aws.String(m.ActorID), SessionId: aws.String(sessionID),
			IncludePayloads: aws.Bool(true), MaxResults: aws.Int32(100), NextToken: next,
		})
		if err != nil {
			return nil, err
		}
		events = append(events, out.Events...)
		if out.NextToken == nil {
			break
		}
		next = out.NextToken
	}
	var evs []Event
	for _, e := range events {
		for _, p := range e.Payload {
			conv, ok := p.(*types.PayloadTypeMemberConversational)
			if !ok {
				continue
			}
			txt, ok := conv.Value.Content.(*types.ContentMemberText)
			if !ok {
				continue
			}
			role := "user"
			if conv.Value.Role == types.RoleAssistant {
				role = "assistant"
			}
			at := ""
			if e.EventTimestamp != nil {
				at = e.EventTimestamp.UTC().Format(time.RFC3339)
			}
			evs = append(evs, Event{Role: role, Text: txt.Value, At: at})
		}
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].At < evs[j].At })
	if len(evs) > n {
		evs = evs[len(evs)-n:]
	}
	return evs, nil
}

func (m *AgentCoreMemory) Retrieve(ctx context.Context, query string, k int) ([]MemoryRecord, error) {
	out, err := m.Client.RetrieveMemoryRecords(ctx, &bedrockagentcore.RetrieveMemoryRecordsInput{
		MemoryId: aws.String(m.MemoryID), Namespace: aws.String(m.factsNamespace()),
		SearchCriteria: &types.SearchCriteria{SearchQuery: aws.String(query), TopK: aws.Int32(int32(k))},
	})
	if err != nil {
		return nil, err
	}
	return summaries(out.MemoryRecordSummaries), nil
}

func (m *AgentCoreMemory) Remember(ctx context.Context, clientToken, text string) error {
	out, err := m.Client.BatchCreateMemoryRecords(ctx, &bedrockagentcore.BatchCreateMemoryRecordsInput{
		MemoryId: aws.String(m.MemoryID), ClientToken: aws.String(token(clientToken)),
		Records: []types.MemoryRecordCreateInput{{RequestIdentifier: aws.String(token(clientToken)), Namespaces: []string{m.factsNamespace()},
			Content: &types.MemoryContentMemberText{Value: text}, Timestamp: aws.Time(time.Now())}},
	})
	if err != nil {
		return err
	}
	if len(out.FailedRecords) > 0 { // the call can succeed while the record is refused
		return fmt.Errorf("memory record not stored: %s", aws.ToString(out.FailedRecords[0].ErrorMessage))
	}
	return nil
}

func (m *AgentCoreMemory) List(ctx context.Context) ([]MemoryRecord, error) {
	var all []MemoryRecord
	var next *string
	for i := 0; i < 5; i++ {
		out, err := m.Client.ListMemoryRecords(ctx, &bedrockagentcore.ListMemoryRecordsInput{
			MemoryId: aws.String(m.MemoryID), Namespace: aws.String(m.factsNamespace()), MaxResults: aws.Int32(50), NextToken: next,
		})
		if err != nil {
			return nil, err
		}
		all = append(all, summaries(out.MemoryRecordSummaries)...)
		if out.NextToken == nil {
			break
		}
		next = out.NextToken
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].CreatedAt > all[j].CreatedAt })
	return all, nil
}

func summaries(ss []types.MemoryRecordSummary) []MemoryRecord {
	var out []MemoryRecord
	for _, s := range ss {
		txt, ok := s.Content.(*types.MemoryContentMemberText)
		if !ok {
			continue
		}
		r := MemoryRecord{ID: aws.ToString(s.MemoryRecordId), Text: txt.Value, Score: aws.ToFloat64(s.Score)}
		if len(s.Namespaces) > 0 {
			r.Namespace = s.Namespaces[0]
		}
		if s.CreatedAt != nil {
			r.CreatedAt = s.CreatedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, r)
	}
	return out
}

// token makes a client token / request identifier the service accepts
// ([a-zA-Z0-9_-], at most 80 characters).
func token(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if len(out) > 80 {
		out = out[len(out)-80:]
	}
	if out == "" {
		out = "t"
	}
	return out
}

// Forget deletes the household facts, every session's events and (best effort)
// the per-session summaries. Every phase is attempted; errors are combined.
// Extraction is asynchronous, so a fact from a turn deleted mid-extraction can
// still appear a minute later.
func (m *AgentCoreMemory) Forget(ctx context.Context) error {
	var errs []error
	// Facts first: they are what the app shows and the model reads.
	if err := m.deleteRecords(ctx, fmt.Sprintf("/household/%s/", m.ActorID), false); err != nil {
		errs = append(errs, fmt.Errorf("delete facts: %w", err))
	}
	sessions, err := m.sessionIDs(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("list sessions: %w", err))
	}
	if err := parallel(8, sessions, func(sid string) error {
		if err := m.deleteEvents(ctx, sid); err != nil {
			return fmt.Errorf("delete events %s: %w", sid, err)
		}
		return nil
	}); err != nil {
		errs = append(errs, err)
	}
	// Session summaries are written by the summarisation strategy and the service
	// refuses to delete them by id ("memory record not found"); nothing reads them,
	// so a leftover summary is invisible. Best effort only.
	_ = m.deleteRecords(ctx, fmt.Sprintf("/sessions/%s/", m.ActorID), true)
	return errors.Join(errs...)
}

// ForgetSession deletes one conversation's events and (best effort) its summary.
func (m *AgentCoreMemory) ForgetSession(ctx context.Context, sessionID string) error {
	if err := m.deleteEvents(ctx, sessionID); err != nil {
		return fmt.Errorf("delete events: %w", err)
	}
	return m.deleteRecords(ctx, fmt.Sprintf("/sessions/%s/%s/", m.ActorID, sessionID), true)
}

func (m *AgentCoreMemory) sessionIDs(ctx context.Context) ([]string, error) {
	var ids []string
	var next *string
	for page := 0; ; page++ {
		if page == 100 {
			return nil, errors.New("too many sessions")
		}
		out, err := m.Client.ListSessions(ctx, &bedrockagentcore.ListSessionsInput{MemoryId: aws.String(m.MemoryID), ActorId: aws.String(m.ActorID), MaxResults: aws.Int32(100), NextToken: next})
		if err != nil {
			var nf *types.ResourceNotFoundException
			if errors.As(err, &nf) {
				return nil, nil // the actor has never spoken
			}
			return nil, err
		}
		for _, s := range out.SessionSummaries {
			ids = append(ids, aws.ToString(s.SessionId))
		}
		if out.NextToken == nil {
			return ids, nil
		}
		next = out.NextToken
	}
}

// deleteEvents lists every event of a session (all pages), then deletes them
// 8 at a time. One sweep: the listing lags deletes, so re-listing would only
// show ghosts. An event that is already gone (404) counts as deleted.
func (m *AgentCoreMemory) deleteEvents(ctx context.Context, sessionID string) error {
	var events []types.Event
	var next *string
	for page := 0; ; page++ {
		if page == 100 {
			return errors.New("too many events")
		}
		out, err := m.Client.ListEvents(ctx, &bedrockagentcore.ListEventsInput{MemoryId: aws.String(m.MemoryID), ActorId: aws.String(m.ActorID), SessionId: aws.String(sessionID), MaxResults: aws.Int32(100), NextToken: next})
		if err != nil {
			var nf *types.ResourceNotFoundException
			if errors.As(err, &nf) {
				return nil
			}
			return err
		}
		events = append(events, out.Events...)
		if out.NextToken == nil {
			break
		}
		next = out.NextToken
	}
	return parallel(8, events, func(e types.Event) error {
		_, err := m.Client.DeleteEvent(ctx, &bedrockagentcore.DeleteEventInput{MemoryId: aws.String(m.MemoryID), ActorId: aws.String(m.ActorID), SessionId: aws.String(sessionID), EventId: e.EventId})
		var nf *types.ResourceNotFoundException
		if errors.As(err, &nf) {
			return nil
		}
		return err
	})
}

// deleteRecords lists every record under a namespace path (all pages), then
// batch-deletes them by id. One sweep, for the same reason as deleteEvents. A
// 404 means the record is already gone; any other failure is an error, or with
// bestEffort just ignored.
func (m *AgentCoreMemory) deleteRecords(ctx context.Context, prefix string, bestEffort bool) error {
	var ids []string
	var next *string
	for page := 0; ; page++ {
		if page == 100 {
			return errors.New("too many records")
		}
		// Namespace is an exact match; NamespacePath lists every namespace under the prefix.
		out, err := m.Client.ListMemoryRecords(ctx, &bedrockagentcore.ListMemoryRecordsInput{MemoryId: aws.String(m.MemoryID), NamespacePath: aws.String(strings.TrimSuffix(prefix, "/")), MaxResults: aws.Int32(100), NextToken: next})
		if err != nil {
			var nf *types.ResourceNotFoundException
			if errors.As(err, &nf) {
				return nil
			}
			return err
		}
		for _, r := range out.MemoryRecordSummaries {
			ids = append(ids, aws.ToString(r.MemoryRecordId))
		}
		if out.NextToken == nil {
			break
		}
		next = out.NextToken
	}
	failed, msg := 0, ""
	for i := 0; i < len(ids); i += 25 {
		// Delete by id only: with Namespace set the service answers "memory record not found".
		var del []types.MemoryRecordDeleteInput
		for _, id := range ids[i:min(i+25, len(ids))] {
			del = append(del, types.MemoryRecordDeleteInput{MemoryRecordId: aws.String(id)})
		}
		res, err := m.Client.BatchDeleteMemoryRecords(ctx, &bedrockagentcore.BatchDeleteMemoryRecordsInput{MemoryId: aws.String(m.MemoryID), Records: del})
		if err != nil {
			return err
		}
		for _, f := range res.FailedRecords {
			if aws.ToInt32(f.ErrorCode) == 404 {
				continue
			}
			failed++
			if msg == "" {
				msg = ": " + aws.ToString(f.ErrorMessage)
			}
		}
	}
	if failed > 0 && !bestEffort {
		return fmt.Errorf("%d records could not be deleted%s", failed, msg)
	}
	return nil
}

// parallel runs fn over items with at most n in flight and returns the first error.
func parallel[T any](n int, items []T, fn func(T) error) error {
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	for _, it := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(it T) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := fn(it); err != nil {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}(it)
	}
	wg.Wait()
	return first
}
