package stockv2

import "context"

// PopulateAssetMaintenanceProgress decorates the lightweight job snapshot with
// independent coverage, deterministic freshness, and asynchronous AI views.
func (s *Service) PopulateAssetMaintenanceProgress(ctx context.Context, jobs []StockV2UpdateJob) error {
	jobIDs := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if job.ID != "" {
			jobIDs = append(jobIDs, job.ID)
		}
	}
	aiByJob, err := s.store.GetAssetMaintenanceAIProgressByJobIDs(ctx, jobIDs)
	if err != nil {
		return err
	}
	assetsByJob, err := s.store.GetAssetMaintenanceAssetsProgressByJobIDs(ctx, jobIDs)
	if err != nil {
		return err
	}
	for i := range jobs {
		checked := jobs[i].CheckedCount
		if checked == 0 && jobs[i].ProcessedCount > 0 {
			checked = jobs[i].ProcessedCount
		}
		pending := jobs[i].TotalCount - checked
		if pending < 0 {
			pending = 0
		}
		aiProgress := aiByJob[jobs[i].ID]
		if aiProgress.Status == "" {
			aiProgress.Status = AssetAIProgressStatusNotRequired
		}
		assetsProgress := assetsByJob[jobs[i].ID]
		assetsProgress.Status = jobs[i].FreshnessStatus
		assetsProgress.Stale = jobs[i].StaleCount
		jobs[i].MaintenanceProgress = AssetMaintenanceJobProgress{
			Coverage: AssetMaintenanceCoverageProgress{
				Status:       jobs[i].CoverageStatus,
				Target:       jobs[i].TotalCount,
				Checked:      checked,
				Pending:      pending,
				Retrying:     jobs[i].RetryCount,
				Failed:       jobs[i].FailedCount,
				UniverseHash: jobs[i].UniverseHash,
				CutoffDate:   jobs[i].ExpectedLatestDate,
			},
			Assets:    assetsProgress,
			AIProfile: aiProgress,
		}
	}
	return nil
}
