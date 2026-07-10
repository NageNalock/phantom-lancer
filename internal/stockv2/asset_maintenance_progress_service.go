package stockv2

import "context"

// PopulateAssetMaintenanceProgress decorates the lightweight job snapshot with
// two independent views: deterministic base work and asynchronous AI work.
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
	for i := range jobs {
		pending := jobs[i].TotalCount - jobs[i].ProcessedCount
		if pending < 0 {
			pending = 0
		}
		aiProgress := aiByJob[jobs[i].ID]
		if aiProgress.Status == "" {
			aiProgress.Status = AssetAIProgressStatusNotRequired
		}
		jobs[i].MaintenanceProgress = AssetMaintenanceJobProgress{
			Base: AssetMaintenanceBaseProgress{
				Status:    jobs[i].Status,
				Total:     jobs[i].TotalCount,
				Processed: jobs[i].ProcessedCount,
				Succeeded: jobs[i].SuccessCount,
				Failed:    jobs[i].FailedCount,
				Pending:   pending,
			},
			AIProfile: aiProgress,
		}
	}
	return nil
}
