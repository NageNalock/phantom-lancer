import type { ApiError, AuditEvent, EventRecord } from "../app/types";

interface ApiOptions {
  method?: string;
  body?: unknown | FormData;
  csrf?: string;
}

export function readCookie(name: string): string {
  const match = document.cookie.split("; ").find((part) => part.startsWith(`${name}=`));
  return match ? decodeURIComponent(match.split("=").slice(1).join("=")) : "";
}

export async function api<T>(path: string, options: ApiOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  const init: RequestInit = { method: options.method || "GET", headers };

  if (options.body instanceof FormData) {
    init.body = options.body;
  } else if (options.body !== undefined) {
    headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(options.body);
  }

  if (options.method && options.method !== "GET") {
    headers["X-CSRF-Token"] = options.csrf || readCookie("pl_csrf");
  }

  const response = await fetch(path, init);
  const payload = (await response.json().catch(() => ({}))) as {
    error?: { code?: string; message?: string };
  };

  if (!response.ok) {
    const error = new Error(payload.error?.message || `请求失败：${response.status}`) as ApiError;
    error.code = payload.error?.code || "";
    error.status = response.status;
    error.payload = payload;
    throw error;
  }

  return payload as T;
}

function unwrapItems<T>(resp: T[] | { items?: T[] }): T[] {
  return Array.isArray(resp) ? resp : resp.items || [];
}

export function friendlyError(error: unknown): string {
  const err = error as ApiError;
  if (err.code === "path_out_of_boundary") {
    return "该路径不在允许根目录内。请使用允许根目录作为路径开头，或先在设置页更新允许根目录。";
  }
  if (err.code === "workspace_path_missing") return "目录不存在。请确认后创建目录，或改用已有目录。";
  if (err.code === "workspace_path_invalid") return `工作目录无效：${err.message}`;
  if (err.code === "workspace_create_failed") return `创建目录失败：${err.message}`;
  if (err.code === "invalid_allowed_roots") return `允许根目录无效：${err.message}`;
  if (err.code === "git_required") return "该目录不是 Git 仓库。如确认要添加，请勾选允许非 Git 目录。";
  if (err.code === "permission_denied") return "workspace-write 需要选择已允许写入的项目。请改用只读沙箱，或在项目配置中允许写入。";
  if (err.code === "codex_app_server_failed") return `Codex app-server 请求失败：${err.message}`;
  if (err.code === "codex_model_required") return "Codex app-server 需要明确模型。请选择一个可用模型后重试。";
  if (err.code === "api_key_missing") return "Images 模块尚未配置 xAI API Key。";
  if (err.code === "provider_failed") return `图片模型调用失败：${err.message}`;
  if (err.code === "images_settings_invalid") return `Images 设置无效：${err.message}`;
  if (err.code === "image_prompt_invalid") return `Prompt 无效：${err.message}`;
  if (err.code === "image_prompt_not_found") return "未找到该 Prompt，可能已被删除。";
  if (err.code === "object_storage_profile_invalid") return `对象存储 profile 无效：${err.message}`;
  if (err.code === "object_storage_profile_in_use") return err.message || "该对象存储 profile 仍被模块引用，无法删除。";
  if (err.code === "object_storage_test_failed") return `对象存储连接测试失败：${err.message}`;
  if (err.code === "docker_unavailable") return `Docker 操作失败：${err.message}`;
  if (err.code === "docker_install_unavailable") return `Docker 安装不可用：${err.message}`;
  if (err.code === "docker_daemon_control_unavailable") return `Docker daemon 控制不可用：${err.message}`;
  if (err.code === "docker_settings_invalid") return `Docker 设置无效：${err.message}`;
  if (err.code === "docker_registry_settings_invalid") return `Docker Registry 设置无效：${err.message}`;
  if (err.code === "docker_registry_failed") return `Docker Registry 操作失败：${err.message}`;
  if (err.code === "docker_container_create_disabled") return "模板化容器创建尚未开启。";
  if (err.code === "docker_container_image_denied") return "镜像必须来自当前受控 Registry 主机。";
  if (err.code === "docker_image_ref_invalid") return err.message || "镜像引用无效。";
  if (err.code?.startsWith("image_") || err.code?.startsWith("prompt_") || err.code === "source_count_invalid" || err.code === "mode_invalid") return `图片生成参数无效：${err.message}`;
  if (err.code === "v2ray_config_invalid") return `V2Ray 配置无效：${err.message}`;
  if (err.code === "v2ray_control_failed") return `V2Ray 服务控制失败：${err.message}`;
  if (err.code === "gateway_disabled") return "Codex Gateway 尚未启用。";
  if (err.code === "no_available_accounts") return "没有可用的 Codex Gateway 账号。";
  if (err.code === "model_not_supported") return "当前账号 plan 不支持该模型。";
  if (err.code === "oauth_settings_invalid") return `OAuth 设置无效：${err.message}`;
  if (err.code === "model_refresh_failed") return `模型刷新失败：${err.message}`;
  if (err.code === "stock_portfolio_in_use") return err.message || "该账户仍被策略、盯盘或历史记录引用，不能删除。";
  if (err.code === "stock_portfolio_not_found") return "该股票账户不存在，可能已被删除。";
  if (err.code === "stock_portfolio_update_failed") return `股票账户更新失败：${err.message}`;
  // Mail / Mox control-plane error codes (Phase 2+ fill in the bodies).
  // These mirror the writeError() calls in internal/httpapi/mail.go.
  if (err.code === "config_drifted") return `检测到 Mox 配置漂移：${err.message || "请在「总览」中选择以 Phantom 或磁盘为准重新同步。"}`;
  if (err.code === "mail_domain_invalid") return `域名无效：${err.message}`;
  if (err.code === "mailbox_auth_failed") return `邮箱账户认证失败：${err.message || "请重置密码后重试。"}`;
  if (err.code === "port_preflight_failed") return `端口预检失败：${err.message || "请确认 SMTP/IMAP/MSA 端口未被其它服务占用，或调整设置中的端口。"}`;
  if (err.code === "binary_checksum_mismatch") return `Mox 二进制校验和不匹配：${err.message || "请重新下载，或手动指定可信 checksum。"}`;
  if (err.code === "mox_min_version") return `当前安装的 Mox 版本过旧：${err.message || "请升级到最低要求版本。"}`;
  if (err.code === "import_read_only") return `已启用 Mail 只读导入模式：${err.message || "该操作需要先切换回托管模式。"}`;
  if (err.code === "mail_not_wired") return `Mail 服务未接入：${err.message || "请检查服务启动日志。"}`;
  if (err.code === "binary_detect_failed") return `检测 Mox 二进制失败：${err.message}`;
  if (err.code === "binary_download_failed") return `下载 Mox 二进制失败：${err.message}`;
  if (err.code === "binary_install_failed") return `安装 Mox 二进制失败：${err.message}`;
  if (err.code === "binary_uninstall_failed") return `卸载 Mox 二进制失败：${err.message}`;
  if (err.code === "setup_initialize_failed") return `初始化 Mox 实例失败：${err.message}`;
  if (err.code === "setup_import_failed") return `接入外部 Mox 失败：${err.message}`;
  if (err.code === "preflight_ports_failed") return `端口预检失败：${err.message}`;
  if (err.code === "runtime_status_failed") return `读取运行状态失败：${err.message}`;
  if (err.code === "start_failed") return `启动 Mox 失败：${err.message}`;
  if (err.code === "stop_failed") return `停止 Mox 失败：${err.message}`;
  if (err.code === "restart_failed") return `重启 Mox 失败：${err.message}`;
  if (err.code === "runtime_probe_failed") return `探针执行失败：${err.message}`;
  if (err.code === "config_validate_failed") return `配置校验失败：${err.message}`;
  if (err.code === "config_apply_failed") return `配置应用失败：${err.message}`;
  if (err.code === "config_rollback_failed") return `配置回滚失败：${err.message}`;
  if (err.code === "drift_resolve_failed") return `漂移消解失败：${err.message}`;
  if (err.code === "mail_domain_not_found") return `域名不存在：${err.message}`;
  if (err.code === "mail_domain_duplicate") return `域名重复：${err.message}`;
  if (err.code === "mail_domain_reserved") return `域名保留或未登记：${err.message}`;
  if (err.code === "dns_check_failed") return `DNS 检查失败：${err.message}`;
  // --- CertManager error codes (Phase 4) ---
  if (err.code === "cert_list_failed") return `证书列表加载失败：${err.message}`;
  if (err.code === "cert_not_found") return `证书不存在：${err.message}`;
  if (err.code === "cert_issue_failed") return `证书签发失败：${err.message || "请检查 DNS 提供商配置与域名权限。"}`;
  if (err.code === "cert_renew_failed") return `证书续期失败：${err.message}`;
  if (err.code === "cert_rollback_failed") return `证书回滚失败：${err.message}`;
  if (err.code === "cert_delete_failed") return `证书删除失败：${err.message}`;
  if (err.code === "dns_provider_list_failed") return `DNS 提供商列表加载失败：${err.message}`;
  if (err.code === "dns_provider_upsert_failed") return `DNS 提供商保存失败：${err.message}`;
  if (err.code === "dns_provider_delete_failed") return `DNS 提供商删除失败：${err.message}`;
  if (err.code === "dns_provider_test_failed") return `DNS 提供商连接测试失败：${err.message}`;
  if (err.code === "manual_challenge_list_failed") return `手动挑战列表加载失败：${err.message}`;
  if (err.code === "manual_challenge_confirm_failed") return `确认手动挑战失败：${err.message}`;
  if (err.code === "manual_challenge_cancel_failed") return `取消手动挑战失败：${err.message}`;
  // --- Phase 5: Accounts / Aliases / Import ---
  if (err.code === "account_quota_exceeded") return `邮箱账户配额超限：${err.message || "请调整域名或全局配额后重试。"}`;
  if (err.code === "invalid_local_part") return `本地用户名无效：${err.message || "仅允许字母、数字、点、连字符和下划线。"}`;
  if (err.code === "address_already_taken") return `邮箱地址已被占用：${err.message || "请使用其他地址或别名。"}`;
  if (err.code === "password_mode_external") return `该账户使用外部认证：${err.message || "请在外部系统中修改密码，或先切换为本地密码模式。"}`;
  if (err.code === "alias_recipient_invalid") return `别名收件人无效：${err.message || "请检查收件人列表中的邮箱格式。"}`;
  if (err.code === "import_path_not_found") return `导入路径不存在：${err.message || "请确认 Mox 数据目录与配置文件路径后重试。"}`;
  if (err.code === "import_probe_failed") return `导入探针失败：${err.message || "无法读取指定路径或实例未就绪。"}`;
  // --- Phase 6: Delivery / Queue / Suppression / Webhook / Outbound ---
  if (err.code === "delivery_not_found") return `投递记录不存在：${err.message}`;
  if (err.code === "queue_action_invalid") return `队列操作无效：${err.message || "请确认所选桶或操作后重试。"}`;
  if (err.code === "suppression_invalid") return `抑制条目无效：${err.message || "请检查收件人哈希与原因后重试。"}`;
  if (err.code === "webhook_signature_invalid") return `Webhook 签名校验失败：${err.message || "请确认共享密钥、算法与时间戳。"}`;
  if (err.code === "webhook_timestamp_expired") return `Webhook 时间戳已过期：${err.message || "请求可能被重放，请检查发送端时钟与签名窗口。"}`;
  if (err.code === "webhook_source_blocked") return `Webhook 来源地址不在允许的 CIDR 内：${err.message || "请在 Webhook 配置中放宽来源 CIDR，或确认请求来源。"}`;
  if (err.code === "webhook_body_too_large") return `Webhook 请求体过大：${err.message || "请在配置中调大 max_body_bytes，或让发送端拆分负载。"}`;
  if (err.code === "outbound_scope_not_found") return `出站速率作用域不存在：${err.message || "请先通过阈值设置创建作用域。"}`;
  if (err.code === "dnsbl_probe_failed") return `DNSBL 探测失败：${err.message || "请确认出站网络与 DNS 解析可用后重试。"}`;
  // --- Phase 7: Mailbox (Folders / Messages / Search / IMAP Sync / Compose) ---
  if (err.code === "folder_not_found") return `文件夹不存在：${err.message || "请刷新列表后重试。"}`;
  if (err.code === "folder_update_failed") return `文件夹保存失败：${err.message}`;
  if (err.code === "folder_delete_failed") return `文件夹删除失败：${err.message}`;
  if (err.code === "message_not_found") return `邮件不存在：${err.message || "可能已被删除，请刷新。"}`;
  if (err.code === "message_list_failed") return `邮件列表加载失败：${err.message}`;
  if (err.code === "message_delete_failed") return `邮件删除失败：${err.message}`;
  if (err.code === "message_move_failed") return `邮件移动失败：${err.message}`;
  if (err.code === "flags_update_failed") return `邮件标记更新失败：${err.message}`;
  if (err.code === "raw_fetch_failed") return `原文加载失败：${err.message}`;
  if (err.code === "attachment_not_stored") return `该附件尚未落地缓存：${err.message || "请等待 IMAP 同步完成后重试。"}`;
  if (err.code === "attachment_fetch_failed") return `附件加载失败：${err.message}`;
  if (err.code === "search_failed") return `全文搜索失败：${err.message}`;
  if (err.code === "invalid_search_scope") return `搜索作用域无效：${err.message || "仅支持 one / all / attachments。"}`;
  if (err.code === "index_health_failed") return `索引健康检查失败：${err.message}`;
  if (err.code === "index_reset_failed") return `索引重建请求失败：${err.message}`;
  if (err.code === "account_not_found") return `账户不存在：${err.message}`;
  if (err.code === "sync_control_failed") return `同步控制失败：${err.message}`;
  if (err.code === "imap_sync_state_invalid") return `IMAP 同步重置失败：${err.message}`;
  if (err.code === "from_address_not_registered") return `发件地址未登记：${err.message || "请先在账户中添加该地址或改用已登记的 From。"}`;
  if (err.code === "compose_failed") return `发送失败：${err.message}`;
  if (err.code === "draft_failed") return `草稿保存失败：${err.message}`;
  if (err.code === "draft_not_found") return `草稿不存在：${err.message}`;
  if (err.code === "draft_delete_failed") return `草稿删除失败：${err.message}`;
  // --- Phase 8: Logs / Backup / Retention / Danger Zone ---
  if (err.code === "path_not_allowed") return "访问被拒绝：日志路径超出允许的目录范围";
  if (err.code === "disk_space_insufficient") return "磁盘空间不足，无法完成备份";
  if (err.code === "backup_not_found") return "备份记录不存在";
  if (err.code === "backup_list_failed") return `备份列表加载失败：${err.message || "数据库迁移未完成，请重启服务后重试。"}`;
  if (err.code === "retention_rule_invalid") return `保留规则无效：${err.message || "目标和保留天数必须有效。"}`;
  if (err.code === "danger_code_mismatch") return "验证码不匹配";
  if (err.code === "danger_code_expired") return "验证码已过期（请在 120 秒内完成操作）";
  if (err.code === "danger_countdown_incomplete") return "60 秒倒计时尚未结束";
  if (err.code === "danger_checkboxes_incomplete") return "必须勾选全部三个确认复选框";
  if (err.code === "danger_account_mismatch") return "账户名与当前登录账户不匹配";
  if (err.code === "log_file_too_large") return "日志文件超过 10MB 的单次读取上限；请使用过滤器或流式模式";
  if (err.code === "hard_delete_disabled") return "当前构建版本未启用硬删除功能；仅在生产环境允许使用";
  return err.message || "请求失败";
}

