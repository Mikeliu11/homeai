import * as cdk from "aws-cdk-lib";
import * as cognito from "aws-cdk-lib/aws-cognito";
import * as iam from "aws-cdk-lib/aws-iam";
import * as lambda from "aws-cdk-lib/aws-lambda";
import * as logs from "aws-cdk-lib/aws-logs";
import * as secretsmanager from "aws-cdk-lib/aws-secretsmanager";
import * as cr from "aws-cdk-lib/custom-resources";
import { Construct } from "constructs";

/**
 * Creates the single login user with a generated permanent password stored
 * in Secrets Manager. Plain CloudFormation cannot set a Cognito password, so a
 * small custom-resource Lambda does: AdminCreateUser (no email) →
 * AdminSetUserPassword (permanent) → PutSecretValue.
 */
export class SeedUser extends Construct {
  readonly secret: secretsmanager.Secret;
  constructor(scope: Construct, id: string, props: { userPool: cognito.UserPool; username: string }) {
    super(scope, id);
    this.secret = new secretsmanager.Secret(this, "Password", {
      description: "helper-pay login password (generated at deploy)",
      removalPolicy: cdk.RemovalPolicy.DESTROY,
    });
    const fn = new lambda.Function(this, "Fn", {
      runtime: lambda.Runtime.NODEJS_22_X,
      handler: "index.handler",
      timeout: cdk.Duration.minutes(2),
      logGroup: new logs.LogGroup(this, "Logs", { retention: logs.RetentionDays.ONE_WEEK, removalPolicy: cdk.RemovalPolicy.DESTROY }),
      code: lambda.Code.fromInline(`
const { CognitoIdentityProviderClient, AdminCreateUserCommand, AdminSetUserPasswordCommand } = require("@aws-sdk/client-cognito-identity-provider");
const { SecretsManagerClient, PutSecretValueCommand } = require("@aws-sdk/client-secrets-manager");
const crypto = require("crypto");
function password() {
  const sets = ["ABCDEFGHJKLMNPQRSTUVWXYZ", "abcdefghijkmnopqrstuvwxyz", "23456789", "!@#$%^&*"];
  const all = sets.join("");
  const pick = (s) => s[crypto.randomInt(s.length)];
  let out = sets.map(pick);
  while (out.length < 16) out.push(pick(all));
  for (let i = out.length - 1; i > 0; i--) { const j = crypto.randomInt(i + 1); [out[i], out[j]] = [out[j], out[i]]; }
  return out.join("");
}
exports.handler = async (event) => {
  const { UserPoolId, Username, SecretId } = event.ResourceProperties;
  if (event.RequestType === "Delete") return { PhysicalResourceId: event.PhysicalResourceId };
  const cognito = new CognitoIdentityProviderClient({});
  const sm = new SecretsManagerClient({});
  const pw = password();
  if (event.RequestType === "Create") {
    try {
      await cognito.send(new AdminCreateUserCommand({ UserPoolId, Username, MessageAction: "SUPPRESS", UserAttributes: [{ Name: "email", Value: Username + "@example.invalid" }, { Name: "email_verified", Value: "true" }] }));
    } catch (e) { if (e.name !== "UsernameExistsException") throw e; }
  }
  await cognito.send(new AdminSetUserPasswordCommand({ UserPoolId, Username, Password: pw, Permanent: true }));
  await sm.send(new PutSecretValueCommand({ SecretId, SecretString: pw }));
  return { PhysicalResourceId: UserPoolId + "/" + Username };
};`),
    });
    fn.addToRolePolicy(new iam.PolicyStatement({ actions: ["cognito-idp:AdminCreateUser", "cognito-idp:AdminSetUserPassword"], resources: [props.userPool.userPoolArn] }));
    this.secret.grantWrite(fn);
    const provider = new cr.Provider(this, "Provider", {
      onEventHandler: fn,
      logGroup: new logs.LogGroup(this, "ProviderLogs", { retention: logs.RetentionDays.ONE_WEEK, removalPolicy: cdk.RemovalPolicy.DESTROY }),
    });
    new cdk.CustomResource(this, "Resource", {
      serviceToken: provider.serviceToken,
      properties: { UserPoolId: props.userPool.userPoolId, Username: props.username, SecretId: this.secret.secretArn, Version: "1" },
    });
  }
}
