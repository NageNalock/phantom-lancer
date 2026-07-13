package stockv2

import (
	"context"
	"strings"
	"time"
)

// GetNewsThreadDetailAsOf keeps the current-detail path unchanged when no
// cutoff is supplied and reconstructs the thread only from knowledge that was
// effective at the requested time otherwise.
func (s *Service) GetNewsThreadDetailAsOf(ctx context.Context, id, asOf string) (NewsThreadDetail, error) {
	if strings.TrimSpace(asOf) == "" {
		return s.GetNewsThreadDetail(ctx, id)
	}
	cutoff := parseNewsContextTime(asOf)
	if cutoff.IsZero() {
		return NewsThreadDetail{}, ErrInvalidNewsContextInput
	}
	return s.getHistoricalNewsThreadDetail(ctx, strings.TrimSpace(id), cutoff)
}

func (s *Service) getHistoricalNewsThreadDetail(ctx context.Context, threadID string, cutoff time.Time) (NewsThreadDetail, error) {
	if threadID == "" || cutoff.IsZero() {
		return NewsThreadDetail{}, ErrInvalidNewsContextInput
	}
	if _, err := s.store.GetNewsThread(ctx, threadID); err != nil {
		return NewsThreadDetail{}, err
	}

	// ponytail: 详情接口本来就读取完整版本历史；沿用既有分页并在服务层按
	// 实际生效时间筛选，避免为单一读取路径增加第二套仓储查询和排序规则。
	versions := make([]NewsThreadVersion, 0, 200)
	for offset := 0; ; offset += 200 {
		page, err := s.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{
			ThreadID: threadID,
			Limit:    200,
			Offset:   offset,
		})
		if err != nil {
			return NewsThreadDetail{}, err
		}
		for _, version := range page {
			if !newsThreadVersionEffectiveTime(version).After(cutoff) {
				versions = append(versions, version)
			}
		}
		if len(page) < 200 {
			break
		}
	}
	if len(versions) == 0 {
		return NewsThreadDetail{}, ErrNewsThreadNotFound
	}
	finalReviewedVersionIDs, err := s.store.ListNewsContextBackfillFinalReviewedVersionIDs(ctx, threadID)
	if err != nil {
		return NewsThreadDetail{}, err
	}
	for index := range versions {
		if _, reviewed := finalReviewedVersionIDs[versions[index].ID]; reviewed {
			versions[index].ReviewStatus = NewsContextReviewCompleted
		}
	}

	// ListNewsThreadVersions is ordered newest-first, so the first retained
	// version is the complete theme snapshot at the requested point in time.
	selected := versions[0]
	theme := historicalNewsThreadSnapshot(selected)
	oldestAt := newsThreadVersionEffectiveTime(versions[len(versions)-1])
	if !oldestAt.IsZero() {
		theme.FirstSeenAt = oldestAt
	}
	allowedVersions := make(map[string]struct{}, len(versions))
	for _, version := range versions {
		allowedVersions[version.ID] = struct{}{}
	}

	evidence := make([]NewsThreadEvidence, 0, 500)
	for offset := 0; ; offset += 500 {
		page, err := s.store.ListNewsThreadEvidence(ctx, NewsThreadEvidenceListFilter{
			ThreadID: threadID,
			Limit:    500,
			Offset:   offset,
		})
		if err != nil {
			return NewsThreadDetail{}, err
		}
		for _, item := range page {
			if _, ok := allowedVersions[item.VersionID]; !ok {
				continue
			}
			// Backfilled evidence is created later than its logical time, so only
			// the source event time and owning version may constrain visibility.
			if !item.EventAt.IsZero() && item.EventAt.After(cutoff) {
				continue
			}
			evidence = append(evidence, item)
		}
		if len(page) < 500 {
			break
		}
	}

	detail := NewsThreadDetail{
		Theme:       theme,
		Versions:    versions,
		Evidence:    evidence,
		IndexStatus: selected.IndexStatus,
		IndexError:  selected.IndexError,
	}
	mcp := s.AgentMCPStatus()
	mcpToolsReady := newsContextContainsString(mcp.RequiredTools, mcpToolSemanticSearchNewsThreads) &&
		newsContextContainsString(mcp.RequiredTools, mcpToolGetNewsThread)
	if !mcp.Enabled {
		detail.MCPError = "本地股票检索服务尚未启动"
	} else if !mcpToolsReady {
		detail.MCPError = "消息脉络检索工具注册不完整"
	}
	if cfg, err := s.embeddingConfigOrDefault(ctx); err == nil && strings.TrimSpace(cfg.EmbeddingModelID) != "" {
		asset, assetErr := s.store.GetEmbeddingAssetByObject(ctx, EmbeddingObjectNewsThreadVersion, selected.ID, cfg.EmbeddingModelID)
		if assetErr == nil && asset.Status == EmbeddingAssetStatusReady && strings.TrimSpace(asset.VectorRef) != "" &&
			asset.TextHash == hashEmbeddingText(NewsThreadVersionEmbeddingText(selected)) {
			detail.IndexStatus = NewsContextIndexReady
			detail.IndexError = ""
			detail.MCPReadable = mcp.Enabled && mcpToolsReady
		} else if assetErr == nil {
			detail.IndexStatus = asset.Status
			detail.IndexError = asset.ErrorMessage
		}
	}
	if detail.IndexStatus != NewsContextIndexReady {
		detail.ProtectedReasons = append(detail.ProtectedReasons, "该时间点主题版本尚未完成向量索引")
	}
	if selected.ReviewStatus != NewsContextReviewCompleted && selected.ReviewStatus != NewsContextReviewNotRequired {
		detail.ProtectedReasons = append(detail.ProtectedReasons, "该时间点主题版本尚未完成影响复核")
	}
	if !detail.MCPReadable {
		detail.ProtectedReasons = append(detail.ProtectedReasons, "CLI 尚不能稳定检索该时间点主题版本")
	}
	detail.ProtectedReasons = uniqueNonEmptyStrings(detail.ProtectedReasons)
	return detail, nil
}
