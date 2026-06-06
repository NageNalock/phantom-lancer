export type MainTab = "dashboard" | "codex" | "logs" | "images" | "v2ray" | "settings";
export type CodexTab = "sessions" | "projects" | "gateway" | "activity";
export type Tone = "neutral" | "good" | "warn" | "danger";

export interface AuthSession {
  id: string;
  trusted?: boolean;
  expiresAt?: string;
}

export interface Workspace {
  id: string;
  name: string;
  rootPath: string;
  description?: string;
  tags?: string[];
  allowCodexWrite?: boolean;
  allowNonGit?: boolean;
  createdAt?: string;
  updatedAt?: string;
}

export interface CodexStatus {
  available?: boolean;
  appServerAvailable?: boolean;
  version?: string;
  error?: string;
  binaryPath?: string;
  codexHome?: string;
}

export interface CodexGatewayStatus {
  enabled?: boolean;
  publicApiKeys?: number;
  activeAccounts?: number;
  totalAccounts?: number;
  models?: number;
  recentRequestCount?: number;
  recentFailureCount?: number;
  lastError?: string;
}

export interface V2RayStatus {
  available?: boolean;
  enabled?: boolean;
  running?: boolean;
  stale?: boolean;
  state?: string;
  endpoint?: string;
  configPath?: string;
  coreVersion?: string;
  lastError?: string;
  remoteClientCount?: number;
  enabledRemoteClients?: number;
}

export interface ImageStatus {
  available?: boolean;
  provider?: string;
  hasApiKey?: boolean;
  maskedApiKey?: string;
  defaultModel?: string;
  historyCount?: number;
  lastJobStatus?: string;
  lastJobId?: string;
  lastError?: string;
  lastCompletedAt?: string;
}

export interface DashboardSummary {
  workspaces?: { total?: number; items?: Workspace[] };
  codex?: CodexStatus;
  codexGateway?: CodexGatewayStatus;
  images?: ImageStatus;
  v2ray?: V2RayStatus;
  pendingApprovals?: number;
  recentActivity?: AuditEvent[];
}

export interface AuditEvent {
  id?: string;
  eventType?: string;
  workspaceId?: string;
  riskLevel?: string;
  summary?: string;
  payload?: Record<string, unknown>;
  createdAt?: string;
}

export interface RuntimeSettings {
  allowedRoots: string[];
  codexBinary?: string;
  codexHome?: string;
  cookieSecure?: boolean;
  updatedAt?: string;
}

export interface FileSettings {
  configPath?: string;
  addr?: string;
  dataDir?: string;
  dbPath?: string;
  logFile?: string;
  logMaxSizeMB?: number;
  logMaxFiles?: number;
  logMaxAgeDays?: number;
}

export interface SettingsPayload {
  file?: FileSettings;
  runtime?: RuntimeSettings;
}

export interface BuildInfo {
  version?: string;
  commit?: string;
  date?: string;
  os?: string;
  arch?: string;
}

export interface SystemUpdateCheck {
  id?: string;
  currentVersion?: string;
  latestVersion?: string;
  updateAvailable?: boolean;
  comparable?: boolean;
  canApply?: boolean;
  reason?: string;
  releaseId?: string;
  releaseUrl?: string;
  publishedAt?: string;
  assetName?: string;
  assetSizeBytes?: number;
  checksumAvailable?: boolean;
  platformSupported?: boolean;
  errorMessage?: string;
  checkedAt?: string;
}

export interface SystemUpdateJob {
  id: string;
  currentVersion?: string;
  targetVersion?: string;
  releaseId?: string;
  assetName?: string;
  status?: string;
  phase?: string;
  bytesDownloaded?: number;
  totalBytes?: number;
  checksumSha256?: string;
  installBinaryPath?: string;
  backupBinaryPath?: string;
  errorMessage?: string;
  createdAt?: string;
  startedAt?: string;
  completedAt?: string;
}

export interface SystemUpdateStatus {
  enabled?: boolean;
  version?: BuildInfo;
  latestCheck?: SystemUpdateCheck;
  activeJob?: SystemUpdateJob;
  latestJob?: SystemUpdateJob;
  restartTimeoutSeconds?: number;
  supportedPlatform?: boolean;
}

