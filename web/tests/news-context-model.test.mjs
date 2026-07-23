import assert from "node:assert/strict";

import {
  backfillPhaseLabel,
  backfillRunPhaseLabel,
  backfillStageLabel,
  backfillStageStatusLabel,
  backfillStageStatusTone,
  backfillStatusTone,
  confidenceLabel,
  formatNewsContextBytes,
  formatNewsContextInterval,
  indexStatusTone,
  mcpVerificationLabel,
  mcpVerificationTone,
  namedObjectLabel,
  newsContextBackfillNeedsRetry,
  newsContextFinalReviewCoverage,
  newsContextRunCoverage,
  originalNewsStatus,
  researchStatusLabel,
  resolveAvailableTaskModel,
  themeStageLabel,
} from "../src/features/stockv2/news-context/model.ts";
import { startSequentialPolling } from "../src/features/stockv2/news-context/polling.ts";

assert.equal(themeStageLabel("accelerating"), "加速");
assert.equal(indexStatusTone("failed"), "danger");
assert.equal(confidenceLabel(0.82), "82%");
assert.equal(formatNewsContextBytes(1024 * 1024), "1.0 MB");
assert.equal(formatNewsContextInterval(86400), "1 天");
assert.equal(namedObjectLabel({ symbol: "600000", name: "示例公司" }), "600000 示例公司");
assert.equal(researchStatusLabel("unresolved"), "公开核实有未决项");
assert.equal(backfillPhaseLabel("final_review"), "完成组合影响复核");
assert.equal(backfillPhaseLabel("finalizing"), "执行最终安全校验");
assert.equal(backfillPhaseLabel("late_scan"), "检查迟到与遗漏消息");
assert.equal(backfillStatusTone("failed"), "danger");
assert.equal(backfillStageLabel("hourly"), "小时检查点");
assert.equal(backfillStageLabel("four_hour"), "四小时模型归纳");
assert.equal(backfillStageLabel("daily"), "日级增量物化");
assert.equal(backfillStageStatusLabel("running"), "进行中");
assert.equal(backfillStageStatusTone("completed"), "good");
assert.equal(backfillRunPhaseLabel("checkpointing"), "写入检查点");
assert.equal(backfillRunPhaseLabel("materializing"), "增量物化");
assert.equal(mcpVerificationLabel(true, true), "待真实验证");
assert.equal(mcpVerificationLabel(true, true, "ready"), "已验证");
assert.equal(mcpVerificationTone(true, true, "failed"), "danger");

const taskProfiles = [{ id: "task", taskType: "news_event_review", primaryModelId: "primary", fallbackModelId: "fallback" }];
const models = [
  { id: "primary", providerId: "provider", modelName: "主模型", enabled: true, status: "", modelType: "chat" },
  { id: "fallback", providerId: "provider", modelName: "备用模型", enabled: true, status: "available", modelType: "chat" },
];
assert.equal(resolveAvailableTaskModel("news_event_review", taskProfiles, models)?.id, "fallback");
assert.equal(resolveAvailableTaskModel("portfolio_sentinel", taskProfiles, models), undefined);

assert.deepEqual(newsContextRunCoverage({
  id: "run",
  totalNewsCount: 100,
  processedNewsCount: 96,
  coveredCount: 70,
  noiseCount: 20,
  deferredCount: 6,
  pendingThemeCount: 4,
}), { empty: false, total: 100, covered: 70, noise: 20, deferred: 6, waiting: 4, percent: 96 });
assert.deepEqual(newsContextRunCoverage({
	id: "empty-run",
	status: "completed",
	windowType: "hourly",
	totalNewsCount: 0,
	processedNewsCount: 0,
	coverageStatus: "complete",
	progress: 1,
}), { empty: true, total: 0, covered: 0, noise: 0, deferred: 0, waiting: 0, percent: 0 });
assert.equal(newsContextBackfillNeedsRetry({
  id: "backfill",
  status: "completed",
  totalNewsCount: 100,
  processedNewsCount: 99,
  remainingNewsCount: 0,
  missingNewsCount: 1,
  completedChunkCount: 4,
}), true);
assert.deepEqual(newsContextFinalReviewCoverage({
  id: "backfill",
  status: "completed",
  totalNewsCount: 100,
  processedNewsCount: 100,
  remainingNewsCount: 0,
  missingNewsCount: 0,
  completedChunkCount: 4,
  historicalDailyOutputVersionCount: 3,
  finalReviewLinkedVersionCount: 2,
  finalReviewMissingVersionCount: 1,
}), { output: 3, linked: 2, missing: 1 });
assert.equal(newsContextFinalReviewCoverage({
  id: "old-backfill",
  status: "completed",
  totalNewsCount: 100,
  processedNewsCount: 100,
  remainingNewsCount: 0,
  missingNewsCount: 0,
  completedChunkCount: 4,
}), null);
assert.deepEqual(originalNewsStatus(true), { label: "原文已清理", tone: "neutral" });
assert.deepEqual(originalNewsStatus(false), { label: "原文保留", tone: "good" });
assert.deepEqual(originalNewsStatus(), { label: "原文状态未知", tone: "neutral" });

let pollingCalls = 0;
let concurrentPollingCalls = 0;
let maxConcurrentPollingCalls = 0;
let releasePoll;
const stopPolling = startSequentialPolling(async () => {
  pollingCalls += 1;
  concurrentPollingCalls += 1;
  maxConcurrentPollingCalls = Math.max(maxConcurrentPollingCalls, concurrentPollingCalls);
  await new Promise((resolve) => {
    releasePoll = resolve;
  });
  concurrentPollingCalls -= 1;
}, 1);
await new Promise((resolve) => setTimeout(resolve, 10));
assert.equal(pollingCalls, 1);
releasePoll();
await new Promise((resolve) => setTimeout(resolve, 10));
assert.equal(pollingCalls, 2);
stopPolling();
releasePoll();
await new Promise((resolve) => setTimeout(resolve, 10));
assert.equal(maxConcurrentPollingCalls, 1);
assert.equal(pollingCalls, 2);

console.log("消息脉络前端纯函数检查通过");
