package mail

// Event scope used for every event and SSE stream published by the Mail
// module.  Keep in sync with the event-type strings below so
// events.Hub.Subscribe(ctx, EventScope, "") can wildcard the whole module.
const EventScope = "mail"

// EventType strings published through the events.Hub with scope=EventScope.
// UI audit labels (web/src/domain/labels.ts auditLabels) are keyed on these.
const (
	// Lifecycle / installation.
	EventTypeBinaryDetected    = "mail.binary.detected"
	EventTypeBinaryInstalled   = "mail.binary.installed"
	EventTypeBinaryUninstalled = "mail.binary.uninstalled"
	EventTypeSetupInitialized  = "mail.setup.initialized"
	EventTypeSetupImported     = "mail.setup.imported"

	// Runtime control.
	EventTypeRuntimeStartRequested = "mail.runtime.start_requested"
	EventTypeRuntimeStarted        = "mail.runtime.started"
	EventTypeRuntimeStartFailed    = "mail.runtime.start_failed"
	EventTypeRuntimeStopRequested  = "mail.runtime.stop_requested"
	EventTypeRuntimeStopped        = "mail.runtime.stopped"
	EventTypeRuntimeRestarted      = "mail.runtime.restarted"
	EventTypeRuntimeCrashed        = "mail.runtime.crashed"
	EventTypeRuntimeAdopted        = "mail.runtime.adopted"
	EventTypeRuntimeProbeResult    = "mail.runtime.probe_result"

	// Config application.
	EventTypeConfigApplyStep     = "mail.config.apply_step"
	EventTypeConfigApplyComplete = "mail.config.apply_complete"
	EventTypeConfigApplyFailed   = "mail.config.apply_failed"
	EventTypeConfigRolledBack    = "mail.config.rolled_back"
	EventTypeConfigDrifted       = "mail.config.drifted"
	EventTypeConfigDriftResolved = "mail.config.drift_resolved"

	// Domains & DNS.
	EventTypeDomainCreated  = "mail.domain.created"
	EventTypeDomainUpdated  = "mail.domain.updated"
	EventTypeDomainDeleted  = "mail.domain.deleted"
	EventTypeDomainDisabled = "mail.domain.disabled"
	EventTypeDomainEnabled  = "mail.domain.enabled"
	EventTypeDomainDNSCheck = "mail.domain.dns_check"

	// Certificates & ACME.
	EventTypeCertIssued            = "mail.cert.issued"
	EventTypeCertRenewed           = "mail.cert.renewed"
	EventTypeCertRevoked           = "mail.cert.revoked"
	EventTypeCertRenewalFailed     = "mail.cert.renewal_failed"
	EventTypeCertDNS01AwaitManual  = "mail.cert.dns01_await_manual"
	EventTypeCertDNS01Confirmed    = "mail.cert.dns01_confirmed"
	EventTypeCertProviderUpdated   = "mail.cert.provider_updated"
	EventTypeCertProviderRotated   = "mail.cert.provider_rotated"

	// Accounts, addresses, aliases.
	EventTypeAccountCreated         = "mail.account.created"
	EventTypeAccountUpdated         = "mail.account.updated"
	EventTypeAccountDeleted         = "mail.account.deleted"
	EventTypeAccountPasswordChanged = "mail.account.password_changed"
	EventTypeAliasCreated           = "mail.alias.created"
	EventTypeAliasUpdated           = "mail.alias.updated"
	EventTypeAliasDeleted           = "mail.alias.deleted"
	EventTypeAddressCreated         = "mail.address.created"
	EventTypeAddressDeleted         = "mail.address.deleted"

	// Queue, delivery, webhooks, reputation.
	EventTypeDeliverySucceeded    = "mail.delivery.succeeded"
	EventTypeDeliveryFailed       = "mail.delivery.failed"
	EventTypeDeliveryDeferred     = "mail.delivery.deferred"
	EventTypeQueueAction          = "mail.queue.action"
	EventTypeSuppressionUpdated   = "mail.suppression.updated"
	EventTypeWebhookInReceived    = "mail.webhook.in.received"
	EventTypeWebhookInRejected    = "mail.webhook.in.rejected"
	EventTypeOutboundRateWarn     = "mail.outbound.rate_warn"
	EventTypeReputationDNSBLHit   = "mail.reputation.dnsbl_hit"
	EventTypeReputationDNSBLClear = "mail.reputation.dnsbl_clear"

	// IMAP sync & search.
	EventTypeSyncStarted      = "mail.sync.started"
	EventTypeSyncProgress     = "mail.sync.progress"
	EventTypeSyncCompleted    = "mail.sync.completed"
	EventTypeSyncError        = "mail.sync.error"
	EventTypeSyncPaused       = "mail.sync.paused"
	EventTypeSyncResized      = "mail.sync.resized"
	EventTypeSearchIndexError = "mail.search.index_error"
	// ---- Phase 7: Folders / Messages / Search / Index / Compose ----
	EventTypeFolderListed      = "mail.folder.listed"
	EventTypeFolderCreated     = "mail.folder.created"
	EventTypeFolderUpdated     = "mail.folder.updated"
	EventTypeFolderDeleted     = "mail.folder.deleted"
	EventTypeMessageListed     = "mail.message.listed"
	EventTypeMessageViewed     = "mail.message.viewed"
	EventTypeMessageMoved      = "mail.message.moved"
	EventTypeMessageFlagsUpd   = "mail.message.flags_updated"
	EventTypeMessageDeleted    = "mail.message.deleted"
	EventTypeMessageRawFetched = "mail.message.raw_fetched"
	EventTypeSearchExecuted    = "mail.search.executed"
	EventTypeIndexHealthViewed = "mail.index.health_viewed"
	EventTypeIndexResetReq     = "mail.index.reset_requested"
	EventTypeImapSyncStarted   = "mail.imap_sync.started"
	EventTypeImapSyncPaused    = "mail.imap_sync.paused"
	EventTypeImapSyncResumed   = "mail.imap_sync.resumed"
	EventTypeImapSyncReset     = "mail.imap_sync.reset"
	EventTypeComposeQueued     = "mail.compose.queued"
	EventTypeDraftSaved        = "mail.draft.saved"
	EventTypeDraftDeleted      = "mail.draft.deleted"

	// Operator-visible high-risk actions (audited HIGH severity).
	EventTypeSettingsUpdated  = "mail.settings.updated"
	EventTypeRetentionPruned  = "mail.retention.pruned"
	EventTypeBackupStarted    = "mail.backup.started"
	EventTypeBackupCompleted  = "mail.backup.completed"
	EventTypeBackupFailed     = "mail.backup.failed"
	EventTypeHardDeleteWiped  = "mail.hard_delete.wiped"
	EventTypeImportModeLocked = "mail.import_mode.locked"
)

