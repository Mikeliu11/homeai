# Helper Pay

An assistant for paying the people who help at home. Jordan drops in whatever exists about a helper (typed notes, pasted texts, screenshots), confirms the terms the assistant extracts, pastes the month's bill, and gets a line-by-line check against what was agreed. Then Jordan approves an amount, and the bill locks.

- **Model:** Claude Sonnet 4.6 on Amazon Bedrock, model ID `global.anthropic.claude-sonnet-4-6` (the global cross-region inference profile). Any Claude model that supports the Converse API with tools and images works: deploy with `--context modelId=global.anthropic.claude-sonnet-5-5` for Sonnet 5.5. The default is 4.6 because the Claude 5 generation is gated on some accounts (new or free-plan accounts get "not available for this account, contact AWS Sales").
- **Memory:** Amazon Bedrock AgentCore Memory (short-term conversation events + long-term household facts).
- **Region:** default `us-east-1`; any region where AgentCore Memory is available works (see "Regions").
- **Infra:** AWS CDK (TypeScript). Backend in Go on Lambda. React UI on CloudFront. Login via Cognito.

## Prerequisites

1. **Node.js 22+** and **Go 1.26+** (`go version`). The Go Lambdas are compiled on your machine during deploy; no Docker needed.
2. **AWS credentials** for the target account with admin rights (`aws configure`, or exported `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_REGION`).
3. **Anthropic model access.** Bedrock enables serverless models automatically, but Anthropic models need a one-time use-case form per account: AWS console → Amazon Bedrock → *Model access* → find **Claude Sonnet 4.6** → submit the form. The first call also starts a Marketplace subscription, which can take a few minutes to settle. Brand-new accounts are also held for "verification" for up to two hours after creation; `npm run preflight` shows the exact message.

## Deploy

```bash
npm install
npm run bootstrap    # once per account+region: CDK's bootstrap stack
npm run preflight    # checks Go version, region, bootstrap, and that Claude answers
npm run deploy       # builds the UI, compiles the Go Lambdas, deploys the stack
```

At the end the stack prints what you need:

```
HelperPay.AppUrl          = https://xxxx.cloudfront.net
HelperPay.Username        = jordan
HelperPay.PasswordCommand = aws secretsmanager get-secret-value --region us-east-1 --secret-id arn:... --query SecretString --output text
```

Run the `PasswordCommand`, open the `AppUrl`, sign in as `jordan` with that password. The app starts empty.

Deploy takes about 5–8 minutes (CloudFront is the slow part). A region other than the default: `AWS_REGION=us-west-2 npm run deploy`.

## Tear down

```bash
npm run destroy
```

Everything is created with `RemovalPolicy.DESTROY` (tables, buckets, log groups, the user pool, the secret, the AgentCore memory). The only leftovers are two tiny CloudWatch log groups from CDK's own custom-resource helpers, which cost nothing.

## Walkthrough with the starter pack

1. **Home** → paste `01-household-notes.txt`. The assistant remembers household facts (see **Memories**) and proposes Theo's undated $25/walk terms. Confirm.
2. Drop `02-priya-rates.png`, `03-lupe-rates.png`, `05-theo-rates.png`, and paste `04-lupe-rate-change.txt`, in any order. Each becomes a terms card with the source date, assumptions ("starting the 15th" = Sep 15) and conflicts (Jordan's $25 note vs Theo's $28 text). Confirm each.
3. Paste or drop the three September bills. Each becomes a bill card; confirm, and the engine prices every line against the terms in force on that date.
4. **Bills** → open each:
   - Priya: *Pay $556, not $626 — 2 problems.* The $40 last-minute fee was never agreed (Sam's say-so is quoted, not trusted), and her lines add to $596, not $626.
   - Lupe: *Pay $800, not $820 — 2 problems.* Sep 1 and Sep 8 billed $150; the $150 rate starts Sep 15.
   - Theo: *Pay $580 — matches what you agreed.* The Aug 27 text supersedes Jordan's note; 20 walks of 21 usual weekdays (Labor Day skipped) is noted.
5. **Approve.** Each flagged line needs an explicit include/exclude. The amount defaults to what the agreement supports. After approval the bill is locked and a message to the helper is drafted.

## Conversations

Home lists past conversations on the right. Open one to continue it (the assistant reads that thread back from AgentCore Memory), or delete it: its messages and memory events go, any card from it that was never confirmed is rejected, and confirmed terms and bills are untouched. One audit entry records the deletion.

## Starting over

**Settings → Start over** deletes every helper, terms version, bill, approval, conversation, upload and memory record, so the household is back to the state right after deployment (the login stays). Type `RESET` to confirm. Useful for running the starter pack again or trying a fresh set of inputs. Facts extracted by AgentCore Memory from a turn that was mid-extraction can reappear a minute or two later; **Memories → Clear memories** removes them without touching records. Per-conversation summary records written by the summarisation strategy cannot be deleted by id (the service answers "not found"); nothing in the app reads them, so they are left behind.

## Local development (no AWS)

```bash
npm run dev     # Go API on :8787 with an in-memory store and a scripted stand-in for Claude; UI on :5173
npm test        # Go unit tests (pricing engine, state machine, API, agent loop) + CDK synth tests
```

The scripted assistant recognises the starter pack by its wording, so the whole flow can be exercised offline. It is never deployed.

## Making a change live

Backend or UI changes: `npm run deploy` (full, ~3 min). Lambda-only changes: `npm -w ui run build && npm -w infra exec -- cdk deploy --hotswap` (~15 s; skips CloudFormation, fine for a demo, not for production).

## Live checks after deploy

- `npm run preflight` — before deploying.
- Smoke-test the DynamoDB condition expressions against the deployed tables with the `SmokeCommand` the stack prints.

## Regions

AgentCore Memory is available in: us-east-1, us-west-2, eu-central-1, ap-southeast-2, us-east-2, eu-west-1, ap-south-1, ap-southeast-1, ap-northeast-1, eu-west-3, ap-northeast-2, eu-north-1, eu-west-2, ca-central-1, sa-east-1. `cdk synth` refuses any other region with a clear message. The model is reached through the global inference profile, so it works from all of these.

## Cost

Pennies per day while idle (DynamoDB on-demand, S3, CloudFront, Lambda all within free tier). Model calls are the only real cost: roughly $0.01–0.05 per turn with a screenshot. Tear the stack down when done.

## Known issues

- **Brand-new AWS accounts** sometimes cannot create CloudFront distributions for the first few hours ("your account must be verified"). Wait and redeploy.
- **Bedrock quotas on new accounts are small.** If the assistant reports throttling, wait a moment and resend. The turn record keeps the error visible instead of hanging.
- **Long-term memory lags by about a minute** after a conversation; the **Memories** tab fills in then (use Refresh).
- **Screenshots must be PNG, JPEG, WebP or GIF** (what Bedrock accepts). Oversized images are shrunk in the browser; HEIC is converted only where the browser can decode it (Safari). On Chrome, export the screenshot as PNG/JPEG first.

## Layout

```
backend/   Go module: pricing (engine), records (state machine + DynamoDB), api (HTTP), agent (Claude loop + AgentCore Memory), cmd/{api,agent,local,preflight,smoke}
ui/        React + Vite SPA (Amplify Authenticator for Cognito)
infra/     CDK stack: Cognito, DynamoDB ×2, S3, CloudFront, API Gateway, Go Lambdas, AgentCore Memory
DESIGN.md  Decisions, failure modes, what was left out
```
