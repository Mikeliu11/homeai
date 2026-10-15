#!/usr/bin/env node
import * as cdk from "aws-cdk-lib";
import * as path from "path";
import * as fs from "fs";
import { HelperPayStack } from "../lib/stack";
import { assertSupportedRegion } from "../lib/regions";

const app = new cdk.App();
const region = app.node.tryGetContext("region") || process.env.CDK_DEFAULT_REGION || process.env.AWS_REGION || "us-east-1";
assertSupportedRegion(region);
const uiDist = path.resolve(__dirname, "..", "..", "ui", "dist");
if (!fs.existsSync(path.join(uiDist, "index.html")) && !process.env.HELPERPAY_SKIP_UI_CHECK) {
  throw new Error("ui/dist is missing. Run `npm run deploy` from the repo root (it builds the UI first), or `npm -w ui run build`.");
}
new HelperPayStack(app, app.node.tryGetContext("stackName") || "HelperPay", {
  env: { account: process.env.CDK_DEFAULT_ACCOUNT, region },
  modelId: app.node.tryGetContext("modelId") || "global.anthropic.claude-sonnet-4-6",
  username: app.node.tryGetContext("username") || "jordan",
  uiDist,
  backendDir: path.resolve(__dirname, "..", "..", "backend"),
  description: "helper-pay: an assistant for paying the people who help at home (take-home)",
});
