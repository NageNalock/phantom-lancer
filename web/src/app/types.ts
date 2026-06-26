export type MainTab = "dashboard" | "codex-gateway" | "codex" | "logs" | "images" | "docker" | "stockv2" | "v2ray" | "settings";
export type Tone = "neutral" | "good" | "warn" | "danger";

export interface AuthSession {
  id: string;
  trusted?: boolean;
  expiresAt?: string;
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

interface DashboardSummary {
  codexGateway?: CodexGatewayStatus;
  codex?: CodexStatus;
  images?: ImageStatus;
  v2ray?: V2RayStatus;
  recentActivity?: AuditEvent[];
  dbStats?: DatabaseStats;
}

export interface DatabaseTableStat {
  name: string;
  sizeBytes: number;
  indexSizeBytes?: number;
  pageCount?: number;
  description?: string;
}

export interface DatabaseStats {
  totalBytes: number;
  tables: DatabaseTableStat[];
  updatedAt: string;
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
  cookieSecure?: boolean;
  addr?: string;
  tlsEnabled?: boolean;
  tlsCertFile?: string;
  tlsKeyFile?: string;
  tlsOwnerUidCheck?: boolean;
  hstsEnabled?: boolean;
  hstsMaxAgeSeconds?: number;
  updatedAt?: string;
}

export interface ListenerEndpoint {
  addr: string;
  tlsEnabled: boolean;
  scheme: "http" | "https";
  certFile?: string;
  certDnsNames?: string[];
  certNotBefore?: string;
  certNotAfter?: string;
  certReloadErr?: string;
  hstsEnabled?: boolean;
  hstsMaxAgeSeconds?: number;
}

export interface TLSProbeResult {
  ok: boolean;
  error?: string;
  subject?: string;
  issuer?: string;
  dnsNames?: string[];
  serialNumber?: string;
  notBefore?: string;
  notAfter?: string;
  sigAlg?: string;
  pubKeyBits?: number;
  fileOwnerUid?: number;
  fileOwnerName?: string;
  fileWritableByOthers?: boolean;
  fileHasSymlink?: boolean;
  daysRemaining?: number;
}

interface FileSettings {
  configPath?: string;
  addr?: string;
  dataDir?: string;
  dbPath?: string;
  dbSizeBytes?: number;
  logFile?: string;
  logMaxSizeMB?: number;
  logMaxFiles?: number;
  logMaxAgeDays?: number;
  tlsBootStrict?: boolean;
  hstsDefaultsApplied?: boolean;
}

export interface LocalDatabaseFileStat {
  kind?: string;
  label?: string;
  path?: string;
  exists?: boolean;
  sizeBytes?: number;
  updatedAt?: string;
}

export interface LocalStorageStats {
  sqlite?: LocalDatabaseFileStat;
  duckdb?: LocalDatabaseFileStat[];
}

export interface SystemSettings {
  eventRetentionDays: number;
}

export interface SettingsPayload {
  file?: FileSettings;
  storage?: LocalStorageStats;
  runtime?: RuntimeSettings;
  listener?: ListenerEndpoint;
  system?: SystemSettings;
}

interface BuildInfo {
  version?: string;
  commit?: string;
  date?: string;
  os?: string;
  arch?: string;
}

interface SystemUpdateCheck {
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

export interface SupervisorStatus {
  underSupervisor?: boolean;
  alive?: boolean;
  pid?: number;
  pidSource?: "env" | "pidfile" | string;
  childPID?: number;
  lastError?: string;
}

export interface SystemUpdateStatus {
  enabled?: boolean;
  version?: BuildInfo;
  latestCheck?: SystemUpdateCheck;
  activeJob?: SystemUpdateJob;
  latestJob?: SystemUpdateJob;
  restartTimeoutSeconds?: number;
  supportedPlatform?: boolean;
  restartMode?: string;
  installBinaryPath?: string;
  backupBinaryPath?: string;
  dbSizeBytes?: number;
  supervisor?: SupervisorStatus;
  /** @deprecated Use `supervisor?.underSupervisor` instead. */
  underSupervisor?: boolean;
  /** @deprecated Use `supervisor?.pid` instead. */
  supervisorPID?: number;
}

interface SystemStatusPayload {
  version?: BuildInfo;
  supervisor?: SupervisorStatus;
  dataDir?: string;
  startedAt?: string;
  uptimeMs?: number;
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
  accountHealthCheckIntervalSeconds?: number;
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

interface CodexGatewayModel {
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
  objectStorageProfileId?: string;
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

export interface ObjectStorageProfile {
  id: string;
  name: string;
  providerLabel: string;
  bucket: string;
  region: string;
  endpoint: string;
  forcePathStyle: boolean;
  hasCredentials: boolean;
  maskedAccessKeyId: string;
  status: string;
  lastTestedAt?: string;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
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

export interface ImagePrompt {
  id: string;
  title: string;
  description?: string;
  prompt: string;
  mode: string;
  model?: string;
  aspectRatio?: string;
  resolution?: string;
  imageCount?: number;
  tags?: string[];
  status?: string;
  useCount?: number;
  lastUsedAt?: string;
  deletedAt?: string;
  createdAt?: string;
  updatedAt?: string;
}

interface ImageGenerationSource {
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

interface ImageGenerationOutput {
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
  prompts?: ImagePrompt[];
  count?: number;
}

export interface V2RayExport {
  clientId?: string;
  label?: string;
  endpoint?: string;
  shareUri?: string;
  clientConfig?: unknown;
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

interface CodexInstallation {
  id?: string;
  binaryPath?: string;
  version?: string;
  status?: string;
  capabilities?: Record<string, unknown>;
  doctorSummary?: Record<string, unknown>;
  lastProbeError?: string;
  detectedAt?: string;
}

export interface CodexAppServerStatus {
  state?: string;
  pid?: number;
  startedAt?: string;
  uptimeSeconds?: number;
  lastProbeAt?: string;
  lastError?: string;
  enabled?: boolean;
}

export interface CodexStatus {
  enabled?: boolean;
  installation?: CodexInstallation;
  appServer?: CodexAppServerStatus;
  workspaceCount?: number;
  threadCount?: number;
  pendingApprovals?: number;
  runtime?: {
    running?: number;
    waitingApproval?: number;
    queued?: number;
    failed?: number;
  };
  legacyTables?: string[];
}

export interface CodexSettings {
  enabled?: boolean;
  binaryPath?: string;
  codexHome?: string;
  defaultModel?: string;
  defaultSandbox?: string;
  defaultApprovalPolicy?: string;
  appServerEnabled?: boolean;
  appServerProbeIntervalSeconds?: number;
  appServerStartOnLaunch?: boolean;
  execFallbackEnabled?: boolean;
  eventRetentionDays?: number;
  maxEventsPerThread?: number;
  maxEventPayloadBytes?: number;
  maxConcurrentTurns?: number;
  scratchWorkspaceId?: string;
}

export interface CodexMemoryDiagnostics {
  codexHomeSummary?: string;
  configPresent?: boolean;
  globalAgentsMd?: boolean;
  sessionsPresent?: boolean;
  scratchAgentsMd?: boolean;
  scratchConfigured?: boolean;
  note?: string;
}

export interface CodexWorkspace {
  id: string;
  label?: string;
  pathSummary?: string;
  trustState?: string;
  defaultModel?: string;
  defaultSandbox?: string;
  defaultApprovalPolicy?: string;
  networkPolicy?: Record<string, unknown>;
  pinned?: boolean;
  lastOpenedAt?: string;
  gitBranch?: string;
  gitState?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface CodexThread {
  id: string;
  codexThreadId?: string;
  workspaceId?: string;
  title?: string;
  status?: string;
  sourceMode?: string;
  kind?: string;
  background?: boolean;
  backgroundSource?: string;
  executionMode?: string;
  worktreeSummary?: string;
  baseBranch?: string;
  branchName?: string;
  worktreeStatus?: string;
  mergeStatus?: string;
  discardedAt?: string;
  model?: string;
  sandboxMode?: string;
  approvalPolicy?: string;
  pinned?: boolean;
  archivedAt?: string;
  lastTurnId?: string;
  lastError?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface CodexWorktreeStatus {
  threadId?: string;
  executionMode?: string;
  worktreeSummary?: string;
  baseBranch?: string;
  branchName?: string;
  worktreeStatus?: string;
  mergeStatus?: string;
  dirtyStatus?: string;
  discardedAt?: string;
  lastError?: string;
}

export interface CodexTurn {
  id: string;
  threadId?: string;
  codexTurnId?: string;
  status?: string;
  promptSummary?: string;
  model?: string;
  sandboxMode?: string;
  approvalPolicy?: string;
  startedAt?: string;
  completedAt?: string;
  errorSummary?: string;
  createdAt?: string;
}

export interface CodexEvent {
  id?: string;
  threadId?: string;
  turnId?: string;
  sequence?: number;
  eventType?: string;
  codexMethod?: string;
  itemType?: string;
  textPreview?: string;
  payload?: Record<string, unknown>;
  createdAt?: string;
}

export interface CodexApproval {
  id: string;
  threadId?: string;
  turnId?: string;
  status?: string;
  actionKind?: string;
  commandPreview?: string;
  cwdSummary?: string;
  riskLevel?: string;
  decision?: string;
  decidedAt?: string;
  expiresAt?: string;
  createdAt?: string;
}

export interface CodexAttachment {
  id: string;
  filename?: string;
  contentType?: string;
  sizeBytes?: number;
}

export interface CodexModel {
  id: string;
  displayName?: string;
  isDefault?: boolean;
}

export interface CodexReviewComment {
  id: string;
  threadId?: string;
  turnId?: string;
  workspaceId?: string;
  filePath?: string;
  oldLine?: number;
  newLine?: number;
  hunkHeader?: string;
  body?: string;
  status?: string;
  createdAt?: string;
  resolvedAt?: string;
}

export interface CodexReviewSnapshot {
  scope?: string;
  summary?: string;
  diff?: string;
  truncated?: boolean;
  comments?: CodexReviewComment[];
  generatedAt?: string;
}

export interface CodexCommand {
  id: string;
  threadId?: string;
  commandPreview?: string;
  cwdSummary?: string;
  status?: string;
  exitCode?: number;
  outputPreview?: string;
  errorSummary?: string;
  startedAt?: string;
  completedAt?: string;
  createdAt?: string;
}

export interface CodexCommandAssessment {
  class?: string;
  riskSummary?: string;
  requiresConfirmation?: boolean;
  sandboxSummary?: string;
  cwdSummary?: string;
  commandPreview?: string;
}

export interface CodexBrowserSession {
  id: string;
  threadId?: string;
  url?: string;
  status?: string;
  lastError?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface CodexAutomation {
  id: string;
  kind?: string;
  threadId?: string;
  workspaceId?: string;
  title?: string;
  promptSummary?: string;
  schedule?: Record<string, unknown>;
  enabled?: boolean;
  defaultSandbox?: string;
  defaultApprovalPolicy?: string;
  lastRunAt?: string;
  nextRunAt?: string;
  retryCount?: number;
  failureBackoffUntil?: string;
  createdAt?: string;
  updatedAt?: string;
}

interface CodexAutomationRun {
  id: string;
  automationId?: string;
  threadId?: string;
  turnId?: string;
  clientRequestId?: string;
  status?: string;
  startedAt?: string;
  lastHeartbeatAt?: string;
  findingSummary?: string;
  errorSummary?: string;
  triageState?: string;
  createdAt?: string;
  completedAt?: string;
}

interface CodexTriageFailedTurn {
  turnId: string;
  threadId: string;
  errorSummary?: string;
  completedAt?: string;
}

export interface CodexTriageInbox {
  automationRuns?: CodexAutomationRun[];
  backgroundThreads?: CodexThread[];
  failedTurns?: CodexTriageFailedTurn[];
  reviewComments?: CodexReviewComment[];
}

export interface CodexCapabilitySummary {
  kind?: string;
  status?: string;
  items?: Array<Record<string, unknown>>;
  lastError?: string;
  probedAt?: string;
}

export interface CodexNotification {
  id: string;
  scope?: string;
  scopeId?: string;
  eventType?: string;
  title?: string;
  summary?: string;
  status?: string;
  severity?: string;
  payload?: Record<string, unknown>;
  createdAt?: string;
}

export interface AppData {
  dashboard: DashboardSummary;
  audit: AuditEvent[];
  codexGateway: CodexGatewayPayload;
  settings: SettingsPayload;
  v2ray: V2RayPayload;
  images: ImagesPayload;
  stockv2: StockV2Payload;
}

export interface ApiError extends Error {
  code?: string;
  status?: number;
  payload?: unknown;
}

export interface DockerStatus {
  state: string;
  available: boolean;
  serverVersion?: string;
  apiVersion?: string;
  os?: string;
  architecture?: string;
  storageDriver?: string;
  rootless: boolean;
  containers: number;
  containersRunning: number;
  images: number;
  lastError?: string;
  lastCheckedAt: string;
}

export interface DockerSettings {
  installEnabled?: boolean;
  daemonControlEnabled?: boolean;
  containerCreateEnabled?: boolean;
  pullConcurrency?: number;
  daemonPullConcurrency?: number;
  updatedAt?: string;
}

export interface DockerJob {
  id: string;
  type: string;
  title: string;
  status: string;
  riskLevel?: string;
  target?: string;
  error?: string;
  eventScope: string;
  eventScopeId: string;
  createdAt?: string;
  startedAt?: string;
  completedAt?: string;
  payload?: Record<string, unknown>;
}

interface DockerInstallStatus {
  supported?: boolean;
  installed?: boolean;
  canInstall?: boolean;
  distroId?: string;
  distroName?: string;
  family?: string;
  reason?: string;
  commandPreview?: string[];
  dockerVersion?: string;
  installEnabled?: boolean;
  privilegeMethod?: string;
}

interface DockerSystemdStatus {
  available?: boolean;
  canControl?: boolean;
  activeState?: string;
  reason?: string;
  controlEnabled?: boolean;
  privilegeMethod?: string;
}

export interface DockerControlStatus {
  settings?: DockerSettings;
  install?: DockerInstallStatus;
  systemd?: DockerSystemdStatus;
  privilegeMethod?: string;
  activeJob?: DockerJob;
  latestJob?: DockerJob;
  daemonPullConcurrency?: number;
}

export interface DockerRegistrySettings {
  enabled?: boolean;
  publicUrl?: string;
  storageBackend?: string;
  objectStorageProfileId?: string;
  objectPrefix?: string;
  storageDir?: string;
  quotaBytes?: number;
  requireTls?: boolean;
  allowAnonymousPull?: boolean;
  allowInsecureLocal?: boolean;
  maxRepositories?: number;
  maxTagsPerRepository?: number;
}

export interface DockerRegistryStatus {
  enabled?: boolean;
  ready?: boolean;
  publicUrl?: string;
  storageBackend?: string;
  storageDir?: string;
  objectPrefix?: string;
  quotaBytes?: number;
  usageBytes?: number;
  maxRepositories?: number;
  maxTagsPerRepository?: number;
  repositoryCount?: number;
  credentialCount?: number;
  requireTls?: boolean;
  allowAnonymousPull?: boolean;
  lastError?: string;
}

export interface DockerRegistryCredential {
  id: string;
  name: string;
  status: string;
  scopes?: string[];
  repositoryPrefix?: string;
  hasStoredSecret?: boolean;
  lastUsedAt?: string;
  createdAt?: string;
  rotatedAt?: string;
  revokedAt?: string;
}

export interface DockerRegistryRepository {
  id: string;
  name: string;
  sizeBytes: number;
  tagCount: number;
  lastPushedAt?: string;
  lastPulledAt?: string;
  createdAt?: string;
  updatedAt?: string;
}

interface DockerRegistryManifest {
  digest: string;
  repository: string;
  mediaType?: string;
  sizeBytes?: number;
  configSizeBytes?: number;
  layerCount?: number;
  pushedBy?: string;
  pushedAt?: string;
  deletedAt?: string;
}

export interface DockerRegistryTag {
  repository: string;
  tag: string;
  digest: string;
  createdAt?: string;
  updatedAt?: string;
  deletedAt?: string;
  manifest?: DockerRegistryManifest;
}

export interface DockerContainerSummary {
  id: string;
  names: string[];
  image: string;
  state: string;
  status: string;
  created: number;
  ports?: string[];
}

export interface DockerContainerPortSummary {
  privatePort: string;
  public?: string;
}

export interface DockerContainerMountSummary {
  type: string;
  name?: string;
  source?: string;
  destination: string;
  mode?: string;
  rw: boolean;
}

export interface DockerContainerNetworkSummary {
  name: string;
  ipAddress?: string;
}

export interface DockerContainerLabelSummary {
  key: string;
  value: string;
}

export interface DockerContainerInspectSummary {
  id: string;
  name: string;
  image: string;
  created?: string;
  state?: string;
  status?: string;
  running: boolean;
  restarting: boolean;
  exitCode: number;
  startedAt?: string;
  finishedAt?: string;
  ports?: DockerContainerPortSummary[];
  mounts?: DockerContainerMountSummary[];
  networks?: DockerContainerNetworkSummary[];
  labels?: DockerContainerLabelSummary[];
  restartCount: number;
}

export interface DockerImageSummary {
  id: string;
  tags?: string[];
  created: number;
  sizeBytes: number;
  usedBy?: string[];
}

export interface DockerVolumeSummary {
  name: string;
  driver: string;
  mountpoint?: string;
  createdAt?: string;
}

export interface DockerNetworkSummary {
  id: string;
  name: string;
  driver: string;
  scope: string;
  usedBy?: string[];
}

export interface DockerLogLine {
  stream: string;
  text: string;
}

export interface DockerStats {
  cpuPercent: number;
  memoryUsageBytes: number;
  memoryLimitBytes: number;
  memoryPercent: number;
}

// StockV2

export interface StockV2Payload {
  portfolios?: StockV2PortfolioWithHoldings[];
  instruments?: StockV2Instrument[];
  updateJobs?: StockV2UpdateJob[];
  settings?: StockV2Settings;
  lastUpdate?: string;
}

export interface StockV2Instrument {
  id: string;
  symbol: string;
  market: string;
  instrumentType?: string;
  name: string;
  industry: string;
  sector: string;
  concepts: string[];
  listDate: string;
  delistDate: string;
  status: string; // active, delisted, suspended
  lastUpdate: string;
  createdAt: string;
  updatedAt: string;
  profileSummary?: StockV2StockProfileSummary;
}

export interface StockV2StockProfileSummary {
  symbol: string;
  status: string;
  businessSummary?: string;
  aiProfileStatus?: string;
  aiProfileModel?: string;
  aiProfileConfidence?: number;
  aiProfileUpdatedAt?: string;
  updatedAt?: string;
}

export interface StockV2StockProfile extends StockV2StockProfileSummary {
  market: string;
  instrumentType: string;
  name: string;
  aliases?: string[];
  aliasesZh?: string[];
  aliasesEn?: string[];
  industry?: string;
  sectors?: string[];
  concepts?: string[];
  tags?: string[];
  keywordsZh?: string[];
  keywordsEn?: string[];
  businessSummaryZh?: string;
  businessSummaryEn?: string;
  businessLinesZh?: string[];
  businessLinesEn?: string[];
  riskTagsZh?: string[];
  riskTagsEn?: string[];
  profileText?: string;
  profileTextZh?: string;
  profileTextEn?: string;
  aiProfileError?: string;
  fundType?: string;
  trackingIndex?: string;
  theme?: string;
  constituentHint?: string;
  profileVersion?: number;
}

export interface StockV2StockProfileSourceStatus {
  source: string;
  status: string;
  message?: string;
  fetchedAt?: string;
}

export interface StockV2StockProfileUpdateTask {
  id: string;
  symbol: string;
  market?: string;
  triggerSource: "manual" | "auto" | string;
  triggerReason?: string;
  status: string;
  baseInputHashBefore?: string;
  baseInputHashAfter?: string;
  baseInputChanged: boolean;
  aiDecision: string;
  agentRunId?: string;
  sourceStatuses?: StockV2StockProfileSourceStatus[];
  errorMessage?: string;
  startedAt: string;
  finishedAt?: string;
  createdAt: string;
  updatedAt: string;
}

export interface StockV2StockProfileUpdateResult {
  profile: StockV2StockProfile;
  task: StockV2StockProfileUpdateTask;
  agentRun?: StockV2AgentRun;
}

export interface StockV2Portfolio {
  id: string;
  name: string;
  description: string;
  cash: number;
  riskLevel: string; // low, medium, high
  maxSinglePositionPct: number;
  maxDrawdownPct: number;
  allowBuy: boolean;
  allowAdd: boolean;
  allowReduce: boolean;
  allowSell: boolean;
  notes: string;
  createdAt: string;
  updatedAt: string;
}

export interface StockV2PortfolioWithHoldings extends StockV2Portfolio {
  totalValue: number;
  totalAssetValue: number;
  cashPct: number;
  holdings: StockV2Holding[];
}

export interface StockV2PortfolioSnapshot {
  id: string;
  portfolioId: string;
  valuationAt: string;
  cash: number;
  holdingMarketValue: number;
  totalAssetValue: number;
  cashPct: number;
  positionCount: number;
  staleQuoteCount: number;
  estimatedQuoteCount: number;
  source: string;
  status: string;
  createdAt: string;
}

export interface StockV2PortfolioRefreshResult {
  portfolioId: string;
  refreshedCount: number;
  staleCount: number;
  estimatedCount: number;
  failedCount: number;
  failedItems: UpdateFailure[];
  snapshot: StockV2PortfolioSnapshot;
  holdings: StockV2Holding[];
}

export interface StockV2Holding {
  id: string;
  portfolioId: string;
  symbol: string;
  market: string;
  name: string;
  quantity: number;
  availableQuantity: number;
  costPrice: number;
  lastPrice: number;
  lastPriceAt: string;
  tradableStatus: string;
  marketValue: number;
  pnl: number;
  positionPct: number;
  acquiredAt: string;
  createdAt: string;
  updatedAt: string;
}

export interface StockV2Transaction {
  id: string;
  portfolioId: string;
  symbol: string;
  market: string;
  name: string;
  side: "buy" | "sell";
  quantity: number;
  price: number;
  amount: number;
  executedAt: string;
  note: string;
  createdAt: string;
}

export interface StockV2TransactionResult {
  transaction: StockV2Transaction;
  portfolio: StockV2Portfolio;
  holding: StockV2Holding;
  holdingCleared: boolean;
}

export interface StockV2AssetCurvePoint {
  date: string; // YYYY-MM-DD
  cash: number;
  holdingValue: number;
  total: number;
}

export interface StockV2AssetCurveMarker {
  date: string;
  side: "buy" | "sell";
  symbol: string;
  name: string;
  quantity: number;
  price: number;
  amount: number;
  total: number;
}

export interface StockV2AssetCurveResponse {
  portfolioId: string;
  points: StockV2AssetCurvePoint[];
  markers: StockV2AssetCurveMarker[];
  start: string;
  end: string;
  estimated: boolean;
}

export interface StockV2UpdateJob {
  id: string;
  triggerType: string; // manual, scheduled
  triggerSource: string;
  status: string; // running, completed, failed, cancelled
  totalCount: number;
  processedCount: number;
  successCount: number;
  failedCount: number;
  failedItems?: UpdateFailure[];
  startAt: string;
  endAt: string;
  errorMessage: string;
  createdAt: string;
}

export interface UpdateFailure {
  symbol: string;
  reason: string;
}

export interface StockV2UpdateProgress {
  updateJobId: string;
  processedCount: number;
  successCount: number;
  currentBatch: number;
  currentBatchProgress: number;
  currentSymbol: string;
  errorCount: number;
  lastError: string;
  updatedAt: string;
}

export interface StockV2Settings {
  id: string;
  autoUpdateEnabled: boolean;
  dailyBarsAutoEnabled: boolean;
  updateIntervalSec: number;
  proxyEnabled: boolean;
  proxyType: string;
  proxyHost: string;
  proxyPort: number;
  baseProfileAutoMaintainEnabled: boolean;
  baseProfileMaintainIntervalSeconds: number;
  baseProfileDeepUpdateBatchSize: number;
  baseProfileDeepUpdateAiBudget: number;
  baseProfileDeepUpdateRateLimitMs: number;
  baseProfileLastMaintainAt?: string;
  baseProfileNextMaintainAt?: string;
  baseProfileLastMaintainResult?: string;
  lastScheduledUpdate: string;
  dailyBarsLastRun: string;
  createdAt: string;
  updatedAt: string;
}

export interface StockV2UniverseUpdateRequest {
  triggerType?: string;
  triggerSource?: string;
  symbols?: string[];
}

export interface StockV2UniverseUpdateResponse {
  jobId: string;
  message: string;
}

export type StockV2Tab = "overview" | "universe" | "dailyBars" | "portfolios" | "strategies" | "agent";

// ===== News side (消息面数据资产) =====

export interface StockV2NewsSourceState {
  source: string;
  enabled: boolean;
  status: string;
  cursor?: string;
  pollIntervalSeconds: number;
  jitterSeconds: number;
  batchLimit: number;
  processLimit: number;
  backoffBaseSeconds: number;
  backoffMaxSeconds: number;
  nextRunAt?: string;
  lastRunAt?: string;
  lastRunStatus?: string;
  lastRunError?: string;
  lastFetchAt?: string;
  lastSuccessAt?: string;
  lastErrorAt?: string;
  lastError?: string;
  consecutiveFailures: number;
  backoffUntil?: string;
  rawNewsCount: number;
  newsEventCount: number;
  linkCandidateCount: number;
  updatedAt: string;
}

export interface StockV2NewsSourceOverview {
  state: StockV2NewsSourceState;
  configured: boolean;
  reason?: string;
  credentialSet?: boolean;
}

export interface StockV2NewsPipelineRunResult {
  source: string;
  status: string;
  fetchedAt?: string;
  fetchedCount: number;
  rawInsertedCount: number;
  normalizedCount: number;
  linkCandidateCount: number;
  cursor?: string;
  nextCursor?: string;
  errorMessage?: string;
}

export interface StockV2RawNews {
  id: string;
  source: string;
  sourceId?: string;
  language?: string;
  title: string;
  content?: string;
  snippet?: string;
  publishedAt?: string;
  url?: string;
  fetchedAt: string;
  rawPayload?: Record<string, unknown>;
  contentHash: string;
  dedupeKey: string;
  quality: string;
  status: string;
  createdAt: string;
  updatedAt: string;
}

export interface StockV2RawNewsTruncateResult {
  before: string;
  deletedCount: number;
}

export interface StockV2NewsEvent {
  id: string;
  rawNewsId?: string;
  source: string;
  externalId?: string;
  title: string;
  summary?: string;
  content?: string;
  url?: string;
  qualityStatus?: string;
  dedupeKey?: string;
  linkStatus: string;
  eventAt: string;
  linkProcessedAt?: string;
  createdAt: string;
  updatedAt: string;
}

export interface StockV2NewsLinkCandidate {
  id: string;
  newsEventId: string;
  rawNewsId?: string;
  newsEventTitle?: string;
  newsEventSource?: string;
  newsEventAt?: string;
  symbol?: string;
  market?: string;
  instrumentName?: string;
  matchMethod?: string;
  score?: number | null;
  reason?: string;
  matchedTerms?: string[] | null;
  monitorStatus?: string;
  monitorHitId?: string;
  monitoredAt?: string;
  createdAt: string;
  updatedAt: string;
}

export interface StockV2PagedResponse<T> {
  items: T[];
  total?: number;
  limit?: number;
  offset?: number;
}

// ===== Daily Bars (日级历史行情) =====

export interface StockV2DailyBar {
  id: string;
  symbol: string;
  market?: string;
  tradeDate: string;
  open: number;
  high: number;
  low: number;
  close: number;
  prevClose: number;
  volume: number;
  amount: number;
  pctChange: number;
  adjusted: string; // none | qfq | hfq
  source: string;
  fetchedAt: string;
  quality: string; // ok | partial | stale | failed | empty
  errorMessage?: string;
  createdAt: string;
  updatedAt: string;
}

export interface StockV2DailyBarsResponse {
  items: StockV2DailyBar[];
  total: number;
  limit: number;
}

export interface StockV2MinuteBar {
  symbol: string;
  market?: string;
  minuteAt: string;
  open: number;
  high: number;
  low: number;
  close: number;
  prevClose?: number;
  volume?: number;
  amount?: number;
  pctChange?: number;
  mainNetInflow?: number;
  snapshotCount?: number;
  source?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockV2DailyBarsQuality {
  symbol: string;
  adjusted: string;
  hasData: boolean;
  rowCount: number;
  earliestDate: string;
  latestDate: string;
  stale: boolean;
  meets250: boolean;
  lastErrorMessage?: string;
  source?: string;
  checkedAt: string;
}

export interface StockV2DailyBarsEnsureResult {
  symbol: string;
  range: string;
  adjusted: string;
  fetched: number;
  skipped: boolean;
  earliestDate: string;
  latestDate: string;
  quality: StockV2DailyBarsQuality;
  jobId?: string;
  jobRunning: boolean;
  errorMessage?: string;
}

export type DailyBarMode = "symbol" | "hot" | "universe_incremental";

export interface StockV2DailyBarsJobRequest {
  mode?: DailyBarMode | string;
  symbol?: string;
  range?: string;       // 6m | 1y | 3y | 5y
  adjusted?: string;    // none | qfq | hfq
  triggerType?: string; // manual | scheduled | system
  triggerSource?: string; // web | auto-updater | agent
}

export interface StockV2DailyBarJob {
  id: string;
  jobType: string;      // daily_bars_ensure | daily_bars_incremental
  mode: string;         // symbol | hot | universe_incremental
  symbol?: string;
  status: string;       // running | completed | failed | cancelled
  totalCount: number;
  processedCount: number;
  successCount: number;
  failedCount: number;
  failedItems?: UpdateFailure[];
  range?: string;
  adjusted?: string;
  triggerType?: string;
  triggerSource?: string;
  startAt: string;
  endAt: string;
  errorMessage?: string;
  createdAt: string;
}

export type DailyBarRange = "6m" | "1y" | "3y" | "5y";
export type DailyBarAdjusted = "none" | "qfq" | "hfq";

// ===== Strategy (策略对象基础层) =====
//
// 策略是长期判断依据,由系统内置监控任务扫描;Review 只负责当次判断。
// active 策略不可原地覆盖,编辑会生成新 strategy_version,旧版本保留供回看。

export type StockV2StrategyKind = "symbol_strategy" | "portfolio_monitor";
export type StockV2StrategyStatus = "draft" | "active" | "paused" | "archived";
export type StockV2StrategyScope = "research" | "portfolio_bound";
export type StockV2StrategySource = "manual" | "system_template" | "agent";
export type StockV2StrategyDirection = "watch" | "bullish" | "bearish" | "neutral" | "buy_signal" | "sell_signal" | "hold";
export type StockV2StrategyVersionStatus = "draft" | "active" | "superseded";
export type StockV2StrategyActionType = "observe" | "build_position" | "add_position" | "hold" | "reduce_position" | "exit_position";
export type StockV2StrategyPrefilterType =
  | "price_above"
  | "price_below"
  | "price_between"
  | "pct_change_above"
  | "pct_change_below"
  | "daily_close_above"
  | "daily_close_below"
  | "quote_stale"
  | "portfolio_symbol_weight_above"
  | "portfolio_symbol_weight_below"
  | "news_semantic_relevance";

export interface StockV2StrategyPrefilter {
  key?: string;
  type: StockV2StrategyPrefilterType | string;
  threshold?: number;
  low?: number;
  high?: number;
  maxAgeSeconds?: number;
  minScore?: number;
  topics?: string[];
}

export interface StockV2StrategyActionRule {
  id?: string;
  action: StockV2StrategyActionType | string;
  title?: string;
  trigger?: string;
  preconditions?: string;
  target?: string;
  risk?: string;
  dataPrefilters?: StockV2StrategyPrefilter[];
  portfolioPrefilters?: StockV2StrategyPrefilter[];
  newsPrefilters?: StockV2StrategyPrefilter[];
  priority?: number;
}

export interface StockV2StrategyPlaybook {
  version?: string;
  rules?: StockV2StrategyActionRule[];
}

/** 单个策略版本的具体判断内容。旧版本必须保留。 */
export interface StockV2StrategyVersion {
  id?: string;
  strategyId?: string;
  versionNo: number;
  status?: StockV2StrategyVersionStatus | string;
  title?: string;
  direction?: StockV2StrategyDirection | string;
  thesis?: string;
  entryConditions?: string[];
  exitConditions?: string[];
  riskNotes?: string;
  evidenceRefs?: string[];
  generationMeta?: Record<string, unknown>;
  createdBy?: string;
  createdAt?: string;
}

/** 策略对象。后端基础对象 + 前端从 activeVersion 派生出的展示字段。 */
export interface StockV2Strategy {
  id: string;
  name: string;
  kind: StockV2StrategyKind | string;
  status: StockV2StrategyStatus | string;
  scope: StockV2StrategyScope | string;
  source: StockV2StrategySource | string;
  symbol?: string;
  market?: string;
  instrumentName?: string;
  portfolioId?: string;
  portfolioName?: string;
  activeVersionId?: string;
  direction?: StockV2StrategyDirection | string;
  playbook?: StockV2StrategyPlaybook;
  activeVersionNo?: number;
  hasDraft?: boolean;
  title?: string;
  thesis?: string;
  entryConditions?: string;
  exitConditions?: string;
  riskNotes?: string;
  evidenceRefs?: string[];
  generationMeta?: Record<string, unknown>;
  entryPriceLow?: number;
  entryPriceHigh?: number;
  triggerPriceAbove?: number;
  triggerPriceBelow?: number;
  stopLoss?: number;
  takeProfit?: number;
  targetPositionPct?: number;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockV2StrategyWithVersion {
  strategy: StockV2Strategy;
  activeVersion?: StockV2StrategyVersion;
}

export interface StockV2StrategyListResponse {
  items: StockV2StrategyWithVersion[];
  total?: number;
  limit?: number;
  offset?: number;
}

export interface StockV2StrategyVersionListResponse {
  items: StockV2StrategyVersion[];
}

/** 创建/编辑策略时提交的判断内容。后端据此创建 strategy 与首发版本。 */
export interface StockV2StrategyInput {
  name?: string;
  kind?: StockV2StrategyKind | string;
  scope?: StockV2StrategyScope | string;
  symbol?: string;
  market?: string;
  portfolioId?: string;
  title?: string;
  direction?: StockV2StrategyDirection | string;
  thesis?: string;
  entryConditions?: string[];
  exitConditions?: string[];
  riskNotes?: string;
  evidenceRefs?: string[];
  generationMeta?: Record<string, unknown>;
  createdBy?: string;
}

// ===== 策略生成 (strategy_generation) =====
//
// Agent 策略生成入口提交体。后端 POST /api/stockv2/agent/strategy-generation/run
// 接收 StrategyGenerationInput(camelCase)，返回 StockV2AgentRun；策略草案异步落库。
export type StockV2StrategyGenerationMode =
  | "manual_target"
  | "single_instrument"
  | "portfolio_strategy_diagnosis";

export type StockV2StrategyGenerationTimeHorizon =
  | "short"
  | "swing"
  | "medium"
  | "long"
  | "unspecified";

export interface StockV2StrategyGenerationTargetInstrument {
  symbol: string;
  market?: string;
  name?: string;
  userNote?: string;
}

export interface StockV2StrategyGenerationInput {
  schemaVersion?: string;
  mode: StockV2StrategyGenerationMode;
  userGoal?: string;
  userIntent?: string;
  portfolioId?: string;
  targetInstruments?: StockV2StrategyGenerationTargetInstrument[];
  requestedBy?: string;
  opportunityId?: string;
  timeHorizon?: StockV2StrategyGenerationTimeHorizon;
  allowedActions?: StockV2StrategyActionType[];
  evidenceScope?: Record<string, boolean>;
}

// ===== Legacy Watch / Alert compatibility =====
//
// Watch 不再是 V2 的用户主模型;保留这些类型是为了兼容旧路由、旧数据和底层规则评估。
// 新 UI 只暴露系统内置 MonitorTask 的开关、周期、运行历史、命中记录和 Alert 台账。
export type StockV2WatchStatus = "active" | "paused" | "archived";
export type StockV2WatchSource = "manual" | "strategy" | "portfolio_monitor";
export type StockV2WatchTriggerPolicy = "any" | "all";
export type StockV2WatchRuleType =
  | "price_above"
  | "price_below"
  | "price_between"
  | "pct_change_above"
  | "pct_change_below"
  | "quote_stale"
  | "daily_close_above"
  | "daily_close_below"
  | "portfolio_symbol_weight_above"
  | "portfolio_symbol_weight_below";
export type StockV2WatchScheduleKind = "manual" | "market_session" | "daily";

export interface StockV2WatchRuleConfig {
  key?: string;
  type?: StockV2WatchRuleType | string;
  ruleType?: StockV2WatchRuleType | string;
  symbol?: string;
  portfolioId?: string;
  threshold?: number;
  low?: number;
  high?: number;
  maxAgeSeconds?: number;
}

export interface StockV2WatchTriggerConfig {
  source?: string;
  template?: string;
  rules?: StockV2WatchRuleConfig[];
  [key: string]: unknown;
}

export interface StockV2Watch {
  id: string;
  name?: string;
  status?: StockV2WatchStatus | string;
  source?: StockV2WatchSource | string;
  symbol?: string;
  market?: string;
  instrumentName?: string;
  portfolioId?: string;
  portfolioName?: string;
  strategyId?: string;
  strategyName?: string;
  strategyVersionId?: string;
  triggerPolicy?: StockV2WatchTriggerPolicy | string;
  triggerConfig?: StockV2WatchTriggerConfig;
  triggerKind?: StockV2WatchRuleType | string;
  threshold?: number;
  comparator?: string;
  cooldownSeconds?: number;
  scheduleKind?: StockV2WatchScheduleKind | string;
  /** 后端可预生成的规则摘要;缺失时前端按 triggerKind + threshold 拼。 */
  ruleSummary?: string;
  lastCheckedAt?: string;
  lastTriggeredAt?: string;
  lastRunStatus?: string;
  lastRunReason?: string;
  archivedAt?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockV2WatchInput {
  name: string;
  source?: StockV2WatchSource | string;
  symbol?: string;
  market?: string;
  portfolioId?: string;
  strategyId?: string;
  strategyVersionId?: string;
  triggerPolicy?: StockV2WatchTriggerPolicy | string;
  triggerConfig?: StockV2WatchTriggerConfig;
  cooldownSeconds?: number;
  scheduleKind?: StockV2WatchScheduleKind | string;
}

export interface StockV2WatchListResponse {
  items: StockV2Watch[];
  total?: number;
  limit?: number;
  offset?: number;
}

export interface StockV2WatchRuleResult {
  ruleKey?: string;
  ruleType?: StockV2WatchRuleType | string;
  status?: "matched" | "not_matched" | "skipped" | "degraded" | string;
  reason?: string;
  observedValue?: number;
  threshold?: unknown;
  evidence?: Record<string, unknown>;
  dataTime?: string;
}

/** 旧 Watch 兼容运行结果。matched / not_matched / skipped / degraded 为规则评估状态。 */
export interface StockV2WatchRunResult {
  watchId?: string;
  status?: "matched" | "not_matched" | "skipped" | "degraded" | string;
  reason?: string;
  ruleResults?: StockV2WatchRuleResult[];
  alert?: StockV2Alert;
  checkedAt?: string;
  totals?: {
    matched?: number;
    notMatched?: number;
    skipped?: number;
    degraded?: number;
  };
  /** 命中后产生的新 alert(若有)。 */
  alerts?: StockV2Alert[];
  note?: string;
}

export type StockV2AlertStatus = "open" | "acknowledged" | "ignored" | "resolved";
export type StockV2AlertLevel = "info" | "warning" | "critical";

export interface StockV2Alert {
  id: string;
  watchId?: string;
  monitorHitId?: string;
  monitorRunId?: string;
  taskType?: string;
  strategyId?: string;
  portfolioId?: string;
  market?: string;
  reviewId?: string;
  reviewStatus?: string;
  agentRunId?: string;
  decisionLedgerId?: string;
  triggerSource?: string;
  watchName?: string;
  status?: StockV2AlertStatus | string;
  level?: StockV2AlertLevel | string;
  title?: string;
  summary?: string;
  symbol?: string;
  portfolioName?: string;
  triggerKind?: string;
  threshold?: number;
  observedValue?: number;
  dedupeKey?: string;
  evidence?: Record<string, unknown>;
  occurrenceCount?: number;
  firstSeenAt?: string;
  lastSeenAt?: string;
  triggeredAt?: string;
  createdAt?: string;
  updatedAt?: string;
  acknowledgedAt?: string;
  resolvedAt?: string;
}

export interface StockV2AlertListResponse {
  items: StockV2Alert[];
  total?: number;
  limit?: number;
  offset?: number;
}

// ===== Monitor(监控与任务:系统固化后台监控的可观测性)=====
// Watch 不再是用户主模型;监控任务由系统内置,用户只配置开关/周期/范围/敏感度/冷却/Agent。

export type StockV2MonitorRunStatus = "running" | "completed" | "failed" | "cancelled";
export type StockV2MonitorTriggerType = "manual" | "scheduled" | "event";
export type StockV2MonitorHitStatus = "candidate" | "doublechecked" | "alerted" | "reviewed" | "ignored";

export interface StockV2MonitorTaskConfig {
  enabled?: boolean;
  intervalSeconds?: number;
  scope?: string;
  sensitivity?: string;
  cooldownSeconds?: number;
  agentDoublecheckEnabled?: boolean;
  agentBudget?: number;
}

export interface StockV2MonitorTaskDefinition {
  taskType: string;
  label?: string;
  description?: string;
  category?: string;
  runnable?: boolean;
  configurable?: boolean;
  defaultConfig?: StockV2MonitorTaskConfig;
}

export interface StockV2MonitorRun {
  id: string;
  taskType: string;
  status?: StockV2MonitorRunStatus | string;
  triggerType?: string;
  startedAt?: string;
  finishedAt?: string;
  scopeSummary?: string;
  scannedCount?: number;
  hitCount?: number;
  alertCount?: number;
  reviewCount?: number;
  successCount?: number;
  failedCount?: number;
  errorMessage?: string;
  metadata?: Record<string, unknown>;
  createdAt?: string;
}

export interface StockV2MonitorHit {
  id: string;
  runId?: string;
  taskType?: string;
  status?: StockV2MonitorHitStatus | string;
  strategyId?: string;
  portfolioId?: string;
  symbol?: string;
  market?: string;
  title?: string;
  summary?: string;
  evidence?: Record<string, unknown>;
  agentDecisionId?: string;
  alertId?: string;
  createdAt?: string;
}

export interface StockV2MonitorReviewPipeline {
  reviewId?: string;
  reviewCreated?: boolean;
  reviewStatus?: string;
  agentDoublecheckEnabled?: boolean;
  agentAttempted?: boolean;
  agentStatus?: string;
  agentSkippedReason?: string;
  agentRunId?: string;
  agentRunStatus?: string;
  agentError?: string;
  error?: string;
}

export type StockV2OperationReviewStatus = "pending" | "running" | "completed" | "failed" | "closed";
export type StockV2OperationReviewOutputType =
  | "trade_signal"
  | "proposed_operation"
  | "strategy_patch"
  | "ignore"
  | "continue_monitoring";

export interface StockV2QuoteLatest {
  symbol: string;
  market?: string;
  name?: string;
  lastPrice: number;
  prevClose?: number;
  openPrice?: number;
  highPrice?: number;
  lowPrice?: number;
  volume?: number;
  amount?: number;
  pctChange?: number;
  amplitude?: number;
  turnoverRate?: number;
  volumeRatio?: number;
  mainNetInflow?: number;
  superNetInflow?: number;
  largeNetInflow?: number;
  mediumNetInflow?: number;
  smallNetInflow?: number;
  mainNetInflowPct?: number;
  quoteAt?: string;
  fetchedAt?: string;
  source?: string;
  status?: string;
  errorMessage?: string;
}

export interface StockV2DailyBarsContext {
  symbol?: string;
  adjusted?: string;
  count?: number;
  latestTradeDate?: string;
  latestClose?: number;
  latestFetchedAt?: string;
  quality?: string;
  summary?: Record<string, number>;
}

export interface StockV2MinuteBarsContext {
  symbol?: string;
  count?: number;
  latestMinuteAt?: string;
  latestClose?: number;
  latestVolume?: number;
  latestNetInflow?: number;
  source?: string;
  summary?: Record<string, number>;
}

export interface StockV2PortfolioReviewContext {
  portfolio: StockV2Portfolio;
  snapshot?: StockV2PortfolioSnapshot;
  holdings?: StockV2Holding[];
}

export interface StockV2AgentContextPack {
  builtAt?: string;
  hit?: StockV2MonitorHit;
  evidence?: Record<string, unknown>;
  strategy?: StockV2StrategyWithVersion;
  quote?: StockV2QuoteLatest;
  dailyBars?: StockV2DailyBarsContext;
  minuteBars?: StockV2MinuteBarsContext;
  portfolio?: StockV2PortfolioReviewContext;
  freshness?: Record<string, unknown>;
}

export interface StockV2OperationReview {
  id: string;
  hitId: string;
  runId?: string;
  status: StockV2OperationReviewStatus | string;
  outputType?: StockV2OperationReviewOutputType | string;
  strategyId?: string;
  portfolioId?: string;
  symbol?: string;
  market?: string;
  inputContext?: StockV2AgentContextPack;
  result?: Record<string, unknown>;
  resultSummary?: string;
  errorMessage?: string;
  createdAt?: string;
  updatedAt?: string;
  completedAt?: string;
  closedAt?: string;
}

export interface StockV2OperationReviewListResponse {
  items: StockV2OperationReview[];
  total?: number;
  limit?: number;
  offset?: number;
}

export interface StockV2OperationReviewResultInput {
  outputType?: StockV2OperationReviewOutputType | string;
  result?: Record<string, unknown>;
  resultSummary?: string;
  status?: StockV2OperationReviewStatus | string;
  errorMessage?: string;
}

export interface StockV2OperationReviewActionInput {
  reason?: string;
  executedAt?: string;
  price?: number;
  quantity?: number;
}

export interface StockV2MonitorTask {
  definition: StockV2MonitorTaskDefinition;
  config: StockV2MonitorTaskConfig;
  latestRun?: StockV2MonitorRun;
}

export interface StockV2MonitorTaskConfigInput {
  enabled?: boolean;
  intervalSeconds?: number;
  scope?: string;
  sensitivity?: string;
  cooldownSeconds?: number;
  agentDoublecheckEnabled?: boolean;
  agentBudget?: number;
}

export interface StockV2MonitorTaskListResponse {
  items: StockV2MonitorTask[];
}

export interface StockV2MonitorRunListResponse {
  items: StockV2MonitorRun[];
  total?: number;
  limit?: number;
  offset?: number;
}

export interface StockV2MonitorHitListResponse {
  items: StockV2MonitorHit[];
  total?: number;
  limit?: number;
  offset?: number;
}

export interface StockV2QuoteRefreshStatus {
  symbol: string;
  market?: string;
  source?: string;
  status: string;
  lastAttemptAt?: string;
  lastSuccessAt?: string;
  lastFailureAt?: string;
  errorMessage?: string;
  consecutiveFailures?: number;
  updatedAt?: string;
}

export interface StockV2QuoteRefreshTaskState {
  taskType: string;
  status: string;
  triggerType?: string;
  startedAt?: string;
  finishedAt?: string;
  scopeSummary?: string;
  scannedCount?: number;
  successCount?: number;
  failedCount?: number;
  errorMessage?: string;
  updatedAt?: string;
}

export interface StockV2QuoteRefreshStateResponse {
  state: StockV2QuoteRefreshTaskState;
  items: StockV2QuoteRefreshStatus[];
}

// ===== Agent 治理层(V2)===== 对齐 internal/stockv2/agent_types.go

export type StockV2AgentProviderType = "openai" | "codex_cli" | "local";
export type StockV2AgentListResponse<T> = { items: T[]; total?: number; limit?: number; offset?: number };

export interface StockV2AgentProviderProfile {
  id: string;
  providerType: StockV2AgentProviderType | string;
  name: string;
  displayName?: string;
  baseUrl?: string;
  apiKeySet?: boolean;
  configState?: string;
  authState?: string;
  availability?: string;
  lastProbeAt?: string;
  lastProbeResult?: string;
  metadata?: Record<string, unknown>;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockV2AgentModelProfile {
  id: string;
  providerId: string;
  modelName: string;
  displayName?: string;
  enabled: boolean;
  status?: string;
  costLevel?: string;
  contextLimit?: number;
  metadata?: Record<string, unknown>;
  createdAt?: string;
  updatedAt?: string;
}

export type StockV2AgentTaskType =
  | "operation_review"
  | "strategy_generation"
  | "opportunity_discovery"
  | "news_event_review"
  | "portfolio_risk_review"
  | "stock_profile_summary"
  | "bull_bear_debate";

export interface StockV2AgentTaskProfile {
  id: string;
  taskType: StockV2AgentTaskType | string;
  primaryModelId?: string;
  fallbackModelId?: string;
  maxBudget?: number;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockV2AgentMCPStatus {
  enabled: boolean;
  serverName: string;
  transport: string;
  url?: string;
  requiredTools: string[];
}

export type StockV2AgentRunStatus = "pending" | "ready" | "running" | "completed" | "failed";

export interface StockV2AgentRun {
  id: string;
  taskType: string;
  providerId?: string;
  modelId?: string;
  triggerObjectType?: string;
  triggerObjectId?: string;
  status: StockV2AgentRunStatus | string;
  costEstimate?: Record<string, unknown>;
  errorMessage?: string;
  output?: string;
  decisionLedgerId?: string;
  startedAt?: string;
  finishedAt?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockV2AgentDecisionLedger {
  id: string;
  runId?: string;
  providerId?: string;
  modelId?: string;
  taskType: string;
  triggerObjectType?: string;
  triggerObjectId?: string;
  inputSummary?: string;
  prompt?: string;
  inputArtifactSummary?: string;
  outputArtifactSummary?: string;
  structuredOutput?: Record<string, unknown>;
  redactionSummary?: Record<string, unknown>;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockV2AgentExecutionDetail {
  run: StockV2AgentRun;
  ledger?: StockV2AgentDecisionLedger;
  review?: StockV2OperationReview;
  inputContext?: StockV2AgentContextPack;
}

// ===== Agent 请求体 =====

export interface StockV2AgentCreateProviderRequest {
  providerType: StockV2AgentProviderType | string;
  name?: string;
  displayName?: string;
  baseUrl?: string;
  apiKey?: string;
  configState?: string;
  authState?: string;
  availability?: string;
  metadata?: Record<string, unknown>;
}

export interface StockV2AgentRunCLIDebugRequest {
  modelId: string;
  requestedBy?: string;
  async?: boolean;
}

export interface StockV2AgentUpdateProviderRequest {
  name?: string;
  displayName?: string;
  baseUrl?: string;
  apiKey?: string;
  configState?: string;
  authState?: string;
  availability?: string;
  metadata?: Record<string, unknown>;
}

export interface StockV2AgentCreateModelRequest {
  providerId: string;
  modelName: string;
  displayName?: string;
  enabled?: boolean;
  status?: string;
  costLevel?: string;
  contextLimit?: number;
  metadata?: Record<string, unknown>;
}

export interface StockV2AgentUpdateModelRequest {
  displayName?: string;
  enabled?: boolean;
  status?: string;
  costLevel?: string;
  contextLimit?: number;
  metadata?: Record<string, unknown>;
}

export interface StockV2AgentUpdateTaskProfileRequest {
  primaryModelId?: string;
  fallbackModelId?: string;
  maxBudget?: number;
}

export interface StockV2AgentProviderModelCatalogItem {
  id: string;
  displayName?: string;
  visibility?: string;
  supportedInAPI?: boolean;
  source?: string;
}

export interface StockV2AgentProviderModelCatalog {
  providerId: string;
  items: StockV2AgentProviderModelCatalogItem[];
}

export interface StockV2AgentModelTestResult {
  ok: boolean;
  message?: string;
  latencyMs?: number;
}
