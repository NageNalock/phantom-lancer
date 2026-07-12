import assert from "node:assert/strict";

import {
  confidenceLabel,
  formatNewsContextBytes,
  indexStatusTone,
  namedObjectLabel,
  researchStatusLabel,
  themeStageLabel,
} from "../src/features/stockv2/news-context/model.ts";

assert.equal(themeStageLabel("accelerating"), "加速");
assert.equal(indexStatusTone("failed"), "danger");
assert.equal(confidenceLabel(0.82), "82%");
assert.equal(formatNewsContextBytes(1024 * 1024), "1.0 MB");
assert.equal(namedObjectLabel({ symbol: "600000", name: "示例公司" }), "600000 示例公司");
assert.equal(researchStatusLabel("unresolved"), "公开核实有未决项");

console.log("消息脉络前端纯函数检查通过");