export interface CodexGatewaySettings {
  id?: string;
  enabled?: boolean;
  baseUrl?: string;
  oauthAuthUrl?: string;
  oauthTokenUrl?: string;
  oauthClientId?: string;
  oauthRedirectUri?: string;
  requestTimeoutSeconds?: number;
  refreshMarginSeconds?: number;
  defaultInstructions?: string;
  installationId?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface CodexGatewayAPIKey {
  id: string;
  name?: string;
  status?: string;
  lastUsedAt?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface CodexGatewayAccount {
  id: string;
  label?: string;
  status?: string;
  hasAccessToken?: boolean;
  hasRefreshToken?: boolean;
  expiresAt?: string;
  plan?: string;
  lastError?: string;
  lastUsedAt?: string;
  checkedAt?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface CodexGatewayModel {
  id: string;
  displayName?: string;
  ownedBy?: string;
  source?: string;
  plans?: string[];
  lastSeenAt?: string;
  updatedAt?: string;
}

export interface CodexGatewayRequestLog {
  id?: string;
  requestId?: string;
  apiKind?: string;
  model?: string;
  accountId?: string;
  sourceIp?: string;
  statusCode?: number;
  errorCode?: string;
  errorSource?: string;
  errorMessage?: string;
  latencyMs?: number;
  streamed?: boolean;
  inputTokens?: number;
  outputTokens?: number;
  createdAt?: string;
}

export interface CodexGatewayPayload {
  status?: CodexGatewayStatus;
  settings?: CodexGatewaySettings;
  apiKeys?: CodexGatewayAPIKey[];
  accounts?: CodexGatewayAccount[];
  models?: CodexGatewayModel[];
  requestLogs?: CodexGatewayRequestLog[];
}

export interface LogSource {
  id: string;
  kind?: "file" | "event" | string;
  module?: string;
  name?: string;
  description?: string;
  path?: string;
  status?: string;
  managed?: boolean;
  sizeBytes?: number;
  updatedAt?: string;
  errorCount?: number;
  warningCount?: number;
  rotationSummary?: string;
}

export interface LogLine {
  sourceId: string;
  offset: number;
  time?: string;
  level?: "info" | "warn" | "error" | string;
  message: string;
  fields?: Record<string, unknown>;
  raw?: string;
  redacted?: boolean;
}

export interface LogTailPayload {
  source?: LogSource;
  lines?: LogLine[];
  limit?: number;
  maxBytes?: number;
  truncated?: boolean;
  cursor?: string;
}

export interface V2RaySettings {
  id?: string;
  enabled?: boolean;
  startOnPhantomLaunch?: boolean;
  assetDir?: string;
  configMode?: string;
  configFormat?: string;
  publicHost?: string;
  listen?: string;
  port?: number;
  protocol?: string;
  transport?: string;
  security?: string;
  wsPath?: string;
  tlsCertFile?: string;
  tlsKeyFile?: string;
  sniffingEnabled?: boolean;
  blockPrivateNetwork?: boolean;
  logLevel?: string;
  rawConfigJson?: string;
}

export interface V2RayRemoteClient {
  id: string;
  label?: string;
  email?: string;
  uuid?: string;
  level?: number;
  alterId?: number;
  enabled?: boolean;
  revokedAt?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface V2RayPayload {
  settings?: V2RaySettings;
  clients?: V2RayRemoteClient[];
  status?: V2RayStatus;
}

export interface ImageProviderSettings {
  id?: string;
  provider?: string;
  hasApiKey?: boolean;
  maskedApiKey?: string;
  defaultModel?: string;
  defaultResponseFormat?: string;
  defaultResolution?: string;
  defaultAspectRatio?: string;
  historyRetention?: number;
  createdAt?: string;
  updatedAt?: string;
}

export interface ImageStorageSettings {
  id?: string;
  backend?: string;
  s3ProviderLabel?: string;
  s3Bucket?: string;
  s3Region?: string;
  s3Endpoint?: string;
  s3Prefix?: string;
  s3ForcePathStyle?: boolean;
  hasS3Credentials?: boolean;
  maskedAccessKeyId?: string;
  s3AccessMode?: string;
  fallbackToLocal?: boolean;
  createdAt?: string;
  updatedAt?: string;
}

export interface ImageAsset {
  id: string;
  assetType?: string;
  status?: string;
  private?: boolean;
  provider?: string;
  model?: string;
  jobId?: string;
  sourceRole?: string;
  slot?: number;
  promptPreview?: string;
  revisedPromptPreview?: string;
  originalFilename?: string;
  originalSourceRedacted?: string;
  mimeType?: string;
  extension?: string;
  sizeBytes?: number;
  width?: number;
  height?: number;
  checksumSha256?: string;
  localName?: string;
  url?: string;
  downloadUrl?: string;
  storageBackend?: string;
  s3Bucket?: string;
  s3Region?: string;
  s3EndpointLabel?: string;
  s3Key?: string;
  s3Etag?: string;
  privateAt?: string;
  archivedAt?: string;
  deletedAt?: string;
  deletedReason?: string;
  lastError?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface ImageGenerationSource {
  id?: string;
  jobId?: string;
  assetId?: string;
  slot?: number;
  sourceType?: string;
  sourceLabel?: string;
  mimeType?: string;
  sizeBytes?: number;
  urlRedacted?: string;
  createdAt?: string;
}

export interface ImageGenerationOutput {
  id?: string;
  jobId?: string;
  assetId?: string;
  slot?: number;
  remoteUrl?: string;
  localName?: string;
  url?: string;
  mimeType?: string;
  revisedPrompt?: string;
  storage?: string;
  sizeBytes?: number;
  createdAt?: string;
}

export interface ImageGenerationJob {
  id: string;
  provider?: string;
  status?: string;
  mode?: string;
  modeLabel?: string;
  model?: string;
  endpoint?: string;
  prompt?: string;
  aspectRatio?: string;
  resolution?: string;
  responseFormat?: string;
  imageCount?: number;
  sourceCount?: number;
  usage?: Record<string, unknown>;
  errorMessage?: string;
  createdAt?: string;
  startedAt?: string;
  completedAt?: string;
  sources?: ImageGenerationSource[];
  outputs?: ImageGenerationOutput[];
}

export interface ImagesPayload {
  settings?: ImageProviderSettings;
  storageSettings?: ImageStorageSettings;
  status?: ImageStatus;
  jobs?: ImageGenerationJob[];
  assets?: ImageAsset[];
  count?: number;
}

export interface V2RayExport {
  clientId?: string;
  label?: string;
  endpoint?: string;
  shareUri?: string;
  clientConfig?: unknown;
}

export interface CodexSession {
  id: string;
  workspaceId: string;
  title: string;
  sandbox: "read-only" | "workspace-write" | string;
  status?: string;
  archived?: boolean;
  codexThreadId?: string;
  lastTurnId?: string;
  lastPrompt?: string;
  promptPreview?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface CodexTurn {
  id?: string;
  sessionId?: string;
  codexTurnId?: string;
  promptPreview?: string;
  status?: string;
  createdAt?: string;
  completedAt?: string;
}

export interface CodexSessionDetail {
  session: CodexSession;
  workspace?: Workspace | null;
  turns?: CodexTurn[];
}

export interface EventRecord {
  id?: string;
  scope?: string;
  scopeId?: string;
  sequence?: number;
  type: string;
  payload?: Record<string, unknown>;
  createdAt?: string;
}

export interface AppData {
  dashboard: DashboardSummary;
  workspaces: Workspace[];
  audit: AuditEvent[];
  pendingApprovals: unknown[];
  codexStatus: CodexStatus;
  codexSessions: CodexSession[];
  codexGateway: CodexGatewayPayload;
  settings: SettingsPayload;
  v2ray: V2RayPayload;
  images: ImagesPayload;
}

export interface ApiError extends Error {
  code?: string;
  status?: number;
  payload?: unknown;
}