// ID prefixes used for mail-module objects via ids.New(prefix).
// The prefixes are deliberately short and stable; changing them would
// invalidate every stored row.
const (
	IDPrefixDomain        = "dom"
	IDPrefixAccount       = "acc"
	IDPrefixAddress       = "addr"
	IDPrefixAlias         = "als"
	IDPrefixAliasRecipient = "alr"
	IDPrefixCertificate   = "cert"
	IDPrefixDNSProvider   = "dnsp"
	IDPrefixMessage       = "msg"
	IDPrefixFolder        = "fld"
	IDPrefixDelivery      = "dlv"
	IDPrefixHealthCheck   = "mca"
	IDPrefixQueueEntry    = "mqe"
	IDPrefixSuppression   = "sup"
	IDPrefixWebhook       = "mwh"
	IDPrefixBackup        = "bck"
	IDPrefixImport        = "mim"
	// ---- Phase 6 (webhook / delivery / queue / rate / DNSBL) ----
	IDPrefixDeliveryEvent  = "dle"
	IDPrefixQueueItem      = "qit"
	IDPrefixOutboundRate   = "obr"
	IDPrefixWebhookEvent   = "whe"
	// ---- Phase 7 (mailbox / folders / FTS) ----
	IDPrefixMessagePart    = "msgp"
	IDPrefixSearchResult   = "msr"
	IDPrefixImapSync       = "ims"
	// ---- Phase 8 (logs / backup / retention / danger) ----
	IDPrefixRetentionRule  = "rlr"
	IDPrefixSchedule       = "sch"
	IDPrefixBackupSchedule = "mbs"
	IDPrefixRetentionRuleAlt = "mrt"
	IDPrefixDangerCode     = "mdc"
)