// --- Mail / Mox control-plane RPC wrappers (Phase 2.5) ---------------------
//
// Each wrapper is a thin call through `api<T>(path, {...})` so call sites
// stay typed and free of path-string duplication.  The CSRF token is read
// from the cookie by the underlying `api()` helper; callers pass `csrf`
// explicitly only when they want to override the cookie value.

export interface MailBinaryDetectRequest {
  hint_path?: string;
  extra_path?: string[];
  version_timeout_ms?: number;
  skip_path?: boolean;
}
export interface MailBinaryInfo {
  path?: string;
  version?: string;
  in_whitelist?: boolean;
  size_bytes?: number;
  source?: string;
}
export interface MailBinaryDetectResponse {
  controlled?: MailBinaryInfo;
  path?: MailBinaryInfo;
  hint?: MailBinaryInfo;
  selected?: MailBinaryInfo;
}

export interface MailBinaryDownloadRequest {
  version: string;
  override_url?: string;
  dest_dir?: string;
  size_max_bytes?: number;
  report_percent?: boolean;
}
export interface MailBinaryDownloadResponse {
  temp_path: string;
  size_bytes: number;
  checksum_sha256: string;
  expected_sha256: string;
  version: string;
}

export interface MailBinaryInstallRequest {
  src?: string;
  version?: string;
  checksum_sha256?: string;
  force?: boolean;
}
export interface MailBinaryInstallResponse {
  installed: boolean;
  installed_version: string;
  installed_path: string;
  previous_version: string;
  backup_path: string;
}

