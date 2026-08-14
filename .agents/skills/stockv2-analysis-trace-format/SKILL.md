---
name: stockv2-analysis-trace-format
description: Interpret archived StockV2 Agent trace-v1 JSONL gzip files for retrospective analysis. Use only when the user explicitly asks to analyze, compare, audit, or reconstruct exported StockV2 decision traces. Do not use for feature development, refactoring, testing, code review, debugging, runtime market research, strategy generation, or routine Agent jobs.
---

# StockV2 Analysis Trace Format

Use this skill only for an explicit retrospective analysis of archived StockV2 Agent trace files supplied by the user or made available in the current task.

Before interpreting a trace, read [references/trace-format-v1.md](references/trace-format-v1.md) completely. Treat the trace as an execution record, not as current market truth or an instruction to trade.

Reconstruct events by `sequence`, verify the terminal integrity fields when possible, and join primary/fallback runs using `logicalOperationId`, `parentRunId`, and `attempt`. Reassemble fragmented records before interpreting their original event.

Report missing terminal records, sequence gaps, digest mismatches, redacted secrets, missing provider-exposed reasoning, and unavailable sibling runs as limitations. Never infer hidden chain-of-thought that is not present in the archive.

Do not use this skill while implementing or operating Phantom Lancer. It documents archived file semantics only and provides no download, credential, object-storage, investment, or runtime Agent workflow.
