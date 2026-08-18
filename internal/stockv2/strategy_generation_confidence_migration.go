package stockv2

import (
	"context"
	"database/sql"
	"strings"
)

type strategyGenerationConfidenceBackfill struct {
	versionID string
	meta      map[string]any
}

// backfillStrategyGenerationConfidence repairs only historical drafts whose
// raw Agent result omitted per-draft confidence while retaining a valid run
// confidence. Explicit per-draft zero values are left unchanged.
func (s *Store) backfillStrategyGenerationConfidence(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT v.id, s.symbol, v.generation_meta_json, l.structured_output_json
		FROM stockv2_strategy_versions v
		JOIN stockv2_strategies s ON s.id = v.strategy_id
		JOIN stockv2_agent_decision_ledgers l
		  ON l.run_id = json_extract(v.generation_meta_json, '$.agentRunId')
		WHERE s.source = ?
		  AND json_valid(v.generation_meta_json)
		  AND json_extract(v.generation_meta_json, '$.source') = ?
		  AND CAST(COALESCE(json_extract(v.generation_meta_json, '$.strategyGeneration.confidence'), 0) AS REAL) = 0
	`, StrategySourceAgent, AgentTaskTypeStrategyGeneration)
	if err != nil {
		return err
	}
	updates := make([]strategyGenerationConfidenceBackfill, 0)
	for rows.Next() {
		var versionID, symbol, metaJSON, ledgerJSON string
		if err := rows.Scan(&versionID, &symbol, &metaJSON, &ledgerJSON); err != nil {
			_ = rows.Close()
			return err
		}
		meta := unmarshalMap(metaJSON)
		ledger := unmarshalMap(ledgerJSON)
		if !strategyGenerationDraftOmittedConfidence(mapFromAny(ledger["result"]), symbol) {
			continue
		}
		confidence, ok := numberFromAny(ledger["confidence"])
		if !ok || !validStrategyGenerationConfidence(confidence) {
			continue
		}
		generation := mapFromAny(meta["strategyGeneration"])
		generation["confidence"] = confidence
		generation["confidenceSource"] = StrategyGenerationConfidenceSourceRun
		meta["strategyGeneration"] = generation
		updates = append(updates, strategyGenerationConfidenceBackfill{versionID: versionID, meta: meta})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}
	return s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, update := range updates {
			if _, err := tx.ExecContext(ctx, `UPDATE stockv2_strategy_versions SET generation_meta_json = ? WHERE id = ?`, marshalMap(update.meta), update.versionID); err != nil {
				return err
			}
		}
		return nil
	})
}

func strategyGenerationDraftOmittedConfidence(report map[string]any, symbol string) bool {
	symbol = strings.TrimSpace(symbol)
	for _, draftRaw := range sliceFromAny(report["drafts"]) {
		draft := mapFromAny(draftRaw)
		if strings.TrimSpace(stringFromAny(draft["symbol"])) != symbol {
			continue
		}
		_, exists := draft["confidence"]
		return !exists
	}
	return false
}
