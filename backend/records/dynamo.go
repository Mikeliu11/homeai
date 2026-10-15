package records

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// DynamoStore keeps records in two tables. Each item is one JSON document
// under "data" plus the keys and the attributes conditions need. The
// Records table is writable only by the api role; the agent role can
// write the Proposals table (proposals and turns).
type DynamoStore struct {
	Client    *dynamodb.Client
	Records   string // table name
	Proposals string // table name
}

type item struct {
	PK        string `dynamodbav:"PK"`
	SK        string `dynamodbav:"SK"`
	GSI1PK    string `dynamodbav:"GSI1PK,omitempty"`
	GSI1SK    string `dynamodbav:"GSI1SK,omitempty"`
	Status    string `dynamodbav:"status,omitempty"`
	CheckHash string `dynamodbav:"checkHash,omitempty"`
	Data      string `dynamodbav:"data"`
}

const gsi1 = "GSI1"

func encode(v any) string { b, _ := json.Marshal(v); return string(b) }

func (d *DynamoStore) put(ctx context.Context, table string, it item, cond *string, names map[string]string, values map[string]types.AttributeValue) error {
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return err
	}
	in := &dynamodb.PutItemInput{TableName: aws.String(table), Item: av, ConditionExpression: cond, ExpressionAttributeNames: names, ExpressionAttributeValues: values}
	_, err = d.Client.PutItem(ctx, in)
	return err
}

func (d *DynamoStore) get(ctx context.Context, table, pk, sk string, out any) error {
	res, err := d.Client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(table), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: pk}, "SK": &types.AttributeValueMemberS{Value: sk}}})
	if err != nil {
		return err
	}
	if res.Item == nil {
		return ErrNotFound
	}
	var it item
	if err := attributevalue.UnmarshalMap(res.Item, &it); err != nil {
		return err
	}
	return json.Unmarshal([]byte(it.Data), out)
}

func (d *DynamoStore) query(ctx context.Context, table string, index *string, keyName, keyValue string) ([]item, error) {
	var items []item
	var start map[string]types.AttributeValue
	for {
		res, err := d.Client.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(table), IndexName: index,
			ConsistentRead:            aws.Bool(index == nil), // base-table reads see our own writes; GSIs cannot
			KeyConditionExpression:    aws.String("#k = :v"),
			ExpressionAttributeNames:  map[string]string{"#k": keyName},
			ExpressionAttributeValues: map[string]types.AttributeValue{":v": &types.AttributeValueMemberS{Value: keyValue}},
			ExclusiveStartKey:         start})
		if err != nil {
			return nil, err
		}
		var page []item
		if err := attributevalue.UnmarshalListOfMaps(res.Items, &page); err != nil {
			return nil, err
		}
		items = append(items, page...)
		if res.LastEvaluatedKey == nil {
			break
		}
		start = res.LastEvaluatedKey
	}
	return items, nil
}

func decodeAll[T any](items []item) []T {
	out := make([]T, 0, len(items))
	for _, it := range items {
		var v T
		if json.Unmarshal([]byte(it.Data), &v) == nil {
			out = append(out, v)
		}
	}
	return out
}

func isCondFail(err error) bool {
	var ccf *types.ConditionalCheckFailedException
	return errors.As(err, &ccf)
}

// ---- helpers / sources / terms ----

func (d *DynamoStore) PutHelper(ctx context.Context, h Helper) error {
	return d.put(ctx, d.Records, item{PK: "HELPER#" + h.ID, SK: "META", GSI1PK: "HELPERS", GSI1SK: h.Name + "#" + h.ID, Data: encode(h)}, nil, nil, nil)
}
func (d *DynamoStore) GetHelper(ctx context.Context, id string) (Helper, error) {
	var h Helper
	return h, d.get(ctx, d.Records, "HELPER#"+id, "META", &h)
}
func (d *DynamoStore) ListHelpers(ctx context.Context) ([]Helper, error) {
	items, err := d.query(ctx, d.Records, aws.String(gsi1), "GSI1PK", "HELPERS")
	if err != nil {
		return nil, err
	}
	return decodeAll[Helper](items), nil
}
func (d *DynamoStore) PutSource(ctx context.Context, s Source) error {
	return d.put(ctx, d.Records, item{PK: "SOURCE#" + s.ID, SK: "META", Data: encode(s)}, nil, nil, nil)
}
func (d *DynamoStore) GetSource(ctx context.Context, id string) (Source, error) {
	var s Source
	return s, d.get(ctx, d.Records, "SOURCE#"+id, "META", &s)
}
func (d *DynamoStore) PutTermsVersion(ctx context.Context, v TermsVersion) error {
	return d.put(ctx, d.Records, item{PK: "TERMS#" + v.HelperID, SK: "V#" + v.ID, Data: encode(v)}, aws.String("attribute_not_exists(PK)"), nil, nil)
}
func (d *DynamoStore) ListTermsVersions(ctx context.Context, helperID string) ([]TermsVersion, error) {
	items, err := d.query(ctx, d.Records, nil, "PK", "TERMS#"+helperID)
	if err != nil {
		return nil, err
	}
	out := decodeAll[TermsVersion](items)
	sort.Slice(out, func(i, j int) bool { return out[i].Terms.EffectiveFrom < out[j].Terms.EffectiveFrom })
	return out, nil
}

