// Command smoke exercises the real DynamoDB condition expressions against a
// deployed stack: approve-once, claim-once, idempotent proposals.
// Usage: TABLE_RECORDS=... TABLE_PROPOSALS=... go run ./cmd/smoke
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"helperpay/pricing"
	"helperpay/records"
)

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		panic(err)
	}
	st := &records.DynamoStore{Client: dynamodb.NewFromConfig(cfg), Records: os.Getenv("TABLE_RECORDS"), Proposals: os.Getenv("TABLE_PROPOSALS")}
	fails := 0
	expect := func(name string, ok bool) {
		if ok {
			fmt.Println("  ok   ", name)
		} else {
			fails++
			fmt.Println("  FAIL ", name)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := "smoke-" + records.NewID()

	// proposals are idempotent on id
	p := records.Proposal{ID: id, Kind: "terms", Payload: []byte(`{"x":1}`), ContentHash: "h", ProposedBy: "smoke", Status: "open", CreatedAt: now}
	p1, err := st.PutProposal(ctx, p)
	p2, err2 := st.PutProposal(ctx, records.Proposal{ID: id, Kind: "terms", Payload: []byte(`{"x":2}`), Status: "open", CreatedAt: "later"})
	expect("proposal put", err == nil && err2 == nil)
	expect("proposal idempotent", p1.CreatedAt == p2.CreatedAt && string(p2.Payload) == `{"x":1}`)
	_, err = st.CloseProposal(ctx, id, "rejected", "", now)
	_, err2 = st.CloseProposal(ctx, id, "confirmed", "", now)
	expect("proposal closes once", err == nil && errors.Is(err2, records.ErrProposalClosed))

	// turns claim once
	t := records.Turn{ID: id, SessionID: "smoke", Status: records.TurnQueued, CreatedAt: now, UpdatedAt: now}
	expect("turn put", st.PutTurn(ctx, t) == nil)
	ok1, _ := st.ClaimTurn(ctx, id, now)
	ok2, _ := st.ClaimTurn(ctx, id, now)
	expect("turn claims once", ok1 && !ok2)

	// bills approve once, and only with the current check hash
	b := records.BillRecord{ID: id, HelperID: "smoke", Period: "2026-01", Status: records.BillChecked, CheckHash: "c1", CreatedAt: now, Bill: pricing.Bill{Period: "2026-01"}}
	expect("bill put", st.PutBill(ctx, b) == nil)
	_, err = st.ApproveBill(ctx, id, "stale", records.Approval{At: now})
	expect("approve with stale hash refused", errors.Is(err, records.ErrTermsChanged))
	_, err = st.ApproveBill(ctx, id, "c1", records.Approval{At: now, AmountCents: 1})
	expect("approve with current hash", err == nil)
	_, err = st.ApproveBill(ctx, id, "c1", records.Approval{At: now})
	expect("second approve refused", errors.Is(err, records.ErrAlreadyApproved))
	expect("approved bill rejects writes", errors.Is(st.PutBill(ctx, b), records.ErrAlreadyApproved))

	// clean up so nothing shows in the app
	ddb := dynamodb.NewFromConfig(cfg)
	for _, k := range []struct{ table, pk string }{{st.Proposals, "PROPOSAL#" + id}, {st.Proposals, "TURN#" + id}, {st.Records, "BILL#" + id}} {
		_, err := ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: &k.table, Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: k.pk}, "SK": &ddbtypes.AttributeValueMemberS{Value: "META"}}})
		expect("cleanup "+k.pk, err == nil)
	}
	if fails > 0 {
		fmt.Printf("%d smoke check(s) failed\n", fails)
		os.Exit(1)
	}
	fmt.Println("smoke: all good")
}
