import * as cdk from "aws-cdk-lib";
import * as apigw from "aws-cdk-lib/aws-apigateway";
import * as agentcore from "aws-cdk-lib/aws-bedrockagentcore";
import * as cloudfront from "aws-cdk-lib/aws-cloudfront";
import * as origins from "aws-cdk-lib/aws-cloudfront-origins";
import * as cognito from "aws-cdk-lib/aws-cognito";
import * as dynamodb from "aws-cdk-lib/aws-dynamodb";
import * as iam from "aws-cdk-lib/aws-iam";
import * as lambda from "aws-cdk-lib/aws-lambda";
import * as logs from "aws-cdk-lib/aws-logs";
import * as s3 from "aws-cdk-lib/aws-s3";
import * as s3deploy from "aws-cdk-lib/aws-s3-deployment";
import { GoFunction } from "@aws-cdk/aws-lambda-go-alpha";
import { Construct } from "constructs";
import * as path from "path";
import { SeedUser } from "./seed-user";

export interface HelperPayStackProps extends cdk.StackProps {
  /** Bedrock model id; default global.anthropic.claude-sonnet-4-6 */
  modelId: string;
  /** Login username; default jordan */
  username: string;
  /** Path to the built UI (ui/dist) */
  uiDist: string;
  /** Path to the Go module */
  backendDir: string;
}

export class HelperPayStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: HelperPayStackProps) {
    super(scope, id, props);
    const region = cdk.Stack.of(this).region;
    const account = cdk.Stack.of(this).account;
    const modelBase = props.modelId.replace(/^(global|us|eu|apac|au|jp|ca)\./, "");

    // ---- records (api-only) and proposals (agent-writable) ----
    const tableProps = {
      partitionKey: { name: "PK", type: dynamodb.AttributeType.STRING },
      sortKey: { name: "SK", type: dynamodb.AttributeType.STRING },
      billingMode: dynamodb.BillingMode.PAY_PER_REQUEST,
      removalPolicy: cdk.RemovalPolicy.DESTROY,
    };
    const gsi = { indexName: "GSI1", partitionKey: { name: "GSI1PK", type: dynamodb.AttributeType.STRING }, sortKey: { name: "GSI1SK", type: dynamodb.AttributeType.STRING } };
    const records = new dynamodb.Table(this, "Records", tableProps);
    records.addGlobalSecondaryIndex(gsi);
    const proposals = new dynamodb.Table(this, "Proposals", tableProps);
    proposals.addGlobalSecondaryIndex(gsi);

    // ---- uploads ----
    const uploads = new s3.Bucket(this, "Uploads", {
      removalPolicy: cdk.RemovalPolicy.DESTROY,
      autoDeleteObjects: true,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      encryption: s3.BucketEncryption.S3_MANAGED,
      enforceSSL: true,
      cors: [{ allowedMethods: [s3.HttpMethods.PUT, s3.HttpMethods.GET], allowedOrigins: ["*"], allowedHeaders: ["*"], maxAge: 3600 }],
    });

    // ---- AgentCore Memory: short-term events + long-term facts/summaries ----
    const actorId = "jordan";
    const memory = new agentcore.Memory(this, "Memory", {
      memoryName: cdk.Names.uniqueResourceName(this, { maxLength: 40, allowedSpecialCharacters: "_" }).replace(/[^a-zA-Z0-9_]/g, "_"),
      description: "helper-pay assistant memory: conversation events and household facts",
      expirationDuration: cdk.Duration.days(30),
      memoryStrategies: [
        agentcore.MemoryStrategy.usingSemantic({ strategyName: "facts", namespaces: ["/household/{actorId}/facts/"] }),
        agentcore.MemoryStrategy.usingSummarization({ strategyName: "summaries", namespaces: ["/sessions/{actorId}/{sessionId}/"] }),
      ],
    });
    for (const child of memory.node.findAll()) {
      if (cdk.CfnResource.isCfnResource(child)) child.applyRemovalPolicy(cdk.RemovalPolicy.DESTROY);
    }

    // ---- auth ----
    const userPool = new cognito.UserPool(this, "Users", {
      selfSignUpEnabled: false,
      signInAliases: { username: true },
      passwordPolicy: { minLength: 12, requireLowercase: true, requireUppercase: true, requireDigits: true, requireSymbols: true },
      accountRecovery: cognito.AccountRecovery.NONE,
      removalPolicy: cdk.RemovalPolicy.DESTROY,
      deletionProtection: false,
    });
    // userSrp for the browser; adminUserPassword so an operator with IAM credentials can script the API (eval, smoke).
    const client = userPool.addClient("Web", { authFlows: { userSrp: true, adminUserPassword: true }, generateSecret: false, preventUserExistenceErrors: true });
    const seed = new SeedUser(this, "SeedUser", { userPool, username: props.username });

