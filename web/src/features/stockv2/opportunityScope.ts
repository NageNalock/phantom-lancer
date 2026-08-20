import type { StockV2OpportunityCandidate } from "../../app/types";

export function isOpportunityCandidateExcludedByBoard(
  candidate: StockV2OpportunityCandidate,
  excludeChiNextAndStarMarket: boolean,
): boolean {
  if (!excludeChiNextAndStarMarket || (candidate.instrumentType || "stock") !== "stock") return false;
  const symbol = candidate.symbol.trim();
  const market = (candidate.market || "").trim().toUpperCase();
  return (market === "SZ" && (symbol.startsWith("300") || symbol.startsWith("301")))
    || (market === "SH" && (symbol.startsWith("688") || symbol.startsWith("689")));
}
