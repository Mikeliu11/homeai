/** Regions where Amazon Bedrock AgentCore Memory is available (checked 2026-10-09). */
export const MEMORY_REGIONS = [
  "us-east-1", "us-west-2", "eu-central-1", "ap-southeast-2",
  "us-east-2", "eu-west-1", "ap-south-1", "ap-southeast-1", "ap-northeast-1",
  "eu-west-3", "ap-northeast-2", "eu-north-1", "eu-west-2", "ca-central-1", "sa-east-1",
];

export function assertSupportedRegion(region: string | undefined) {
  if (!region || region.includes("${Token")) {
    throw new Error("No region. Set AWS_REGION or CDK_DEFAULT_REGION (e.g. us-east-1).");
  }
  if (!MEMORY_REGIONS.includes(region)) {
    throw new Error(`Region ${region} does not offer AgentCore Memory. Deploy to one of: ${MEMORY_REGIONS.join(", ")}`);
  }
}