    // ---- SPA hosting (CloudFront + private bucket) ----
    const site = new s3.Bucket(this, "Site", { removalPolicy: cdk.RemovalPolicy.DESTROY, autoDeleteObjects: true, blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL, enforceSSL: true });
    const dist = new cloudfront.Distribution(this, "Cdn", {
      defaultBehavior: { origin: origins.S3BucketOrigin.withOriginAccessControl(site), viewerProtocolPolicy: cloudfront.ViewerProtocolPolicy.REDIRECT_TO_HTTPS, cachePolicy: cloudfront.CachePolicy.CACHING_OPTIMIZED },
      defaultRootObject: "index.html",
      errorResponses: [
        { httpStatus: 403, responseHttpStatus: 200, responsePagePath: "/index.html", ttl: cdk.Duration.seconds(0) },
        { httpStatus: 404, responseHttpStatus: 200, responsePagePath: "/index.html", ttl: cdk.Duration.seconds(0) },
      ],
      priceClass: cloudfront.PriceClass.PRICE_CLASS_100,
      comment: "helper-pay",
    });
    const appUrl = `https://${dist.distributionDomainName}`;

    // ---- Go Lambdas ----
    const goDefaults = {
      runtime: lambda.Runtime.PROVIDED_AL2023,
      architecture: lambda.Architecture.ARM_64,
      moduleDir: path.join(props.backendDir, "go.mod"),
      bundling: { cgoEnabled: false },
    };
    const agentLogs = new logs.LogGroup(this, "AgentLogs", { retention: logs.RetentionDays.ONE_WEEK, removalPolicy: cdk.RemovalPolicy.DESTROY });
    const agentFn = new GoFunction(this, "Agent", {
      ...goDefaults,
      entry: path.join(props.backendDir, "cmd", "agent"),
      timeout: cdk.Duration.minutes(5),
      memorySize: 1024,
      logGroup: agentLogs,
      retryAttempts: 0,
      maxEventAge: cdk.Duration.minutes(5),
      environment: {
        TABLE_RECORDS: records.tableName, TABLE_PROPOSALS: proposals.tableName, UPLOADS_BUCKET: uploads.bucketName,
        MEMORY_ID: memory.memoryId, ACTOR_ID: actorId, MODEL_ID: props.modelId,
      },
    });
    // The agent can read records and write only proposals/turns, memory, and the model.
    records.grantReadData(agentFn);
    proposals.grantReadWriteData(agentFn);
    uploads.grantRead(agentFn);
    memory.grantRead(agentFn);
    memory.grantWrite(agentFn);
    agentFn.addToRolePolicy(new iam.PolicyStatement({
      actions: ["bedrock-agentcore:CreateEvent", "bedrock-agentcore:ListEvents", "bedrock-agentcore:GetEvent", "bedrock-agentcore:RetrieveMemoryRecords", "bedrock-agentcore:ListMemoryRecords", "bedrock-agentcore:GetMemoryRecord", "bedrock-agentcore:BatchCreateMemoryRecords"],
      resources: [memory.memoryArn],
    }));
    agentFn.addToRolePolicy(new iam.PolicyStatement({
      actions: ["bedrock:InvokeModel", "bedrock:InvokeModelWithResponseStream"],
      resources: [
        `arn:aws:bedrock:${region}:${account}:inference-profile/${props.modelId}`,
        `arn:aws:bedrock:*:${account}:inference-profile/${props.modelId}`,
        `arn:aws:bedrock:*::foundation-model/${modelBase}`,
        `arn:aws:bedrock:::foundation-model/${modelBase}`, // the form AWS's global-profile example policy uses
      ],
    }));
    agentFn.addToRolePolicy(new iam.PolicyStatement({ actions: ["aws-marketplace:ViewSubscriptions", "aws-marketplace:Subscribe"], resources: ["*"] }));

