export type MainTab = "dashboard" | "codex-gateway" | "codex" | "logs" | "images" | "docker" | "stock" | "stockv2" | "v2ray" | "settings";
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

export interface SystemSettings {
  eventRetentionDays: number;
}

export interface SettingsPayload {
  file?: FileSettings;
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
  py?: string;
  pyFull?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface StockInstrumentQueryParams {
  q?: string;
  market?: string;
  status?: string;
  industry?: string;
  quality?: string;
  sort?: "relevance" | "symbol_asc" | "market_then_symbol" | "updated_desc";
  page?: number;
  pageSize?: number;
  includeDelisted?: boolean;
}

export interface StockInstrumentSearchResponse {
  total: number;
  page: number;
  pageSize: number;
  items: StockInstrument[];
  snippets?: Record<string, Record<string, string>>;
  fts?: boolean;
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

export interface StockDataMaintenanceResult {
  tasks?: StockDataTask[];
  sources?: StockDataSource[];
  instruments?: StockInstrument[];
  quotes?: StockQuote[];
  marketData?: StockMarketDataPoint[];
  newsItems?: StockNewsItem[];
  opportunities?: StockOpportunity[];
  alerts?: StockAlert[];
  notes?: string[];
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
  settings?: StockSettings;
}

export interface StockSettings {
  id?: string;
  proxyEnabled?: boolean;
  proxyType?: string;
  proxyAddress?: string;
  proxyUseForEastmoney?: boolean;
  proxyUseForSina?: boolean;
  proxyUseForTencent?: boolean;
  quoteTtlSeconds?: number;
  autoRefreshEnabled?: boolean;
  refreshIntervalSecs?: number;
  defaultDataSource?: string;
  createdAt?: string;
  updatedAt?: string;
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
  createdAt: string;
  updatedAt: string;
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
  updateIntervalSec: number;
  proxyEnabled: boolean;
  proxyType: string;
  proxyHost: string;
  proxyPort: number;
  lastScheduledUpdate: string;
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

export type StockV2Tab = "overview" | "universe" | "portfolios" | "settings";