export interface MailBinaryUninstallRequest {
  force?: boolean;
}
export interface MailBinaryUninstallResponse {
  removed_binary: boolean;
  removed_sidecar: boolean;
  backups_removed: number;
  uninstalled_version: string;
  controlled_dir: string;
}

export interface MailSetupInitializeRequest {
  admin_email: string;
  hostname: string;
  webapi_addr: string;
  webmail_addr: string;
  use_controlled_binary?: boolean;
  overwrite_existing_conf?: boolean;
}
export interface MailSetupInitializeResponse {
  config_path: string;
  data_dir: string;
  binary_path: string;
  placeholder_note: string;
  next_steps: string[];
}

export interface MailSetupImportRequest {
  binary_path: string;
  config_path: string;
  data_dir: string;
  label: string;
}
export interface MailSetupImportResponse {
  imported: boolean;
  label: string;
  preflight_notes: string[];
}

export interface MailPortCheck {
  name: string;
  port: number;
  host: string;
  free: boolean;
  conflict?: string;
}
export interface MailPreflightPortsResponse {
  ports: MailPortCheck[];
  all_ok: boolean;
}

export interface MailLifecycleRequest {
  reason?: string;
  block_ms?: number;
}
export interface MailLifecycleResponse {
  requested: "start" | "stop" | "restart";
  accepted: boolean;
  observed_now: string;
  message?: string;
}

export interface MailProbeResult {
  name: string;
  layer: number;
  state: "unknown" | "good" | "warn" | "critical" | "error";
  message?: string;
  duration_ms?: number;
}
export interface MailRuntimeStatus {
  config_mode: "managed" | "import" | "";
  desired_state: "running" | "stopped" | "";
  import_mode: boolean;
  observed_state: string;
  pid: number;
  boot_id: string;
  crash_loop_state: string;
  consecutive_failures: number;
  backoff_remaining_ms: number;
  uptime_ms: number;
  binary_controlled?: MailBinaryInfo;
  binary_path?: MailBinaryInfo;
  binary_selected?: MailBinaryInfo;
  probes: MailProbeResult[];
  overall: "unknown" | "good" | "warn" | "critical" | "error";
  domain_count: number;
  account_count: number;
  last_probe_at: string;
  last_change_at: string;
  emergency_inbound_reject?: MailEmergencyInboundRejectState;
}
export interface MailRuntimeProbeRequest {
  layers?: number[];
}
export interface MailRuntimeProbeResponse {
  results: MailProbeResult[];
  overall: MailRuntimeStatus["overall"];
  at: string;
}