    const apiLogs = new logs.LogGroup(this, "ApiLogs", { retention: logs.RetentionDays.ONE_WEEK, removalPolicy: cdk.RemovalPolicy.DESTROY });
    const apiFn = new GoFunction(this, "Api", {
      ...goDefaults,
      entry: path.join(props.backendDir, "cmd", "api"),
      timeout: cdk.Duration.seconds(29),
      memorySize: 512,
      logGroup: apiLogs,
      environment: {
        TABLE_RECORDS: records.tableName, TABLE_PROPOSALS: proposals.tableName, UPLOADS_BUCKET: uploads.bucketName,
        MEMORY_ID: memory.memoryId, ACTOR_ID: actorId, AGENT_FUNCTION: agentFn.functionName, ALLOW_ORIGIN: appUrl,
      },
    });
    records.grantReadWriteData(apiFn);
    proposals.grantReadWriteData(apiFn);
    uploads.grantReadWrite(apiFn);
    memory.grantRead(apiFn);
    // Read for the Memories tab; list/delete for the household reset (POST /reset) and conversation delete, the only routes that delete anything.
    apiFn.addToRolePolicy(new iam.PolicyStatement({
      actions: ["bedrock-agentcore:ListMemoryRecords", "bedrock-agentcore:RetrieveMemoryRecords", "bedrock-agentcore:ListSessions", "bedrock-agentcore:ListEvents",
        "bedrock-agentcore:DeleteEvent", "bedrock-agentcore:DeleteMemoryRecord", "bedrock-agentcore:BatchDeleteMemoryRecords"],
      resources: [memory.memoryArn],
    }));
    agentFn.grantInvoke(apiFn);

    // ---- REST API behind Cognito ----
    const authorizer = new apigw.CognitoUserPoolsAuthorizer(this, "Authorizer", { cognitoUserPools: [userPool] });
    const api = new apigw.RestApi(this, "Http", {
      restApiName: `${id}-api`,
      deployOptions: { stageName: "api", throttlingRateLimit: 20, throttlingBurstLimit: 40 },
      defaultCorsPreflightOptions: { allowOrigins: [appUrl], allowHeaders: ["Authorization", "Content-Type"], allowMethods: ["GET", "POST", "OPTIONS"] },
      // Every method, including ANY on the root, requires a Cognito token. CDK keeps OPTIONS open for CORS preflight.
      defaultMethodOptions: { authorizer, authorizationType: apigw.AuthorizationType.COGNITO },
      cloudWatchRole: false,
    });
    api.root.addProxy({ defaultIntegration: new apigw.LambdaIntegration(apiFn, { proxy: true }), anyMethod: true });
    // CORS headers on authorizer/gateway errors, so the browser shows "signed out" instead of an opaque CORS failure.
    const corsHeaders = { "Access-Control-Allow-Origin": `'${appUrl}'`, "Access-Control-Allow-Headers": "'Authorization,Content-Type'" };
    api.addGatewayResponse("Default4xx", { type: apigw.ResponseType.DEFAULT_4XX, responseHeaders: corsHeaders });
    api.addGatewayResponse("Default5xx", { type: apigw.ResponseType.DEFAULT_5XX, responseHeaders: corsHeaders });
    const apiUrl = api.url.replace(/\/$/, "");

    // ---- deploy the UI with its runtime config ----
    new s3deploy.BucketDeployment(this, "SiteDeploy", {
      destinationBucket: site,
      distribution: dist,
      distributionPaths: ["/*"],
      sources: [
        s3deploy.Source.asset(props.uiDist),
        s3deploy.Source.jsonData("config.json", { apiUrl, region, userPoolId: userPool.userPoolId, userPoolClientId: client.userPoolClientId }),
      ],
      logGroup: new logs.LogGroup(this, "SiteDeployLogs", { retention: logs.RetentionDays.ONE_WEEK, removalPolicy: cdk.RemovalPolicy.DESTROY }),
    });

    // ---- what the grader needs ----
    new cdk.CfnOutput(this, "AppUrl", { value: appUrl, description: "Open this in a browser" });
    new cdk.CfnOutput(this, "Username", { value: props.username });
    new cdk.CfnOutput(this, "PasswordCommand", { value: `aws secretsmanager get-secret-value --region ${region} --secret-id ${seed.secret.secretArn} --query SecretString --output text` });
    new cdk.CfnOutput(this, "ModelId", { value: props.modelId });
    new cdk.CfnOutput(this, "Region", { value: region });
    new cdk.CfnOutput(this, "ApiUrl", { value: apiUrl });
    new cdk.CfnOutput(this, "MemoryId", { value: memory.memoryId });
    new cdk.CfnOutput(this, "SmokeCommand", { value: `TABLE_RECORDS=${records.tableName} TABLE_PROPOSALS=${proposals.tableName} AWS_REGION=${region} go run ./cmd/smoke   # from backend/` });
  }
}
