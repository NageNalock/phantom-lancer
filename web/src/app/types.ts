export type MainTab = "dashboard" | "codex-gateway" | "codex" | "logs" | "images" | "docker" | "stock" | "v2ray" | "mail" | "settings";
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
  logFile?: string;
  logMaxSizeMB?: number;
  logMaxFiles?: number;
  logMaxAgeDays?: number;
  tlsBootStrict?: boolean;
  hstsDefaultsApplied?: boolean;
}

export interface SettingsPayload {
  file?: FileSettings;
  runtime?: RuntimeSettings;
  listener?: ListenerEndpoint;
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

export interface StockMarketClock {
  market?: string;
  timezone?: string;
  now?: string;
  tradingDay?: boolean;
  calendarStatus?: string;
  activeSession?: boolean;
  session?: string;
  nextActionHint?: string;
}

export interface StockSummary {
  portfolioCount?: number;
  strategyCount?: number;
  activeWatchCount?: number;
  openAlertCount?: number;
  pendingReviewCount?: number;
  pendingOperationCount?: number;
  totalCash?: number;
  totalMarketValue?: number;
  totalAssetValue?: number;
  lastAlertAt?: string;
}

export interface StockDataHealth {
  sourceCount?: number;
  availableSources?: number;
  degradedSources?: number;
  failedSources?: number;
  instrumentCount?: number;
  marketPointCount?: number;
  newsItemCount?: number;
  importantNewsCount?: number;
  taskCount?: number;
  failedTaskCount?: number;
  staleQuoteCount?: number;
  lastTaskAt?: string;
  lastNewsAt?: string;
}

export interface StockDataSource {
  source: string;
  displayName?: string;
  sourceType?: string;
  authMode?: string;
  enabled?: boolean;
  status?: string;
  quality?: string;
  lastCursor?: string;
  lastIngestedAt?: string;
  nextAllowedAt?: string;
  consecutiveFailures?: number;
  failureSummary?: string;
  rateLimitSeconds?: number;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockInstrument {
  symbol?: string;
  market?: string;
  name?: string;
  status?: string;
  industry?: string;
  concept?: string;
  listingDate?: string;
  source?: string;
  quality?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockMarketDataPoint {
  id?: string;
  symbol?: string;
  market?: string;
  dataset?: string;
  dataDate?: string;
  open?: number;
  high?: number;
  low?: number;
  close?: number;
  volume?: number;
  amount?: number;
  pe?: number;
  pb?: number;
  turnoverRate?: number;
  netInflow?: number;
  quality?: string;
  source?: string;
  rawJson?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockDataCoverage {
  symbol?: string;
  dataset?: string;
  firstDate?: string;
  lastDate?: string;
  pointCount?: number;
  latestQuality?: string;
  latestSource?: string;
  updatedAt?: string;
}

export interface StockNewsItem {
  id?: string;
  source?: string;
  sourceItemId?: string;
  symbol?: string;
  market?: string;
  title?: string;
  summary?: string;
  category?: string;
  importance?: string;
  keywords?: string;
  quality?: string;
  publishedAt?: string;
  dedupeKey?: string;
  rawPayload?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockDataTask {
  id?: string;
  taskType?: string;
  source?: string;
  symbol?: string;
  status?: string;
  requestedBy?: string;
  inputJson?: string;
  resultJson?: string;
  processedCount?: number;
  failedCount?: number;
  failureSummary?: string;
  startedAt?: string;
  completedAt?: string;
  nextRunAt?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockPortfolio {
  id: string;
  name?: string;
  description?: string;
  cash?: number;
  riskLevel?: string;
  maxSinglePositionPct?: number;
  maxDrawdownPct?: number;
  allowBuy?: boolean;
  allowAdd?: boolean;
  allowReduce?: boolean;
  allowSell?: boolean;
  notes?: string;
  holdings?: StockHolding[];
  marketValue?: number;
  totalAssetValue?: number;
  cashPct?: number;
  constraintStatus?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockHolding {
  id: string;
  portfolioId?: string;
  symbol?: string;
  market?: string;
  name?: string;
  quantity?: number;
  availableQuantity?: number;
  costPrice?: number;
  lastPrice?: number;
  lastPriceAt?: string;
  tradableStatus?: string;
  marketValue?: number;
  pnl?: number;
  positionPct?: number;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockQuote {
  symbol?: string;
  market?: string;
  name?: string;
  lastPrice?: number;
  previousClose?: number;
  volume?: number;
  amount?: number;
  dataTimestamp?: string;
  dataFreshness?: string;
  tradableStatus?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockStrategy {
  id: string;
  title?: string;
  strategyType?: "account_agnostic" | "account_bound" | string;
  portfolioId?: string;
  symbol?: string;
  market?: string;
  name?: string;
  direction?: string;
  entryPriceLow?: number;
  entryPriceHigh?: number;
  triggerPriceAbove?: number;
  triggerPriceBelow?: number;
  takeProfit?: number;
  stopLoss?: number;
  targetPositionPct?: number;
  status?: string;
  source?: string;
  thesis?: string;
  riskNotes?: string;
  currentVersion?: number;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockOpportunity {
  id: string;
  title?: string;
  sourceType?: string;
  sourceRefId?: string;
  symbol?: string;
  market?: string;
  name?: string;
  theme?: string;
  thesis?: string;
  evidenceSummary?: string;
  confidence?: string;
  status?: string;
  linkedStrategyId?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockWatch {
  id: string;
  strategyId?: string;
  portfolioId?: string;
  symbol?: string;
  market?: string;
  name?: string;
  status?: string;
  checkIntervalSeconds?: number;
  triggerPriceAbove?: number;
  triggerPriceBelow?: number;
  cooldownSeconds?: number;
  lastCheckedAt?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockAlert {
  id: string;
  watchId?: string;
  strategyId?: string;
  portfolioId?: string;
  symbol?: string;
  market?: string;
  name?: string;
  level?: string;
  status?: string;
  sourceType?: string;
  sourceRefId?: string;
  dedupeKey?: string;
  cooldownUntil?: string;
  title?: string;
  summary?: string;
  triggerReason?: string;
  createdAt?: string;
  updatedAt?: string;
  acknowledgedAt?: string;
  resolvedAt?: string;
}

export interface StockReview {
  id: string;
  alertId?: string;
  watchId?: string;
  strategyId?: string;
  portfolioId?: string;
  symbol?: string;
  market?: string;
  name?: string;
  status?: string;
  reviewResult?: string;
  inputJson?: string;
  outputJson?: string;
  guardrailResult?: string;
  summary?: string;
  createdAt?: string;
  updatedAt?: string;
  completedAt?: string;
}

export interface StockTradeSignal {
  id: string;
  reviewId?: string;
  strategyId?: string;
  symbol?: string;
  market?: string;
  name?: string;
  direction?: string;
  priceRange?: string;
  triggerSummary?: string;
  stopLoss?: number;
  takeProfit?: number;
  status?: string;
  createdAt?: string;
}

export interface StockProposedOperation {
  id: string;
  reviewId?: string;
  strategyId?: string;
  portfolioId?: string;
  symbol?: string;
  market?: string;
  name?: string;
  action?: string;
  quantity?: number;
  price?: number;
  amount?: number;
  targetPositionPct?: number;
  guardrailResult?: string;
  guardrailReason?: string;
  status?: string;
  createdAt?: string;
  confirmedAt?: string;
}

export interface StockOperation {
  id: string;
  proposedOperationId?: string;
  portfolioId?: string;
  symbol?: string;
  market?: string;
  name?: string;
  action?: string;
  quantity?: number;
  price?: number;
  amount?: number;
  occurredAt?: string;
  notes?: string;
  createdAt?: string;
}

export interface StockMemory {
  id: string;
  portfolioId?: string;
  symbol?: string;
  objectType?: string;
  objectId?: string;
  summary?: string;
  createdAt?: string;
}

export interface StockAgentTraceSummary {
  runCount?: number;
  completedRunCount?: number;
  failedRunCount?: number;
  pendingPatchCount?: number;
  claimCount?: number;
  lastRunAt?: string;
}

export interface StockAgentModelProfile {
  id?: string;
  name?: string;
  provider?: string;
  model?: string;
  taskType?: string;
  decisionProtocol?: string;
  authMode?: string;
  enabled?: boolean;
  temperature?: number;
  dailyTokenBudget?: number;
  dailyCostBudget?: number;
  status?: string;
  lastUsedAt?: string;
  failureSummary?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockDataAdapterStatus {
  key?: string;
  source?: string;
  label?: string;
  category?: string;
  configured?: boolean;
}

export interface StockAgentRun {
  id: string;
  triggerSource?: string;
  triggerObjectType?: string;
  triggerObjectId?: string;
  strategyId?: string;
  portfolioId?: string;
  watchId?: string;
  alertId?: string;
  reviewId?: string;
  symbol?: string;
  decisionProtocol?: string;
  status?: string;
  result?: string;
  confidence?: string;
  modelProfileId?: string;
  provider?: string;
  model?: string;
  promptSnapshot?: string;
  inputSnapshot?: string;
  outputSnapshot?: string;
  runGraphJson?: string;
  skillSnapshotJson?: string;
  toolSnapshotJson?: string;
  costSummaryJson?: string;
  summary?: string;
  redactionSummary?: string;
  startedAt?: string;
  completedAt?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockAgentAuthorization {
  id: string;
  runId?: string;
  reviewId?: string;
  profileId?: string;
  taskType?: string;
  decisionProtocol?: string;
  provider?: string;
  model?: string;
  symbol?: string;
  status?: string;
  reason?: string;
  requestedBy?: string;
  decision?: string;
  errorSummary?: string;
  createdAt?: string;
  decidedAt?: string;
  completedAt?: string;
  updatedAt?: string;
}

export interface StockAgentRunStep {
  id: string;
  runId?: string;
  stepKey?: string;
  role?: string;
  status?: string;
  inputJson?: string;
  outputJson?: string;
  toolCallsJson?: string;
  latencyMs?: number;
  tokenEstimate?: number;
  summary?: string;
  startedAt?: string;
  completedAt?: string;
  createdAt?: string;
}

export interface StockAgentClaim {
  id: string;
  runId?: string;
  stepId?: string;
  claimType?: string;
  text?: string;
  evidenceJson?: string;
  verificationStatus?: string;
  confidence?: string;
  sourceRef?: string;
  createdAt?: string;
}

export interface StockStrategyPatch {
  id: string;
  runId?: string;
  reviewId?: string;
  strategyId?: string;
  patchJson?: string;
  summary?: string;
  status?: string;
  createdAt?: string;
  updatedAt?: string;
  acceptedAt?: string;
}

export interface StockPayload {
  summary?: StockSummary;
  dataHealth?: StockDataHealth;
  agentTrace?: StockAgentTraceSummary;
  marketClock?: StockMarketClock;
  portfolios?: StockPortfolio[];
  quotes?: StockQuote[];
  dataSources?: StockDataSource[];
  dataAdapters?: StockDataAdapterStatus[];
  instruments?: StockInstrument[];
  marketData?: StockMarketDataPoint[];
  dataCoverage?: StockDataCoverage[];
  newsItems?: StockNewsItem[];
  dataTasks?: StockDataTask[];
  opportunities?: StockOpportunity[];
  strategies?: StockStrategy[];
  watches?: StockWatch[];
  alerts?: StockAlert[];
  reviews?: StockReview[];
  tradeSignals?: StockTradeSignal[];
  proposedOperations?: StockProposedOperation[];
  operations?: StockOperation[];
  memories?: StockMemory[];
  agentProfiles?: StockAgentModelProfile[];
  agentRuns?: StockAgentRun[];
  agentAuthorizations?: StockAgentAuthorization[];
  agentSteps?: StockAgentRunStep[];
  agentClaims?: StockAgentClaim[];
  strategyPatches?: StockStrategyPatch[];
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
  stock: StockPayload;
  mail: MailPayload;
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

// --- Mail / Mox control-plane types (Phase 1 skeletons) ---------------
// These shapes mirror the Go structs in internal/storage/storage_mail.go
// and the payloads served from internal/httpapi/mail.go.  They are
// deliberately populated in Phase 1 even though most fields will sit
// empty until Phase 2+ — doing so keeps the UI layout stable and lets
// TypeScript catch wiring bugs ahead of time.

export interface MailStatus {
  ok: boolean;
  service_ready: boolean;
  config_mode: "managed" | "import" | "";
  desired_state: "running" | "stopped" | "";
  phantom_instance_id: string;
  import_mode: boolean;
  mox_root: string;
  domain_count: number;
  account_count: number;
  emergency_inbound_reject?: MailEmergencyInboundRejectState;
}

export interface MailEmergencyInboundRejectState {
  enabled: boolean;
  reason?: string;
  mode: string;
  applied_by?: string;
  applied_at?: string;
  auto_restore_at?: string;
  last_auto_restore_attempt_at?: string;
  auto_restore_blocked_at?: string;
  last_normal_config_hash?: string;
  last_config_hash?: string;
  last_apply_summary?: string;
  last_failure?: string;
  last_failure_step?: number;
  last_rollback_result?: string;
  last_reload_result?: string;
  last_probe_result?: string;
  restore_conflict?: string;
  restore_expected_hash?: string;
  restore_disk_hash?: string;
  apply_unknown?: boolean;
  affected_domains: number;
  affected_accounts: number;
  actual_mox_strategy: string;
  degraded_implementation?: boolean;
  degraded_reason?: string;
}

export interface MailDomain {
  id: string;
  domain: string;
  enabled: boolean;
  dkim_selector: string;
  dmarc_policy: string;
  dmarc_rua: string;
  spf_include: string;
  dns_provider_id: string;
  cert_id: string;
  synced: boolean;
  last_synced_at?: string;
  last_sync_error?: string;
  last_dns_check_at?: string;
  dns_check_json?: unknown;
  created_at: string;
  updated_at: string;
}

export interface MailAccount {
  id: string;
  // Phase 1 legacy fields (may still be referenced by older views / stubs).
  email?: string;
  display_name: string;
  recovery_email?: string;
  storage_limit_mb?: number;
  enabled?: boolean;
  role?: "user" | "admin" | "catch-all" | "";
  import_mode_read_only?: boolean;
  synced?: boolean;
  last_synced_at?: string;
  last_sync_error?: string;
  last_password_changed_at?: string;
  sync_state?: "idle" | "syncing" | "error" | "paused" | "";
  // Phase 5 canonical fields.
  domain_id?: string;
  local_part?: string;
  address?: string;
  password_mode?: "generated" | "external" | "disabled";
  status?: "active" | "disabled";
  quota_mb?: number;
  is_admin?: boolean;
  imap_sync_enabled?: boolean;
  imap_sync_state?: "idle" | "syncing" | "error" | "paused";
  imap_error?: string;
  webapi_credential_present?: boolean;
  webapi_endpoint_valid?: boolean;
  webapi_runtime_available?: boolean;
  send_disabled_reason?: string;
  can_send?: boolean;
  last_login_at?: string;
  created_at: string;
  updated_at: string;
}

export interface MailAlias {
  id: string;
  // Phase 1 legacy fields.
  domain_id: string;
  alias_addr?: string;
  mode?: "forward" | "list" | "catch-all" | "";
  enabled?: boolean;
  recipient_ids?: string[];
  synced?: boolean;
  // Phase 5 canonical fields.
  source?: string;
  recipients?: string[];
  alias_mode?: "alias" | "catchall" | "list" | "drop";
  list_name?: string;
  list_reply_to?: string;
  description?: string;
  created_at: string;
  updated_at: string;
}

export interface MailCertificate {
  id: string;
  domain_id: string;
  provider: "dns01" | "manual" | "external" | "";
  dns_provider_id: string;
  subject: string;
  san_domains: string[];
  not_before?: string;
  not_after?: string;
  last_renewed_at?: string;
  last_attempt_at?: string;
  last_error?: string;
  tlsa_rr_3_1_1?: string;
  status: "valid" | "expiring" | "expired" | "pending" | "failed" | "";
  created_at: string;
}

export interface MailMessage {
  id: string;
  account_id: string;
  folder: string;
  uid: number;
  message_id: string;
  subject: string;
  from_addr: string;
  from_name: string;
  to_addrs: string[];
  cc_addrs: string[];
  replied_at?: string;
  forwarded_at?: string;
  flags: string[];
  size_bytes: number;
  internaldate: string;
  preview_text: string;
  has_attachments: boolean;
  attachment_count: number;
  synced: boolean;
  created_at: string;
}

export interface MailSettings {
  id: number;
  phantom_instance_id: string;
  import_mode: boolean;
  import_label: string;
  config_mode: string;
  desired_state: string;
  mox_binary_path: string;
  mox_data_dir: string;
  mox_config_path: string;
  webapi_endpoint: string;
  admin_email: string;
  hostname: string;
  smtp_port: number;
  smtp_submission_port: number;
  smtps_port: number;
  imap_port: number;
  imaps_port: number;
  webmail_addr: string;
  webapi_addr: string;
  acme_default_provider_id: string;
  queue_max_size_bytes: number;
  queue_max_age_seconds: number;
  outbound_rate_limit_per_hour: number;
  retention_delivery_events_days: number;
  retention_health_checks_per_type: number;
  search_index_max_size_gb: number;
  dnsbl_enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface MailPayload {
  status: MailStatus;
  settings: MailSettings;
  domains: MailDomain[];
  accounts: MailAccount[];
  aliases: MailAlias[];
  certificates: MailCertificate[];
  // Phase 6: Delivery / Queue / Suppression / Webhook / Outbound
  queueSummary?: {
    hold: number;
    active: number;
    schedule: number;
    deferred: number;
    fail: number;
    drop: number;
  };
  deliverySummary?: {
    sent_24h: number;
    bounced_24h: number;
    deferred_count: number;
    pending_count: number;
  };
  suppressionSummary?: {
    active_count: number;
    added_7d: number;
    expiring_soon: number;
  };
  webhookSummary?: {
    registration_count: number;
    recent_events: number;
  };
  outboundSummary?: {
    send_1m: number;
    send_1h: number;
    send_24h: number;
    bounce_rate_pct: number;
  };
  // Phase 8: Logs / Backup / Retention / Danger Zone
  logFiles?: Array<{
    path: string;
    size_bytes: number;
    modified_at: string;
    lines_estimated: number;
  }>;
  backups?: Array<{
    id: string;
    scope: "config" | "data_full";
    file_path: string;
    file_size_bytes: number;
    checksum_sha256?: string;
    contains_config: boolean;
    contains_data: boolean;
    note?: string;
    created_at: string;
    expires_at?: string;
  }>;
  backupSchedules?: Array<{
    id: string;
    scope: string;
    enabled: boolean;
    cron_expr: string;
    retention_days: number;
    max_copies: number;
    last_run_at?: string;
    last_run_ok: boolean;
    last_error?: string;
    next_run_at?: string;
    created_at: string;
    updated_at: string;
  }>;
  retentionRules?: Array<{
    id: string;
    scope: "delivery_events" | "health_checks" | "webhook_events" | "mail_index_messages";
    retain_days?: number;
    retain_max_rows?: number;
    created_at: string;
    updated_at: string;
  }>;
}
