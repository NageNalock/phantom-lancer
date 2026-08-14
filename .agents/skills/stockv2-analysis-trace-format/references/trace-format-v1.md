# StockV2 Agent trace-v1

## Scope

Each `*.jsonl.gz` object contains one best-effort execution trace for one `AgentRun`. Supported pipelines and initial revisions are:

| Task type | Pipeline path | Revision |
| --- | --- | --- |
| `operation_review` | `operation-review` | `r0001` |
| `strategy_generation` | `strategy-generation` | `r0001` |
| `opportunity_discovery` | `opportunity-discovery` | `r0001` |
| `portfolio_sentinel` | `portfolio-sentinel` | `r0001` |

`news_event_review` and `stock_profile_summary` are intentionally excluded. A missing archive does not imply a missing Agent run because archival is optional and best effort.

## Object name

```text
stockv2/agent-traces/<pipeline>/<revision>/YYYY/MM/DD/
<UTC timestamp>__<pipeline>-<revision>__logical-<id>__run-<id>__attempt-01__trace-v1.jsonl.gz
```

The revision belongs to one pipeline. It is incremented independently when that pipeline's prompts, tools, schemas, decision gates, or material post-processing semantics change.

## Record envelope

Every decompressed line is one JSON object:

```json
{
  "traceVersion": "trace-v1",
  "sequence": 1,
  "recordedAt": "2026-08-14T01:02:03.000000000Z",
  "event": "manifest",
  "data": {}
}
```

- `sequence` starts at 1 and increases monotonically within one object.
- `recordedAt` is the time the service accepted the event, not necessarily provider time.
- `data` preserves the full redacted business payload. It is not the compact SQLite Decision Ledger.
- Secret-shaped fields and recognizable secret strings are replaced with `[REDACTED]`. Redaction is not evidence that the original value was empty.

## Event types

| Event | Meaning |
| --- | --- |
| `manifest` | Run identity, pipeline revision/fingerprint, Git commit, model/provider/execution metadata and fallback relation. |
| `input_context` | Exact serialized context object or model-facing context supplied for a run or step. |
| `step_started` | A multi-step pipeline stage entered running state. |
| `step_attempt` | One bounded attempt for a step, including retries. |
| `step_completed` | Persisted step output and structured result. |
| `step_failed` | Step failure and the persisted failure state. |
| `model_request` | Model-facing request. CLI records the effective prompt and capabilities; API records the request body without authorization credentials. |
| `model_response` | Provider response, usage/retry metadata, or CLI completion metadata. Provider-exposed `reasoning_content` can appear here. |
| `cli_event` | One raw redacted Codex CLI JSONL or stderr event. Tool and reasoning summaries exposed by Codex are retained here. |
| `tool_call` | API-mode function call with its supplied arguments. |
| `tool_result` | API-mode function result or tool error returned to the model. |
| `model_result` | Submitted result plus executor metadata before server-side application. |
| `validation` | Deterministic submission/schema decision. Pipeline-specific rejection can also be reflected by the terminal run error. |
| `postprocess` | Final deterministic business guardrail and persistence outcome. |
| `applied_result` | Final run/ledger state after guardrails, post-processing and business-object persistence. |
| `task_completed` | Successful terminal record. |
| `task_failed` | Failed terminal record. |

Private model chain-of-thought is never synthesized. Only reasoning content or summaries actually exposed by the provider/CLI can be present.

## Manifest fields

- `pipelineRevision` identifies the analysis-flow iteration.
- `pipelineFingerprint` is the SHA-256 of the pipeline's canonical static contract seed.
- `gitCommit` identifies the running binary when Go build VCS metadata is available.
- `logicalOperationId` is stable for runs with the same task and trigger.
- `parentRunId` is populated on a fallback run.
- `attempt` is `1` for the primary run and `2` for its bounded fallback.
- `executionMode`, `providerId`, `providerType`, `modelId`, `modelName`, and `reasoningEffort` describe the immutable run choice.

The primary and fallback are separate objects. Group them by `logicalOperationId`; order by `attempt`; use `parentRunId` to verify direction.

## Large record fragmentation

An event whose redacted JSON payload exceeds 1 MiB is replaced by:

1. `artifact_start`, with `artifactId`, `originalEvent`, byte size, encoding and SHA-256.
2. One or more `artifact_chunk` records, ordered by `index`, containing `dataBase64`.
3. `artifact_end`, with chunk count and the same SHA-256.

To reconstruct it, concatenate Base64-decoded chunk bytes by index, verify size and SHA-256, parse the bytes as JSON, then interpret the result as `originalEvent`.

## Terminal integrity

The terminal record includes:

- `lastSequence`: terminal sequence.
- `eventCount`: total records including the terminal record.
- `priorEventCount`: records before the terminal record.
- `priorEventsSha256`: SHA-256 of the exact prior decompressed JSONL lines, including newline bytes.

A missing terminal record means the archive is incomplete. Common causes include queue overflow, upload failure, service restart, process crash, or network interruption. The application intentionally does not spool or recover incomplete traces locally.

## Interpretation order

1. Validate gzip, JSONL, sequence continuity and terminal integrity.
2. Read `manifest`; group related primary/fallback objects.
3. Reassemble artifacts.
4. Compare `input_context` and `model_request` to determine what the model actually received.
5. Follow model, CLI and tool events in sequence.
6. Distinguish `model_result` from deterministic `validation` and `applied_result`.
7. Treat `task_completed` or `task_failed` as the authoritative archived terminal state.