export function mailBinaryDetect(req: MailBinaryDetectRequest = {}, csrf?: string) {
  return api<MailBinaryDetectResponse>("/api/mail/binary/detect", { method: "POST", csrf, body: req });
}
export function mailBinaryDownload(req: MailBinaryDownloadRequest, csrf?: string) {
  return api<MailBinaryDownloadResponse>("/api/mail/binary/download", { method: "POST", csrf, body: req });
}
export function mailBinaryInstall(req: MailBinaryInstallRequest, csrf?: string) {
  return api<MailBinaryInstallResponse>("/api/mail/binary/install", { method: "POST", csrf, body: req });
}
export function mailBinaryUninstall(req: MailBinaryUninstallRequest = {}, csrf?: string) {
  return api<MailBinaryUninstallResponse>("/api/mail/binary/uninstall", { method: "POST", csrf, body: req });
}
export function mailSetupInitialize(req: MailSetupInitializeRequest, csrf?: string) {
  return api<MailSetupInitializeResponse>("/api/mail/setup/initialize", { method: "POST", csrf, body: req });
}
export function mailSetupImport(req: MailSetupImportRequest, csrf?: string) {
  return api<MailSetupImportResponse>("/api/mail/setup/import", { method: "POST", csrf, body: req });
}
export function mailSetupPreflightPorts(csrf?: string) {
  return api<MailPreflightPortsResponse>("/api/mail/setup/preflight-ports", { method: "POST", csrf, body: {} });
}
export function mailRuntimeStatus() {
  return api<MailRuntimeStatus>("/api/mail/runtime/status", { method: "GET" });
}
export function mailRuntimeStart(req: MailLifecycleRequest = {}, csrf?: string) {
  return api<MailLifecycleResponse>("/api/mail/runtime/start", { method: "POST", csrf, body: req });
}
export function mailRuntimeStop(req: MailLifecycleRequest = {}, csrf?: string) {
  return api<MailLifecycleResponse>("/api/mail/runtime/stop", { method: "POST", csrf, body: req });
}
export function mailRuntimeRestart(req: MailLifecycleRequest = {}, csrf?: string) {
  return api<MailLifecycleResponse>("/api/mail/runtime/restart", { method: "POST", csrf, body: req });
}
export function mailRuntimeProbe(req: MailRuntimeProbeRequest = {}, csrf?: string) {
  return api<MailRuntimeProbeResponse>("/api/mail/runtime/probe", { method: "POST", csrf, body: req });
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

export interface MailEmergencyInboundRejectRequest {
  reason?: string;
  confirmation: string;
  auto_restore_at?: string;
}

export function mailEmergencyInboundRejectGet() {
  return api<MailEmergencyInboundRejectState>("/api/mail/emergency/inbound-reject", { method: "GET" });
}
export function mailEmergencyInboundRejectEnable(req: MailEmergencyInboundRejectRequest, csrf?: string) {
  return api<{ state: MailEmergencyInboundRejectState; pipeline?: MailPipelineResult }>("/api/mail/emergency/inbound-reject/enable", { method: "POST", csrf, body: req });
}
export function mailEmergencyInboundRejectDisable(req: MailEmergencyInboundRejectRequest, csrf?: string) {
  return api<{ state: MailEmergencyInboundRejectState; pipeline?: MailPipelineResult }>("/api/mail/emergency/inbound-reject/disable", { method: "POST", csrf, body: req });
}

// --- Phase 3: Config application + drift + domains ---------------------------

export interface MailStepStatus {
  step: number;
  total: number;
  name: string;
  percent: number;
  message?: string;
  output?: string;
  state: "running" | "done" | "failed" | "rollback";
}
export interface MailPipelineResult {
  success: boolean;
  steps: MailStepStatus[];
  failure_step: number;
  rolled_back: boolean;
  rollback_err?: string;
  config_hash?: string;
  summary?: string;
}
export interface MailConfigValidateRequest {
  dry_run?: boolean;
}
export interface MailConfigValidateResponse {
  ok: boolean;
  errors: string[];
  warnings: string[];
  diff_added: string[];
  diff_removed: string[];
  sha?: string;
}
export interface MailConfigApplyRequest {
  expect_hash?: string;
  force?: boolean;
}
export interface MailConfigRollbackRequest {
  reason?: string;
}
export interface MailConfigRollbackResponse {
  ok: boolean;
  restored_from: string;
  message?: string;
}
export interface MailConfigSummaryResponse {
  current_hash: string;
  last_good_hash?: string;
  last_apply_at?: string;
  last_apply_result?: string;
  backup_exists: boolean;
  backup_hash?: string;
  diff_added: string[];
  diff_removed: string[];
  drifted: boolean;
}
export interface MailResolveDriftRequest {
  action: "overwrite" | "reimport";
}
export interface MailResolveDriftResponse {
  accepted: boolean;
  action: "overwrite" | "reimport";
  new_hash: string;
  message?: string;
}

export interface MailDomain {
  id?: string;
  domain: string;
  dkim_selector: string;
  dmarc_policy: "none" | "quarantine" | "reject";
  dmarc_rua: string;
  spf_include: string;
  enabled: boolean;
  synced: boolean;
  dns_status: MailDomainDNSStatus;
  dns_records: MailDNSRecord[];
  account_count?: number;
  created_at?: string;
  updated_at?: string;
}
export interface MailDomainDNSStatus {
  mx_ok?: boolean;
  spf_ok?: boolean;
  dkim_ok?: boolean;
  dmarc_ok?: boolean;
  tlsrpt_ok?: boolean;
  ptr_ok?: boolean;
  tlsa_ok?: boolean;
  autoconfig_ok?: boolean;
  last_check_at?: string;
  overall?: "good" | "warn" | "critical" | "error" | "unknown";
}
export interface MailDNSRecord {
  type: string;
  name: string;
  value: string;
  priority?: number;
  ttl?: number;
  checked?: boolean;
  ok?: boolean;
}

export function mailConfigValidate(req: MailConfigValidateRequest = {}, csrf?: string) {
  return api<MailConfigValidateResponse>("/api/mail/config/validate", { method: "POST", csrf, body: req });
}
export function mailConfigApply(req: MailConfigApplyRequest = {}, csrf?: string) {
  return api<MailPipelineResult>("/api/mail/config/apply", { method: "POST", csrf, body: req });
}
export function mailConfigRollback(req: MailConfigRollbackRequest = {}, csrf?: string) {
  return api<MailConfigRollbackResponse>("/api/mail/config/rollback", { method: "POST", csrf, body: req });
}
export function mailConfigSummary() {
  return api<MailConfigSummaryResponse>("/api/mail/config/summary", { method: "GET" });
}
export function mailResolveDrift(req: MailResolveDriftRequest, csrf?: string) {
  return api<MailResolveDriftResponse>("/api/mail/runtime/resolve-drift", { method: "POST", csrf, body: req });
}
export function mailDomainList() {
  return api<{ items: MailDomain[]; total: number; count: number; drifted: boolean }>("/api/mail/domains", { method: "GET" });
}
export function mailDomainCreate(req: MailDomain, csrf?: string) {
  return api<MailDomain>("/api/mail/domains", { method: "POST", csrf, body: req });
}
export function mailDomainGet(id: string) {
  return api<MailDomain>(`/api/mail/domains/${encodeURIComponent(id)}`, { method: "GET" });
}
export function mailDomainUpdate(id: string, req: MailDomain, csrf?: string) {
  return api<MailDomain>(`/api/mail/domains/${encodeURIComponent(id)}`, { method: "PUT", csrf, body: req });
}
export function mailDomainDelete(id: string, csrf?: string) {
  return api<{ ok: boolean }>(`/api/mail/domains/${encodeURIComponent(id)}`, { method: "DELETE", csrf });
}
export function mailDomainEnable(id: string, enable: boolean, csrf?: string) {
  return api<MailDomain>(`/api/mail/domains/${encodeURIComponent(id)}/enable`, { method: "POST", csrf, body: { enable } });
}
export function mailDomainDNSCheck(id: string, csrf?: string) {
  return api<MailDomainDNSStatus>(`/api/mail/domains/${encodeURIComponent(id)}/dns-check`, { method: "POST", csrf });
}
export function mailDomainDNSRecords(id: string) {
  return api<{ items: MailDNSRecord[]; domain: string }>(`/api/mail/domains/${encodeURIComponent(id)}/dns-records`, { method: "GET" });
}

// --- Phase 4: CertManager --------------------------------------------------

// DNS Provider types
export interface MailDNSProvider {
  id: string;
  kind: "cloudflare" | "dnspod" | "route53" | "manual" | string;
  display_name: string;
  config_keys: string[];
  has_token: boolean;
  tested: boolean;
  last_tested_at?: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
}

export interface MailDNSProviderRecord {
  type: string;
  name: string;
  value: string;
  ttl?: number;
  ok?: boolean;
  note?: string;
}

export interface MailDNSProviderUpsertRequest {
  id?: string;
  kind: string;
  display_name: string;
  config: Record<string, string>;
}

export interface MailDNSProviderUpsertResponse {
  id: string;
  kind: string;
  display_name: string;
  config_keys: string[];
  has_token: boolean;
  tested: boolean;
  last_tested_at?: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
}

export interface MailDNSProviderTestResponse {
  ok: boolean;
  message: string;
  zones?: string[];
}

// Certificate types
export interface MailCertificate {
  id: string;
  domain: string;
  sans?: string[];
  issuer?: string;
  serial?: string;
  not_before?: string;
  not_after?: string;
  days_left: number;
  tlsa_record?: string;
  dns_provider_id?: string;
  status: "active" | "renewing" | "expiring_soon" | "expired" | "error" | "manual_pending";
  last_renewal_at?: string;
  next_renewal_at?: string;
  last_error?: string;
  files: { privkey: string; cert: string; chain: string };
}

export interface MailCertificateIssueRequest {
  domain: string;
  sans?: string[];
  dns_provider_id?: string;
  force?: boolean;
  accept_tos: boolean;
  contact_email: string;
  acme_directory_url?: string;
  tlsa_enabled?: boolean;
  mx_host?: string;
}

export interface MailCertificateRenewResponse {
  renewed: boolean;
  message: string;
  pipeline?: MailCertPipelineResult;
  cert?: MailCertificate;
}

export interface MailCertificateListResponse {
  items: MailCertificate[];
  count: number;
  expiring_count: number;
  expired_count: number;
  active_count: number;
  manual_pending_count: number;
  drifted: boolean;
}

export interface MailCertificateGetResponse {
  id: string;
  domain: string;
  sans?: string[];
  issuer?: string;
  serial?: string;
  not_before?: string;
  not_after?: string;
  days_left: number;
  tlsa_record?: string;
  dns_provider_id?: string;
  status: MailCertificate["status"];
  last_renewal_at?: string;
  next_renewal_at?: string;
  last_error?: string;
  files: { privkey: string; cert: string; chain: string };
}

export interface MailCertificateRollbackResponse {
  restored: boolean;
  message: string;
  from_backup?: string;
}

// Manual challenge types
export interface MailManualChallenge {
  id: string;
  cert_id: string;
  domain: string;
  fqdn: string;
  value: string;
  expires_at: string;
  created_at: string;
}

export interface MailManualChallengeListResponse {
  items: MailManualChallenge[];
  count: number;
}

export interface MailManualChallengeConfirmRequest {
  confirm: boolean;
}

// Cert pipeline result (mirrors MailPipelineResult shape for StepProgress)
export interface MailCertPipelineStep {
  step: number;
  total: number;
  name: string;
  percent: number;
  message?: string;
  output?: string;
  state: "running" | "done" | "failed" | "rollback";
}
export interface MailCertPipelineResult {
  success: boolean;
  failure_step: number;
  rolled_back: boolean;
  rollback_err?: string;
  summary?: string;
  steps?: MailCertPipelineStep[];
  cert_id?: string;
}

// ---- RPC wrappers ---------------------------------------------------------

// Certificates (6)
export function mailCertificateList() {
  return api<MailCertificateListResponse>("/api/mail/certificates", { method: "GET" });
}
export function mailCertificateIssue(req: MailCertificateIssueRequest, csrf?: string) {
  return api<MailCertPipelineResult>("/api/mail/certificates", { method: "POST", csrf, body: req });
}
export function mailCertificateGet(id: string) {
  return api<MailCertificateGetResponse>(`/api/mail/certificates/${encodeURIComponent(id)}`, { method: "GET" });
}
export function mailCertificateRenew(id: string, force = false, csrf?: string) {
  return api<MailCertificateRenewResponse>(`/api/mail/certificates/${encodeURIComponent(id)}/renew`, { method: "POST", csrf, body: { force } });
}
export function mailCertificateRollback(id: string, csrf?: string) {
  return api<MailCertificateRollbackResponse>(`/api/mail/certificates/${encodeURIComponent(id)}/rollback`, { method: "POST", csrf });
}
export function mailCertificateDelete(id: string, csrf?: string) {
  return api<{ deleted: boolean; id: string; message: string }>(`/api/mail/certificates/${encodeURIComponent(id)}`, { method: "DELETE", csrf });
}

// DNS Providers (4)
export function mailDNSProviderList() {
  return api<{ items: MailDNSProvider[]; count: number }>("/api/mail/dns-providers", { method: "GET" });
}
export function mailDNSProviderUpsert(req: MailDNSProviderUpsertRequest, csrf?: string) {
  return api<MailDNSProviderUpsertResponse>("/api/mail/dns-providers", { method: "POST", csrf, body: req });
}
export function mailDNSProviderDelete(id: string, csrf?: string) {
  return api<{ ok: boolean }>(`/api/mail/dns-providers/${encodeURIComponent(id)}`, { method: "DELETE", csrf });
}
export function mailDNSProviderTest(id: string, csrf?: string) {
  return api<MailDNSProviderTestResponse>(`/api/mail/dns-providers/${encodeURIComponent(id)}/test`, { method: "POST", csrf });
}

// Manual Challenges (3)
export function mailManualChallengeList() {
  return api<MailManualChallengeListResponse>("/api/mail/manual-challenges", { method: "GET" });
}
export function mailManualChallengeConfirm(id: string, csrf?: string) {
  return api<{ accepted: boolean; message: string }>(`/api/mail/manual-challenges/${encodeURIComponent(id)}/confirm`, { method: "POST", csrf });
}
export function mailManualChallengeCancel(id: string, csrf?: string) {
  return api<{ accepted: boolean; message: string }>(`/api/mail/manual-challenges/${encodeURIComponent(id)}`, { method: "DELETE", csrf });
}

// --- Phase 5: Mail Accounts / Aliases / Import Registration ----------------

export interface MailAccount {
  id: string;
  domain_id: string;
  local_part: string;
  address: string;
  display_name?: string;
  password_mode: "generated" | "external" | "disabled";
  status: "active" | "disabled";
  quota_mb: number;
  is_admin: boolean;
  imap_sync_enabled: boolean;
  imap_sync_state: "idle" | "syncing" | "error" | "paused";
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

export interface AccountCreateReq {
  domain_id: string;
  local_part: string;
  display_name?: string;
  password_mode?: "generated" | "external" | "disabled";
  quota_mb?: number;
  is_admin?: boolean;
}

export interface AccountCreateResp {
  id: string;
  address: string;
  display_name?: string;
  one_time_password: string;
  created_at: string;
}

export interface AccountResetResp {
  one_time_password: string;
}

export interface AccountUpdateReq {
  display_name?: string;
  status?: string;
  password_mode?: string;
  quota_mb?: number;
  is_admin?: boolean;
  imap_sync_enabled?: boolean;
}

export interface MailAlias {
  id: string;
  domain_id: string;
  source: string;
  recipients: string[];
  mode: "alias" | "catchall" | "list" | "drop";
  list_name?: string;
  list_reply_to?: string;
  description?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface AliasUpsertReq {
  id?: string;
  domain_id: string;
  source: string;
  recipients: string[];
  mode: MailAlias["mode"];
  list_name?: string;
  list_reply_to?: string;
  description?: string;
  enabled?: boolean;
}

export interface ImportRegistration {
  id: string;
  name: string;
  data_dir: string;
  config_path?: string;
  supervisor_type: string;
  read_only: boolean;
  probe_url?: string;
  status: string;
  last_probe_at?: string;
  last_error?: string;
  version?: string;
  created_at: string;
  updated_at: string;
}

export interface ImportRegisterReq {
  name: string;
  data_dir: string;
  config_path?: string;
  supervisor_type?: string;
  probe_url?: string;
}

// ---- Accounts (8) ----
export function mailAccountCreate(r: AccountCreateReq, csrf?: string) {
  return api<AccountCreateResp>("/api/mail/accounts", { method: "POST", csrf, body: r });
}
export function mailAccountList(q: { domain_id?: string; status?: string } = {}) {
  const params = new URLSearchParams();
  if (q.domain_id) params.set("domain_id", q.domain_id);
  if (q.status) params.set("status", q.status);
  const query = params.toString();
  return api<{ items?: MailAccount[]; count?: number; active_count?: number; admin_count?: number }>(`/api/mail/accounts${query ? `?${query}` : ""}`, { method: "GET" });
}
export function mailAccountGet(id: string) {
  return api<MailAccount>(`/api/mail/accounts/${encodeURIComponent(id)}`, { method: "GET" });
}
export function mailAccountUpdate(id: string, r: AccountUpdateReq, csrf?: string) {
  return api<MailAccount>(`/api/mail/accounts/${encodeURIComponent(id)}`, { method: "PATCH", csrf, body: r });
}
export function mailAccountDelete(id: string, csrf?: string) {
  return api<{ ok: boolean }>(`/api/mail/accounts/${encodeURIComponent(id)}`, { method: "DELETE", csrf });
}
export function mailAccountResetPassword(id: string, csrf?: string) {
  return api<AccountResetResp>(`/api/mail/accounts/${encodeURIComponent(id)}/reset-password`, { method: "POST", csrf, body: {} });
}
export function mailAccountResyncImap(id: string, csrf?: string) {
  return api<void>(`/api/mail/accounts/${encodeURIComponent(id)}/resync-imap`, { method: "POST", csrf, body: {} });
}
export function mailAccountDisable(id: string, csrf?: string) {
  return api<MailAccount>(`/api/mail/accounts/${encodeURIComponent(id)}/disable`, { method: "POST", csrf, body: {} });
}

// ---- Aliases (5) ----
export function mailAliasUpsert(r: AliasUpsertReq, csrf?: string) {
  return api<MailAlias>("/api/mail/aliases", { method: "POST", csrf, body: r });
}
export function mailAliasList(q: { domain_id?: string; mode?: string; enabled?: string } = {}) {
  const params = new URLSearchParams();
  if (q.domain_id) params.set("domain_id", q.domain_id);
  if (q.mode) params.set("mode", q.mode);
  if (q.enabled !== undefined) params.set("enabled", q.enabled);
  const query = params.toString();
  return api<MailAlias[]>(`/api/mail/aliases${query ? `?${query}` : ""}`, { method: "GET" });
}
export function mailAliasGet(id: string) {
  return api<MailAlias>(`/api/mail/aliases/${encodeURIComponent(id)}`, { method: "GET" });
}
export function mailAliasUpdate(id: string, r: Partial<AliasUpsertReq>, csrf?: string) {
  return api<MailAlias>(`/api/mail/aliases/${encodeURIComponent(id)}`, { method: "PATCH", csrf, body: r });
}
export function mailAliasDelete(id: string, csrf?: string) {
  return api<void>(`/api/mail/aliases/${encodeURIComponent(id)}`, { method: "DELETE", csrf });
}

// ---- Import Registration (4) ----
export function mailImportRegister(r: ImportRegisterReq, csrf?: string) {
  return api<ImportRegistration>("/api/mail/imports", { method: "POST", csrf, body: r });
}
export function mailImportList() {
  return api<ImportRegistration[]>("/api/mail/imports", { method: "GET" });
}
export function mailImportDelete(id: string, csrf?: string) {
  return api<void>(`/api/mail/imports/${encodeURIComponent(id)}`, { method: "DELETE", csrf });
}
export function mailImportProbe(id: string, csrf?: string) {
  return api<ImportRegistration>(`/api/mail/imports/${encodeURIComponent(id)}/probe`, { method: "POST", csrf, body: {} });
}

export function auditEvents() {
  return api<{ items: AuditEvent[] }>("/api/audit/events", { method: "GET" });
}

export function eventHistory(scope: string, id: string, limit = 200) {
  const params = new URLSearchParams({ scope, id, limit: String(limit) });
  return api<{ items: EventRecord[] }>(`/api/events/history?${params.toString()}`, { method: "GET" });
}

// --- Phase 6: Delivery / Queue / Suppression / Webhook / Outbound -----------

export type DeliveryStatus = "pending" | "queued" | "sent" | "deferred" | "bounced" | "suppressed" | "dropped";
export type DeliveryDirection = "in" | "out" | "local";
export interface MailDeliveryEvent {
  id: string;
  from_domain: string;
  to_domain: string;
  message_id_hash: string;
  subject_snippet: string;
  direction: DeliveryDirection;
  smtp_code?: number;
  smtp_enhanced?: string;
  redacted_error?: string;
  status: DeliveryStatus;
  attempt_count: number;
  first_attempt_at?: string;
  last_attempt_at?: string;
  completed_at?: string;
  recipient_hash?: string;
  queue_msg_id?: number;
  from_id?: string;
  created_at: string;
}
export interface DeliveryListResp {
  items: MailDeliveryEvent[];
  count: number;
  next_cursor?: string;
}
export type QueueBucket = "hold" | "active" | "schedule" | "deferred" | "fail" | "drop";
export interface MailQueueSummary {
  hold: number;
  active: number;
  schedule: number;
  deferred: number;
  fail: number;
  drop: number;
}
export interface MailQueueItem {
  id: string;
  bucket: QueueBucket;
  envelope_from_domain?: string;
  envelope_to_hash?: string;
  status?: string;
  scheduled_at?: string;
  attempt_count: number;
  created_at: string;
}
export type QueueAction = "hold" | "unhold" | "schedule" | "fail" | "drop";
export interface MailSuppression {
  id: string;
  recipient_hash: string;
  domain_id?: string;
  reason?: string;
  smtp_code?: number;
  source?: string;
  added_at: string;
  expires_at?: string;
  active: boolean;
}
export interface SuppressionUpsertReq {
  id?: string;
  recipient_hash: string;
  domain_id?: string;
  reason?: string;
  smtp_code?: number;
  source?: string;
  expires_at?: string;
  active?: boolean;
}
export interface SuppressionImportReq {
  entries: SuppressionUpsertReq[];
}
export interface MailWebhookRegistration {
  id: string;
  name: string;
  direction: "in" | "out";
  url?: string;
  signing_alg: string;
  source_cidr: string;
  max_body_bytes: number;
  event_mask: string;
  enabled: boolean;
  created_at: string;
}
export interface WebhookRegisterReq {
  name: string;
  direction?: "in" | "out";
  url?: string;
  source_cidr?: string;
  signing_alg?: string;
  max_body_bytes?: number;
  event_mask?: string;
}
export interface WebhookRegisterResp {
  registration: MailWebhookRegistration;
  one_time_secret: string;
}
export interface MailWebhookEvent {
  id: string;
  registration_id?: string;
  direction: string;
  event_type: string;
  payload_hash: string;
  payload_size: number;
  source_addr?: string;
  hmac_valid: boolean;
  timestamp_skew_ms: number;
  status: string;
  error_reason?: string;
  created_at: string;
}
export interface OutboundRateSnapshot {
  scope: string;
  counts: Record<string, number>;
  bounce_rate_pct: number;
  thresholds: MailOutboundThreshold;
}
export interface MailOutboundThreshold {
  scope: string;
  send_1m_warn: number;
  send_1m_crit: number;
  send_1h_warn: number;
  send_1h_crit: number;
  bounce_rate_pct_warn: number;
  bounce_rate_pct_crit: number;
  updated_at?: string;
}
export interface DNSBLResult {
  ip: string;
  source: string;
  listed: boolean;
  code?: string;
  severity: "good" | "warn" | "critical";
}
export interface DNSBLProbeResp {
  results: DNSBLResult[];
  last_run_at: string;
  summary: {
    total_ips: number;
    listed_count: number;
    critical_count: number;
    warn_count: number;
  };
}

// ---- Deliveries (5) ----
export function mailDeliveryList(q: {
  direction?: DeliveryDirection;
  status?: DeliveryStatus;
  from_domain?: string;
  to_domain?: string;
  subject_contains?: string;
  limit?: number;
  cursor?: string;
} = {}) {
  const params = new URLSearchParams();
  if (q.direction) params.set("direction", q.direction);
  if (q.status) params.set("status", q.status);
  if (q.from_domain) params.set("from_domain", q.from_domain);
  if (q.to_domain) params.set("to_domain", q.to_domain);
  if (q.subject_contains) params.set("subject_contains", q.subject_contains);
  if (q.limit) params.set("limit", String(q.limit));
  if (q.cursor) params.set("cursor", q.cursor);
  const query = params.toString();
  return api<DeliveryListResp>(`/api/mail/deliveries${query ? `?${query}` : ""}`, { method: "GET" });
}
export function mailDeliveryGet(id: string) {
  return api<MailDeliveryEvent>(`/api/mail/deliveries/${encodeURIComponent(id)}`, { method: "GET" });
}
export function mailDeliveryRetry(id: string, csrf?: string) {
  return api<{ requeued: boolean; id: string }>(`/api/mail/deliveries/${encodeURIComponent(id)}/retry`, { method: "POST", csrf, body: {} });
}
export function mailDeliveryDelete(id: string, csrf?: string) {
  return api<void>(`/api/mail/deliveries/${encodeURIComponent(id)}`, { method: "DELETE", csrf });
}
export function mailDeliveryPrune({ days }: { days: number }, csrf?: string) {
  return api<{ pruned_count: number }>("/api/mail/deliveries/prune", { method: "POST", csrf, body: { days } });
}

// ---- Queue (3) ----
export function mailQueueSummary() {
  return api<MailQueueSummary>("/api/mail/queue/summary", { method: "GET" });
}
export function mailQueueItems(q: { bucket?: QueueBucket; limit?: number; cursor?: string } = {}) {
  const params = new URLSearchParams();
  if (q.bucket) params.set("bucket", q.bucket);
  if (q.limit) params.set("limit", String(q.limit));
  if (q.cursor) params.set("cursor", q.cursor);
  const query = params.toString();
  return api<{ items: MailQueueItem[]; next_cursor?: string }>(`/api/mail/queue/items${query ? `?${query}` : ""}`, { method: "GET" });
}
export function mailQueueAction(action: QueueAction, ids: string[], csrf?: string) {
  return api<{ updated_count: number }>(`/api/mail/queue/action/${encodeURIComponent(action)}`, { method: "POST", csrf, body: { ids } });
}

// ---- Suppression (5) ----
export function mailSuppressionList(q: {
  active?: string;
  reason?: string;
  domain_id?: string;
  recipient_prefix?: string;
  limit?: number;
  cursor?: string;
} = {}) {
  const params = new URLSearchParams();
  if (q.active !== undefined) params.set("active", q.active);
  if (q.reason) params.set("reason", q.reason);
  if (q.domain_id) params.set("domain_id", q.domain_id);
  if (q.recipient_prefix) params.set("recipient_prefix", q.recipient_prefix);
  if (q.limit) params.set("limit", String(q.limit));
  if (q.cursor) params.set("cursor", q.cursor);
  const query = params.toString();
  return api<{ items: MailSuppression[]; next_cursor?: string; count: number }>(`/api/mail/suppressions${query ? `?${query}` : ""}`, { method: "GET" });
}
export function mailSuppressionUpsert(r: SuppressionUpsertReq, csrf?: string) {
  return api<MailSuppression>("/api/mail/suppressions", { method: "POST", csrf, body: r });
}
export function mailSuppressionDelete(id: string, csrf?: string) {
  return api<void>(`/api/mail/suppressions/${encodeURIComponent(id)}`, { method: "DELETE", csrf });
}
export function mailSuppressionImport(body: SuppressionImportReq, csrf?: string) {
  return api<{ imported_count: number }>("/api/mail/suppressions/import", { method: "POST", csrf, body });
}
export function mailSuppressionPrune(csrf?: string) {
  return api<{ pruned_count: number }>("/api/mail/suppressions/prune-expired", { method: "POST", csrf, body: {} });
}

// ---- Webhooks (5) ----
export function mailWebhookRegister(req: WebhookRegisterReq, csrf?: string) {
  return api<WebhookRegisterResp>("/api/mail/webhooks", { method: "POST", csrf, body: req });
}
export function mailWebhookList() {
  return api<MailWebhookRegistration[] | { items?: MailWebhookRegistration[] }>("/api/mail/webhooks", { method: "GET" }).then(unwrapItems);
}
export function mailWebhookDelete(id: string, csrf?: string) {
  return api<void>(`/api/mail/webhooks/${encodeURIComponent(id)}`, { method: "DELETE", csrf });
}
export function mailWebhookRotateSecret(id: string, csrf?: string) {
  return api<{ one_time_secret: string }>(`/api/mail/webhooks/${encodeURIComponent(id)}/rotate-secret`, { method: "POST", csrf, body: {} });
}
export function mailWebhookEvents(limit = 50) {
  return api<MailWebhookEvent[] | { items?: MailWebhookEvent[] }>(`/api/mail/webhooks/events?limit=${limit}`, { method: "GET" }).then(unwrapItems);
}

// ---- Outbound (3) ----
export function mailOutboundRate(scope = "global") {
  return api<OutboundRateSnapshot>(`/api/mail/outbound/rate?scope=${encodeURIComponent(scope)}`, { method: "GET" });
}
export function mailOutboundThresholdsList() {
  return api<MailOutboundThreshold[] | { items?: MailOutboundThreshold[] }>("/api/mail/outbound/thresholds", { method: "GET" }).then(unwrapItems);
}
export function mailOutboundThresholdsUpdate(scope: string, patch: Partial<MailOutboundThreshold>, csrf?: string) {
  return api<MailOutboundThreshold>(`/api/mail/outbound/thresholds/${encodeURIComponent(scope)}`, { method: "PATCH", csrf, body: patch });
}

// ---- DNSBL / Reputation (1) ----
export function mailDNSBLProbe() {
  return api<DNSBLProbeResp>("/api/mail/reputation/dnsbl", { method: "GET" });
}

// ============================================================================
// ---- Phase 7: Mailbox (Folders / Messages / Search / IMAP Sync / Compose) --
// ============================================================================

// ---- Types ----

export interface MailFolder {
  id: string;
  account_id: string;
  name: string;
  role?: "inbox" | "sent" | "drafts" | "trash" | "junk" | "archive" | "";
  parent_id?: string;
  total_messages?: number;
  unseen_messages?: number;
  message_count?: number;
  last_synced_at?: string;
  uid_next?: number;
  uid_validity?: number;
  subscribed?: boolean;
  readonly?: boolean;
  tags?: string[];
  created_at?: string;
  updated_at?: string;
}

export interface MailMessagePart {
  id: string;
  folder_id: string;
  message_id: string;
  part_id?: string;
  content_type: string;
  charset?: string;
  filename?: string;
  content_id?: string;
  disposition?: string;
  size_bytes?: number;
  body_cache_path?: string;
  body_hash_sha256?: string;
  decoded_text?: string;
  is_attachment?: boolean;
  is_inline?: boolean;
  created_at?: string;
  updated_at?: string;
  content_transfer_encoding?: string;
}

export interface AttachmentInfo {
  index: number;
  part_id?: string;
  filename: string;
  content_type: string;
  size_bytes: number;
  stored: boolean;
}

export interface MailMessage {
  id: string;
  message_id: string;
  folder_id: string;
  subject?: string;
  from?: string;
  to?: string[];
  cc?: string[];
  bcc?: string[];
  reply_to?: string[];
  date?: string;
  message_body_text?: string;
  message_body_html?: string;
  attachments_count?: number;
  seen?: boolean;
  flagged?: boolean;
  uid?: number;
  modseq?: number;
  envelope?: Record<string, unknown>;
  size_bytes?: number;
  parts?: MailMessagePart[];
  attachments?: AttachmentInfo[];
  created_at?: string;
  updated_at?: string;
}

export interface MailMessageListResp {
  items: MailMessagePart[];
  total: number;
  next_cursor: string;
}

export interface MailSearchQuery {
  query: string;
  account_ids?: string[];
  scope?: "one" | "all" | "attachments" | "has_attachment";
  limit?: number;
  offset?: number;
}

export interface MailSearchResult {
  id: string;
  message_id: string;
  folder_id: string;
  account_id: string;
  subject: string;
  from: string;
  to: string;
  date: string;
  snippet: string;
  size_bytes: number;
  rank?: number;
}

export interface MailSearchResp {
  items: MailSearchResult[];
  total: number;
  duration_ms: number;
  account_ids: string[];
  query: string;
}

export interface MailIndexHealth {
  id?: string;
  account_id: string;
  status: "ok" | "rebuilding" | "stale" | "error" | "pending";
  indexed_messages: number;
  total_messages: number;
  last_indexed_at?: string;
  indexed_folder_ids?: string[];
  missing_folder_ids?: string[];
  error_msg?: string;
  created_at?: string;
  updated_at?: string;
}

export interface ComposeSendReq {
  account_id: string;
  from: string;
  to: string[];
  cc?: string[];
  bcc?: string[];
  subject: string;
  body_text?: string;
  body_html?: string;
  reply_to_message_id?: string;
  forward_message_id?: string;
  attachments?: AttachmentInfo[];
  tags?: string[];
}

export interface ComposeSendResp {
  job_id: string;
  from: string;
  to: string[];
  queued_at: string;
  saved_to_sent?: boolean;
  sent_folder_id?: string;
  draft_id?: string;
  preview_url?: string;
}

export interface DraftSaveReq {
  draft_id?: string;
  account_id: string;
  from?: string;
  to?: string[];
  cc?: string[];
  bcc?: string[];
  subject?: string;
  body_text?: string;
  body_html?: string;
  reply_to_message_id?: string;
  forward_message_id?: string;
  attachments?: AttachmentInfo[];
  tags?: string[];
}

export interface DraftSaveResp {
  draft_id: string;
  saved_at: string;
  folder_id?: string;
  from?: string;
  to?: string[];
  cc?: string[];
  bcc?: string[];
  subject?: string;
  body_text?: string;
}

// ---- RPC wrappers (Folders = 3) ----

export function mailFolderList(accountId = "all") {
  return api<{ items: MailFolder[] }>(`/api/mail/accounts/${encodeURIComponent(accountId)}/folders`, { method: "GET" });
}
export function mailFolderUpsert(accountId: string, patch: Partial<MailFolder>, csrf?: string) {
  return api<MailFolder>(`/api/mail/accounts/${encodeURIComponent(accountId)}/folders`, { method: "POST", csrf, body: patch });
}
export function mailFolderDelete(folderId: string, csrf?: string) {
  return api<{ ok: boolean }>(`/api/mail/folders/${encodeURIComponent(folderId)}`, { method: "DELETE", csrf });
}

// ---- Messages (7) ----

export function mailMessageList(folderId: string, params?: { limit?: number; cursor?: string; unseen_only?: 0 | 1 }) {
  const qs = new URLSearchParams();
  if (params?.limit) qs.set("limit", String(params.limit));
  if (params?.cursor) qs.set("cursor", params.cursor);
  if (params?.unseen_only === 1) qs.set("unseen_only", "1");
  const suffix = qs.toString() ? `?${qs.toString()}` : "";
  return api<MailMessageListResp>(`/api/mail/folders/${encodeURIComponent(folderId)}/messages${suffix}`, { method: "GET" });
}
export function mailMessageGet(messageId: string) {
  return api<MailMessage>(`/api/mail/messages/${encodeURIComponent(messageId)}`, { method: "GET" });
}
export function mailMessageDelete(messageId: string, csrf?: string) {
  return api<{ ok: boolean }>(`/api/mail/messages/${encodeURIComponent(messageId)}`, { method: "DELETE", csrf });
}
export function mailMessageMove(messageId: string, destFolderId: string, csrf?: string) {
  return api<{ ok: boolean }>(`/api/mail/messages/${encodeURIComponent(messageId)}/move`, {
    method: "POST", csrf, body: { dest_folder_id: destFolderId },
  });
}
export function mailMessageUpdateFlags(messageId: string, body: { add?: string[]; remove?: string[] }, csrf?: string) {
  return api<{ ok: boolean }>(`/api/mail/messages/${encodeURIComponent(messageId)}/flags`, { method: "PATCH", csrf, body });
}
export function mailMessageRaw(messageId: string) {
  return fetch(`/api/mail/messages/${encodeURIComponent(messageId)}/raw`, {
    method: "GET",
    credentials: "same-origin",
    headers: { "Accept": "text/plain; charset=utf-8" },
  }).then(r => {
    if (!r.ok) throw new Error(`原文加载失败 (${r.status})`);
    return r.text();
  });
}
export function mailMessageAttachment(messageId: string, index: number) {
  return api<AttachmentInfo>(`/api/mail/messages/${encodeURIComponent(messageId)}/attachments/${index}`, { method: "GET" });
}

// ---- Search (1) ----

export function mailMessageSearch(accountId: string, body: MailSearchQuery, csrf?: string) {
  return api<MailSearchResp>(`/api/mail/accounts/${encodeURIComponent(accountId)}/search`, { method: "POST", csrf, body });
}

// ---- IMAP Index + Sync (7) ----

export function mailIndexHealthGet(accountId: string) {
  return api<MailIndexHealth>(`/api/mail/accounts/${encodeURIComponent(accountId)}/index/health`, { method: "GET" });
}
export function mailIndexHealthList() {
  return api<{ items: MailIndexHealth[] }>("/api/mail/index/health", { method: "GET" });
}
export function mailIndexHealthReset(accountId: string, csrf?: string) {
  return api<MailIndexHealth>(`/api/mail/accounts/${encodeURIComponent(accountId)}/index/reset`, { method: "POST", csrf, body: {} });
}
export function mailImapSyncStart(accountId: string, csrf?: string) {
  return api<{ ok: boolean; state: string }>(`/api/mail/accounts/${encodeURIComponent(accountId)}/sync/start`, { method: "POST", csrf, body: {} });
}
export function mailImapSyncPause(accountId: string, csrf?: string) {
  return api<{ ok: boolean; state: string }>(`/api/mail/accounts/${encodeURIComponent(accountId)}/sync/pause`, { method: "POST", csrf, body: {} });
}
export function mailImapSyncResume(accountId: string, csrf?: string) {
  return api<{ ok: boolean; state: string }>(`/api/mail/accounts/${encodeURIComponent(accountId)}/sync/resume`, { method: "POST", csrf, body: {} });
}
export function mailImapSyncReset(accountId: string, csrf?: string) {
  return api<{ ok: boolean; state: string }>(`/api/mail/accounts/${encodeURIComponent(accountId)}/sync/reset`, { method: "POST", csrf, body: {} });
}

// ---- Compose + Drafts (3) ----

export function mailComposeSend(req: ComposeSendReq, csrf?: string) {
  return api<ComposeSendResp>("/api/mail/compose/send", { method: "POST", csrf, body: req });
}
export function mailDraftSave(req: DraftSaveReq, csrf?: string) {
  return api<DraftSaveResp>("/api/mail/drafts", { method: "POST", csrf, body: req });
}
export function mailDraftDelete(draftId: string, csrf?: string) {
  return api<{ ok: boolean }>(`/api/mail/drafts/${encodeURIComponent(draftId)}`, { method: "DELETE", csrf });
}

// ============================================================================
// ---- Phase 8: Logs / Backup / Retention / Danger Zone ----------------------
// ============================================================================

// ---- Logs Types ----

export interface LogFileInfo {
  path: string;
  size_bytes: number;
  modified_at: string;
  lines_estimated: number;
}

export interface LogsTailReq {
  path?: string;
  limit?: number;
  search?: string;
  severity?: "debug" | "info" | "warn" | "error" | "critical" | "";
}

export interface LogsTailResp {
  lines: string[];
  truncated: boolean;
  scanned_bytes: number;
  matched_count: number;
}

export interface LogsStreamReq {
  path?: string;
  sample_rate?: "high" | "normal" | "low";
}

export interface RedactionSummaryResp {
  rules_count: number;
  descriptions: { pattern: string; description: string }[];
}

// ---- Backup Types ----

export interface MailBackup {
  id: string;
  scope: "config" | "data_full";
  state?: string;
  file_name?: string;
  file_path?: string;
  size_bytes: number;
  checksum_sha256?: string;
  contains_config?: boolean;
  contains_data?: boolean;
  note?: string;
  created_at_iso: string;
  done_at_iso?: string;
  started_at_iso?: string;
  retention_days?: number;
  expires_at_iso?: string;
  schedule_id?: string;
}

export interface MailBackupCreateReq {
  scope: "config" | "data_full";
  note?: string;
}

export interface MailBackupSchedule {
  id: string;
  name?: string;
  scope: string;
  enabled: boolean;
  cron_expr: string;
  retention_days: number;
  last_run_at_iso?: string;
  last_backup_id?: string;
  last_error?: string;
  next_run_at_iso?: string;
  created_at_iso?: string;
  updated_at_iso?: string;
}

// ---- Retention Types ----

export interface MailRetentionRule {
  id: string;
  name?: string;
  rule_kind?: string;
  target_kind: "delivery_events" | "health_checks" | "webhook_events" | "index_messages" | "expired_backups";
  days: number;
  keep_min_count?: number;
  enabled: boolean;
  description?: string;
  last_run_at_iso?: string;
  last_pruned_count?: number;
  last_error?: string;
  created_at_iso?: string;
  updated_at_iso?: string;
}

export interface RetentionApplyResp {
  applied_at_iso: string;
  deleted_by_target: Record<string, number>;
  total_deleted: number;
}

// ---- Danger Zone Types ----

export interface DangerGenerateCodeResp {
  code: string;
  generated_at_iso: string;
  expires_at_iso: string;
  countdown_started_iso: string;
}

export interface DangerDeleteConfirmation {
  account_name: string;
  checkboxes: boolean[];
  verification_code: string;
  countdown_elapsed_seconds: number;
}

export interface DangerDeleteResp {
  deleted_scope: string;
  backups_kept: boolean;
  warning: string;
}

export interface DangerRequirementsResp {
  required_checkboxes_3: string[];
  required_elapsed_seconds: number;
  code_length: number;
  note: string;
}

// ---- Logs RPC wrappers ----

export function mailLogsList() {
  return api<LogFileInfo[] | { items?: LogFileInfo[] }>("/api/mail/logs/files", { method: "GET" }).then(unwrapItems);
}

export function mailLogsTail(req: LogsTailReq) {
  const params = new URLSearchParams();
  if (req.path) params.set("path", req.path);
  if (req.limit) params.set("limit", String(req.limit));
  if (req.search) params.set("search", req.search);
  if (req.severity) params.set("severity", req.severity);
  const query = params.toString();
  return api<LogsTailResp>(`/api/mail/logs/tail${query ? `?${query}` : ""}`, { method: "GET" });
}

/**
 * Opens an SSE-style stream for mail logs. Uses fetch + ReadableStream so that
 * auth CSRF headers can be attached (plain EventSource can't carry headers).
 * Parses text/event-stream frames and fires callbacks for `line`, `skipped`
 * and heartbeat events. Returns a close() function that aborts the controller.
 */
export function openMailLogsStream(
  req: LogsStreamReq,
  onLine: (line: string) => void,
  onSkipped: (count: number) => void,
  onHeartbeat?: () => void,
): { close: () => void } {
  const controller = new AbortController();
  const params = new URLSearchParams();
  if (req.path) params.set("path", req.path);
  if (req.sample_rate) params.set("sample_rate", req.sample_rate);
  const query = params.toString();
  const url = `/api/mail/logs/stream${query ? `?${query}` : ""}`;
  const headers: Record<string, string> = {
    Accept: "text/event-stream",
    "X-CSRF-Token": readCookie("pl_csrf"),
  };

  void (async () => {
    try {
      const response = await fetch(url, {
        method: "GET",
        credentials: "same-origin",
        headers,
        signal: controller.signal,
      });
      if (!response.ok || !response.body) return;
      const reader = response.body.getReader();
      const decoder = new TextDecoder("utf-8");
      let buffer = "";
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        let idx: number;
        // SSE frames are separated by blank lines (\n\n or \r\n\r\n).
        while ((idx = buffer.indexOf("\n\n")) >= 0 || (idx = buffer.indexOf("\r\n\r\n")) >= 0) {
          const frame = buffer.slice(0, idx);
          buffer = buffer.slice(idx + (frame.endsWith("\r") ? 4 : 2));
          parseEventFrame(frame, onLine, onSkipped, onHeartbeat);
        }
      }
    } catch {
      // Abort or network error — silently close.
    }
  })();

  return { close: () => controller.abort() };
}

function parseEventFrame(
  frame: string,
  onLine: (line: string) => void,
  onSkipped: (count: number) => void,
  onHeartbeat?: () => void,
) {
  const lines = frame.split(/\r?\n/);
  let event = "message";
  let data = "";
  for (const raw of lines) {
    if (!raw) continue;
    const colon = raw.indexOf(":");
    const key = colon >= 0 ? raw.slice(0, colon).trim() : raw.trim();
    const value = colon >= 0 ? raw.slice(colon + 1).replace(/^ /, "") : "";
    if (key === "event") event = value;
    else if (key === "data") data = value;
  }
  if (event === "heartbeat" || (event === "message" && data === ":keep-alive")) {
    onHeartbeat?.();
    return;
  }
  if (event === "skipped") {
    const n = parseInt(data, 10) || 0;
    if (n > 0) onSkipped(n);
    return;
  }
  if (event === "line" || event === "message") {
    if (data) onLine(data);
  }
}

export function mailLogsRedactionSummary() {
  return api<RedactionSummaryResp>("/api/mail/logs/redaction", { method: "GET" });
}

// ---- Backups RPC wrappers ----

export function mailBackupList(scope?: string, limit = 20, offset = 0) {
  const params = new URLSearchParams();
  if (scope) params.set("scope", scope);
  params.set("limit", String(limit));
  params.set("offset", String(offset));
  return api<{ items: MailBackup[]; total: number }>(`/api/mail/backups?${params.toString()}`, { method: "GET" });
}

export function mailBackupCreate(req: MailBackupCreateReq, csrf?: string) {
  return api<MailBackup>("/api/mail/backups", { method: "POST", csrf, body: req });
}

export function mailBackupDownloadUrl(id: string): string {
  return `/api/mail/backups/${encodeURIComponent(id)}`;
}

export function mailBackupDelete(id: string, csrf?: string) {
  return api<void>(`/api/mail/backups/${encodeURIComponent(id)}`, { method: "DELETE", csrf });
}

export function mailBackupScheduleList() {
  return api<MailBackupSchedule[] | { items?: MailBackupSchedule[] }>("/api/mail/backups/schedules", { method: "GET" }).then(unwrapItems);
}

export function mailBackupScheduleUpsert(s: Partial<MailBackupSchedule>, csrf?: string) {
  return api<MailBackupSchedule>("/api/mail/backups/schedules", { method: "POST", csrf, body: s });
}

export function mailBackupScheduleDelete(id: string, csrf?: string) {
  return api<void>(`/api/mail/backups/schedules/${encodeURIComponent(id)}`, { method: "DELETE", csrf });
}

// ---- Retention RPC wrappers ----

export function mailRetentionRuleList() {
  return api<MailRetentionRule[] | { items?: MailRetentionRule[] }>("/api/mail/retention/rules", { method: "GET" }).then(unwrapItems);
}

export function mailRetentionRuleUpsert(r: Partial<MailRetentionRule>, csrf?: string) {
  return api<MailRetentionRule>("/api/mail/retention/rules", { method: "POST", csrf, body: r });
}

export function mailRetentionRuleDelete(id: string, csrf?: string) {
  return api<void>(`/api/mail/retention/rules/${encodeURIComponent(id)}`, { method: "DELETE", csrf });
}

export function mailRetentionApplyNow(csrf?: string) {
  return api<RetentionApplyResp>("/api/mail/retention/apply-now", { method: "POST", csrf, body: {} });
}

// ---- Danger Zone RPC wrappers ----

export function mailDangerGenerateCode(csrf?: string) {
  return api<DangerGenerateCodeResp>("/api/mail/danger/generate-code", { method: "POST", csrf, body: {} });
}

export function mailDangerHardDelete(conf: DangerDeleteConfirmation, csrf?: string) {
  return api<DangerDeleteResp>("/api/mail/danger/hard-delete", { method: "POST", csrf, body: conf });
}

export function mailDangerRequirements() {
  return api<DangerRequirementsResp>("/api/mail/danger/requirements", { method: "GET" });
}