// ---- bills ----

func (d *DynamoStore) PutBill(ctx context.Context, b BillRecord) error {
	err := d.put(ctx, d.Records, item{PK: "BILL#" + b.ID, SK: "META", GSI1PK: "BILLS", GSI1SK: b.HelperID + "#" + b.Period + "#" + b.CreatedAt,
		Status: string(b.Status), CheckHash: b.CheckHash, Data: encode(b)},
		aws.String("attribute_not_exists(PK) OR #st <> :approved"), map[string]string{"#st": "status"},
		map[string]types.AttributeValue{":approved": &types.AttributeValueMemberS{Value: string(BillApproved)}})
	if isCondFail(err) {
		return ErrAlreadyApproved
	}
	return err
}
func (d *DynamoStore) GetBill(ctx context.Context, id string) (BillRecord, error) {
	var b BillRecord
	return b, d.get(ctx, d.Records, "BILL#"+id, "META", &b)
}
func (d *DynamoStore) ListBills(ctx context.Context, helperID string) ([]BillRecord, error) {
	items, err := d.query(ctx, d.Records, aws.String(gsi1), "GSI1PK", "BILLS")
	if err != nil {
		return nil, err
	}
	all := decodeAll[BillRecord](items)
	var out []BillRecord
	for _, b := range all {
		if helperID == "" || b.HelperID == helperID {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Period != out[j].Period {
			return out[i].Period > out[j].Period
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out, nil
}
func (d *DynamoStore) ApproveBill(ctx context.Context, id, expectedCheckHash string, a Approval) (BillRecord, error) {
	b, err := d.GetBill(ctx, id)
	if err != nil {
		return b, err
	}
	if b.Status == BillApproved {
		return b, ErrAlreadyApproved
	}
	if b.CheckHash != expectedCheckHash {
		return b, ErrTermsChanged
	}
	b.Status, b.Approval, b.UpdatedAt = BillApproved, &a, a.At
	_, err = d.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(d.Records),
		Key:                      map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: "BILL#" + id}, "SK": &types.AttributeValueMemberS{Value: "META"}},
		UpdateExpression:         aws.String("SET #st = :approved, #data = :data"),
		ConditionExpression:      aws.String("attribute_exists(PK) AND #st <> :approved AND checkHash = :hash"),
		ExpressionAttributeNames: map[string]string{"#st": "status", "#data": "data"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":approved": &types.AttributeValueMemberS{Value: string(BillApproved)},
			":data":     &types.AttributeValueMemberS{Value: encode(b)},
			":hash":     &types.AttributeValueMemberS{Value: expectedCheckHash}}})
	if isCondFail(err) {
		cur, _ := d.GetBill(ctx, id)
		if cur.Status == BillApproved {
			return cur, ErrAlreadyApproved
		}
		return cur, ErrTermsChanged
	}
	return b, err
}

// ---- proposals / turns ----

