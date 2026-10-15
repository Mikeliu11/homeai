import assert from "node:assert/strict";
import * as cdk from "aws-cdk-lib";
import { Template } from "aws-cdk-lib/assertions";
import * as path from "path";
import { HelperPayStack } from "../lib/stack";
import { assertSupportedRegion } from "../lib/regions";

process.env.HELPERPAY_SKIP_UI_CHECK = "1";

function synth() {
  const app = new cdk.App({ context: { "aws:cdk:bundling-stacks": [] } }); // skip Go builds in tests
  const stack = new HelperPayStack(app, "HelperPayTest", {
    env: { account: "123456789012", region: "us-east-1" },
    modelId: "global.anthropic.claude-sonnet-4-6", username: "jordan",
    uiDist: path.resolve(__dirname, "fixtures-ui"), backendDir: path.resolve(__dirname, "..", "..", "backend"),
  });
  return Template.fromStack(stack);
}

// 1. region guard
assert.throws(() => assertSupportedRegion("us-gov-west-1"), /AgentCore Memory/);
assert.doesNotThrow(() => assertSupportedRegion("us-east-1"));

// 2. the stack synthesizes with the expected pieces
const t = synth();
t.resourceCountIs("AWS::DynamoDB::Table", 2);
t.resourceCountIs("AWS::BedrockAgentCore::Memory", 1);
t.resourceCountIs("AWS::Cognito::UserPool", 1);
t.resourceCountIs("AWS::CloudFront::Distribution", 1);
t.hasResourceProperties("AWS::Lambda::Function", { Runtime: "provided.al2023", Architectures: ["arm64"], Timeout: 300 });
t.hasResourceProperties("AWS::Cognito::UserPool", { AdminCreateUserConfig: { AllowAdminCreateUserOnly: true } });
t.hasResourceProperties("AWS::ApiGateway::Method", { AuthorizationType: "COGNITO_USER_POOLS", HttpMethod: "ANY" });
t.hasResourceProperties("AWS::ApiGateway::GatewayResponse", { ResponseType: "DEFAULT_4XX" });
t.hasOutput("AppUrl", {});
t.hasOutput("PasswordCommand", {});

// 2b. no API method is open except OPTIONS
for (const [name, r] of Object.entries(t.toJSON().Resources) as [string, any][]) {
  if (r.Type === "AWS::ApiGateway::Method" && r.Properties.HttpMethod !== "OPTIONS") {
    assert.equal(r.Properties.AuthorizationType, "COGNITO_USER_POOLS", `${name} must require Cognito`);
  }
}

// 3. the agent role must not be able to write the Records table
const json = t.toJSON();
const policies = Object.entries(json.Resources).filter(([, r]: any) => r.Type === "AWS::IAM::Policy") as [string, any][];
const agentPolicies = policies.filter(([name]) => name.startsWith("Agent"));
assert.ok(agentPolicies.length > 0, "agent policy exists");
for (const [, p] of agentPolicies) {
  for (const st of p.Properties.PolicyDocument.Statement) {
    const actions: string[] = Array.isArray(st.Action) ? st.Action : [st.Action];
    const resources = JSON.stringify(st.Resource);
    if (resources.includes("Records") && actions.some(a => /dynamodb:(PutItem|UpdateItem|DeleteItem|BatchWriteItem)/.test(a))) {
      assert.fail("agent role can write the Records table: " + JSON.stringify(st));
    }
  }
}
// 4. every log group / table / bucket is deleted on destroy
for (const [name, r] of Object.entries(json.Resources) as [string, any][]) {
  if (["AWS::DynamoDB::Table", "AWS::Logs::LogGroup", "AWS::S3::Bucket", "AWS::Cognito::UserPool", "AWS::SecretsManager::Secret"].includes(r.Type)) {
    assert.equal(r.DeletionPolicy, "Delete", `${name} (${r.Type}) must have DeletionPolicy Delete`);
  }
}
console.log("stack tests passed");