func (d *DynamoStore) PutProposal(ctx context.Context, p Proposal) (Proposal, error) {
	err := d.put(ctx, d.Proposals, item{PK: "PROPOSAL#" + p.ID, SK: "META", GSI1PK: "PROPOSALS", GSI1SK: p.CreatedAt + "#" + p.ID, Status: p.Status, Data: encode(p)},
		aws.String("attribute_not_exists(PK)"), nil, nil)
	if isCondFail(err) {
		return d.GetProposal(ctx, p.ID)
	}
	return p, err
}
func (d *DynamoStore) GetProposal(ctx context.Context, id string) (Proposal, error) {
	var p Proposal
	return p, d.get(ctx, d.Proposals, "PROPOSAL#"+id, "META", &p)
}
func (d *DynamoStore) ListProposals(ctx context.Context, status string) ([]Proposal, error) {
	items, err := d.query(ctx, d.Proposals, aws.String(gsi1), "GSI1PK", "PROPOSALS")
	if err != nil {
		return nil, err
	}
	var out []Proposal
	for _, p := range decodeAll[Proposal](items) {
		if status == "" || p.Status == status {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}
func (d *DynamoStore) CloseProposal(ctx context.Context, id, status, resultID, at string) (Proposal, error) {
	p, err := d.GetProposal(ctx, id)
	if err != nil {
		return p, err
	}
	if p.Status != "open" {
		return p, ErrProposalClosed
	}
	p.Status, p.ResultID, p.ClosedAt = status, resultID, at
	_, err = d.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(d.Proposals),
		Key:                       map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: "PROPOSAL#" + id}, "SK": &types.AttributeValueMemberS{Value: "META"}},
		UpdateExpression:          aws.String("SET #st = :st, #data = :data"),
		ConditionExpression:       aws.String("#st = :open"),
		ExpressionAttributeNames:  map[string]string{"#st": "status", "#data": "data"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":st": &types.AttributeValueMemberS{Value: status}, ":open": &types.AttributeValueMemberS{Value: "open"}, ":data": &types.AttributeValueMemberS{Value: encode(p)}}})
	if isCondFail(err) {
		return p, ErrProposalClosed
	}
	return p, err
}
func (d *DynamoStore) PutTurn(ctx context.Context, t Turn) error {
	err := d.put(ctx, d.Proposals, item{PK: "TURN#" + t.ID, SK: "META", GSI1PK: "SESSION#" + t.SessionID, GSI1SK: t.CreatedAt + "#" + t.ID, Status: string(t.Status), Data: encode(t)},
		aws.String("attribute_not_exists(PK)"), nil, nil)
	if isCondFail(err) {
		return nil
	}
	return err
}
func (d *DynamoStore) GetTurn(ctx context.Context, id string) (Turn, error) {
	var t Turn
	return t, d.get(ctx, d.Proposals, "TURN#"+id, "META", &t)
}
func (d *DynamoStore) ListTurns(ctx context.Context, sessionID string) ([]Turn, error) {
	items, err := d.query(ctx, d.Proposals, aws.String(gsi1), "GSI1PK", "SESSION#"+sessionID)
	if err != nil {
		return nil, err
	}
	out := decodeAll[Turn](items)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}
func (d *DynamoStore) ClaimTurn(ctx context.Context, id, at string) (bool, error) {
	t, err := d.GetTurn(ctx, id)
	if err != nil {
		return false, err
	}
	if t.Status != TurnQueued {
		return false, nil
	}
	t.Status, t.UpdatedAt = TurnRunning, at
	_, err = d.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(d.Proposals),
		Key:                       map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: "TURN#" + id}, "SK": &types.AttributeValueMemberS{Value: "META"}},
		UpdateExpression:          aws.String("SET #st = :running, #data = :data"),
		ConditionExpression:       aws.String("#st = :queued"),
		ExpressionAttributeNames:  map[string]string{"#st": "status", "#data": "data"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":running": &types.AttributeValueMemberS{Value: string(TurnRunning)}, ":queued": &types.AttributeValueMemberS{Value: string(TurnQueued)}, ":data": &types.AttributeValueMemberS{Value: encode(t)}}})
	if isCondFail(err) {
		return false, nil
	}
	return err == nil, err
}
func (d *DynamoStore) UpdateTurn(ctx context.Context, t Turn) error {
	_, err := d.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(d.Proposals),
		Key:                       map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: "TURN#" + t.ID}, "SK": &types.AttributeValueMemberS{Value: "META"}},
		UpdateExpression:          aws.String("SET #st = :st, #data = :data"),
		ConditionExpression:       aws.String("attribute_exists(PK)"),
		ExpressionAttributeNames:  map[string]string{"#st": "status", "#data": "data"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":st": &types.AttributeValueMemberS{Value: string(t.Status)}, ":data": &types.AttributeValueMemberS{Value: encode(t)}}})
	if isCondFail(err) {
		return ErrNotFound
	}
	return err
}

// ---- audit ----

func (d *DynamoStore) AppendAudit(ctx context.Context, e AuditEvent) error {
	if e.TS == "" {
		e.TS = time.Now().UTC().Format(time.RFC3339)
	}
	return d.put(ctx, d.Records, item{PK: "AUDIT", SK: e.TS + "#" + NewID(), Data: encode(e)}, nil, nil, nil)
}
func (d *DynamoStore) ListAudit(ctx context.Context) ([]AuditEvent, error) {
	items, err := d.query(ctx, d.Records, nil, "PK", "AUDIT")
	if err != nil {
		return nil, err
	}
	out := decodeAll[AuditEvent](items)
	sort.Slice(out, func(i, j int) bool { return out[i].TS > out[j].TS })
	return out, nil
}

var _ Store = (*DynamoStore)(nil)

// Reset scans both tables and deletes every item in batches of 25.
func (d *DynamoStore) Reset(ctx context.Context) error {
	for _, table := range []string{d.Records, d.Proposals} {
		if err := d.wipeTable(ctx, table); err != nil {
			return fmt.Errorf("reset %s: %w", table, err)
		}
	}
	return nil
}

func (d *DynamoStore) wipeTable(ctx context.Context, table string) error {
	var start map[string]types.AttributeValue
	for {
		res, err := d.Client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(table), ProjectionExpression: aws.String("PK, SK"), ExclusiveStartKey: start})
		if err != nil {
			return err
		}
		for i := 0; i < len(res.Items); i += 25 {
			end := min(i+25, len(res.Items))
			reqs := make([]types.WriteRequest, 0, end-i)
			for _, it := range res.Items[i:end] {
				reqs = append(reqs, types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: map[string]types.AttributeValue{"PK": it["PK"], "SK": it["SK"]}}})
			}
			pending := map[string][]types.WriteRequest{table: reqs}
			for attempt := 0; len(pending[table]) > 0 && attempt < 8; attempt++ {
				if attempt > 0 {
					time.Sleep(time.Duration(50<<attempt) * time.Millisecond) // 100 ms … 6.4 s
				}
				out, err := d.Client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: pending})
				if err != nil {
					return err
				}
				pending = out.UnprocessedItems
			}
			if len(pending[table]) > 0 {
				return fmt.Errorf("%d items could not be deleted", len(pending[table]))
			}
		}
		if res.LastEvaluatedKey == nil {
			return nil
		}
		start = res.LastEvaluatedKey
	}
}

// OpenTurns scans the Proposals table for queued or running turns (reset guard only).
func (d *DynamoStore) OpenTurns(ctx context.Context) ([]Turn, error) {
	var out []Turn
	var start map[string]types.AttributeValue
	for {
		res, err := d.Client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(d.Proposals), ExclusiveStartKey: start,
			FilterExpression:         aws.String("begins_with(PK, :t) AND #s IN (:q, :r)"),
			ExpressionAttributeNames: map[string]string{"#s": "status"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":t": &types.AttributeValueMemberS{Value: "TURN#"},
				":q": &types.AttributeValueMemberS{Value: string(TurnQueued)}, ":r": &types.AttributeValueMemberS{Value: string(TurnRunning)}}})
		if err != nil {
			return nil, err
		}
		var items []item
		if err := attributevalue.UnmarshalListOfMaps(res.Items, &items); err != nil {
			return nil, err
		}
		out = append(out, decodeAll[Turn](items)...)
		if res.LastEvaluatedKey == nil {
			return out, nil
		}
		start = res.LastEvaluatedKey
	}
}

// ListSessions scans the turn items (one household, a few hundred at most) and groups them.
func (d *DynamoStore) ListSessions(ctx context.Context) ([]SessionSummary, error) {
	var all []Turn
	var start map[string]types.AttributeValue
	for {
		res, err := d.Client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(d.Proposals), ExclusiveStartKey: start,
			FilterExpression: aws.String("begins_with(PK, :t)"), ExpressionAttributeValues: map[string]types.AttributeValue{":t": &types.AttributeValueMemberS{Value: "TURN#"}}})
		if err != nil {
			return nil, err
		}
		var items []item
		if err := attributevalue.UnmarshalListOfMaps(res.Items, &items); err != nil {
			return nil, err
		}
		all = append(all, decodeAll[Turn](items)...)
		if res.LastEvaluatedKey == nil {
			break
		}
		start = res.LastEvaluatedKey
	}
	return SummariseSessions(all), nil
}

func (d *DynamoStore) DeleteSession(ctx context.Context, sessionID string) error {
	items, err := d.query(ctx, d.Proposals, aws.String(gsi1), "GSI1PK", "SESSION#"+sessionID)
	if err != nil {
		return err
	}
	for _, it := range items {
		if _, err := d.Client.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(d.Proposals),
			Key: map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: it.PK}, "SK": &types.AttributeValueMemberS{Value: it.SK}}}); err != nil {
			return err
		}
	}
	return nil
}
