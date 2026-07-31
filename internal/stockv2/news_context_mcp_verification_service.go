package stockv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

// ponytail: MCP verification returns one compact search/detail payload; a fixed
// internal cap prevents accidental memory growth and is not an owner workflow limit.
const newsContextMCPVerificationResponseLimit = 2 << 20

type newsContextMCPVerificationCacheKey struct{}

type newsContextMCPVerificationCache map[string]string

func (s *Service) VerifyNewsThreadMCP(ctx context.Context, threadID string) (NewsContextMCPVerification, error) {
	thread, err := s.store.GetNewsThread(ctx, strings.TrimSpace(threadID))
	if err != nil {
		return NewsContextMCPVerification{}, err
	}
	if strings.TrimSpace(thread.CurrentVersionID) == "" {
		return NewsContextMCPVerification{}, ErrNewsContextPrerequisite
	}
	return s.verifyNewsThreadMCP(ctx, thread, nil)
}

func (s *Service) verifyHistoricalNewsThreadMCP(ctx context.Context, threadID, versionID string) (NewsContextMCPVerification, error) {
	thread, err := s.store.GetNewsThread(ctx, strings.TrimSpace(threadID))
	if err != nil {
		return NewsContextMCPVerification{}, err
	}
	version, err := s.store.GetNewsThreadVersion(ctx, strings.TrimSpace(versionID))
	if err != nil {
		return NewsContextMCPVerification{}, err
	}
	if version.ThreadID != thread.ID || newsThreadVersionEffectiveTime(version).IsZero() {
		return NewsContextMCPVerification{}, ErrNewsContextPrerequisite
	}
	return s.verifyNewsThreadMCP(ctx, thread, &version)
}

func (s *Service) verifyNewsThreadMCP(ctx context.Context, thread NewsThread, historical *NewsThreadVersion) (NewsContextMCPVerification, error) {
	versionID := strings.TrimSpace(thread.CurrentVersionID)
	queryParts := []string{thread.Title, thread.CoreThesis, thread.LatestChange}
	searchArguments := map[string]any{"limit": 50}
	detailArguments := map[string]any{"threadId": thread.ID}
	if historical != nil {
		versionID = historical.ID
		queryParts = []string{historical.Title, historical.CoreThesis, historical.LatestChange}
		asOf := newsThreadVersionEffectiveTime(*historical).Format(time.RFC3339Nano)
		searchArguments["asOf"] = asOf
		detailArguments["asOf"] = asOf
	}
	if cache, ok := ctx.Value(newsContextMCPVerificationCacheKey{}).(newsContextMCPVerificationCache); ok && cache[thread.ID] == versionID {
		if cached, found, err := s.store.GetNewsContextMCPVerification(ctx, thread.ID); err != nil {
			return NewsContextMCPVerification{}, err
		} else if found && cached.Status == NewsContextMCPVerificationReady {
			return cached, nil
		}
	}

	checkedAt := time.Now()
	fail := func(cause error) (NewsContextMCPVerification, error) {
		message := safelog.Text(cause.Error(), 500)
		item, storeErr := s.store.UpsertNewsContextMCPVerification(ctx, NewsContextMCPVerification{
			ThreadID: thread.ID, VersionID: versionID, Status: NewsContextMCPVerificationFailed,
			CheckedAt: checkedAt, ErrorMessage: message,
		})
		if storeErr != nil {
			return NewsContextMCPVerification{}, storeErr
		}
		return item, cause
	}

	query := strings.TrimSpace(strings.Join(nonEmptyStrings(queryParts), "\n"))
	if query == "" {
		return fail(errors.New("news thread has no searchable content"))
	}
	query = safelog.Text(query, 4000)
	searchArguments["query"] = query
	var search struct {
		Items []SemanticNewsThreadResult `json:"items"`
	}
	if err := s.callNewsContextMCPTool(ctx, mcpToolSemanticSearchNewsThreads, searchArguments, &search); err != nil {
		return fail(fmt.Errorf("semantic theme lookup failed: %w", err))
	}
	found := false
	for _, item := range search.Items {
		if item.Thread.ID == thread.ID && (historical == nil || (item.Version != nil && item.Version.ID == versionID && item.Thread.CurrentVersionID == versionID)) {
			found = true
			break
		}
	}
	if !found {
		return fail(errors.New("semantic theme lookup did not return the expected theme"))
	}
	var detail NewsThreadDetail
	if err := s.callNewsContextMCPTool(ctx, mcpToolGetNewsThread, detailArguments, &detail); err != nil {
		return fail(fmt.Errorf("theme detail lookup failed: %w", err))
	}
	if detail.Theme.ID != thread.ID || detail.Theme.CurrentVersionID != versionID {
		return fail(errors.New("theme detail lookup returned a different expected version"))
	}
	stored, err := s.store.UpsertNewsContextMCPVerification(ctx, NewsContextMCPVerification{
		ThreadID: thread.ID, VersionID: versionID, Status: NewsContextMCPVerificationReady,
		CheckedAt: checkedAt, VerifiedAt: time.Now(),
	})
	if err == nil {
		if cache, ok := ctx.Value(newsContextMCPVerificationCacheKey{}).(newsContextMCPVerificationCache); ok {
			cache[thread.ID] = versionID
		}
	}
	return stored, err
}

func (s *Service) VerifyNewsContextMCP(ctx context.Context, threadIDs []string) error {
	for _, threadID := range uniqueNonEmptyStrings(threadIDs) {
		if _, err := s.VerifyNewsThreadMCP(ctx, threadID); err != nil {
			return err
		}
	}
	return nil
}

// VerifyNewsContextMCPProbe proves the real local MCP path with one semantic
// search and one detail read. Static index checks cover the full set separately;
// doing a top-N search for every theme would be both expensive and semantically wrong.
func (s *Service) VerifyNewsContextMCPProbe(ctx context.Context, representativeThreadID string) error {
	representative, err := s.store.GetNewsThread(ctx, strings.TrimSpace(representativeThreadID))
	if err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(nonEmptyStrings([]string{
		representative.Title, representative.CoreThesis, representative.LatestChange,
	}), "\n"))
	if query == "" {
		return errors.New("news context MCP probe has no searchable theme")
	}
	var search struct {
		Items []SemanticNewsThreadResult `json:"items"`
	}
	if err := s.callNewsContextMCPTool(ctx, mcpToolSemanticSearchNewsThreads, map[string]any{
		"query": safelog.Text(query, 4000), "limit": 10,
	}, &search); err != nil {
		return fmt.Errorf("semantic theme probe failed: %w", err)
	}
	if len(search.Items) == 0 || strings.TrimSpace(search.Items[0].Thread.ID) == "" {
		return errors.New("semantic theme probe returned no theme")
	}
	hit := search.Items[0].Thread
	var detail NewsThreadDetail
	if err := s.callNewsContextMCPTool(ctx, mcpToolGetNewsThread, map[string]any{"threadId": hit.ID}, &detail); err != nil {
		return fmt.Errorf("theme detail probe failed: %w", err)
	}
	if detail.Theme.ID != hit.ID || strings.TrimSpace(detail.Theme.CurrentVersionID) == "" ||
		(strings.TrimSpace(hit.CurrentVersionID) != "" && detail.Theme.CurrentVersionID != hit.CurrentVersionID) {
		return errors.New("theme detail probe returned an inconsistent current version")
	}
	_, err = s.store.UpsertNewsContextMCPVerification(ctx, NewsContextMCPVerification{
		ThreadID: detail.Theme.ID, VersionID: detail.Theme.CurrentVersionID,
		Status: NewsContextMCPVerificationReady, CheckedAt: time.Now(), VerifiedAt: time.Now(),
	})
	return err
}

func (s *Service) callNewsContextMCPTool(ctx context.Context, name string, arguments any, output any) error {
	status := s.AgentMCPStatus()
	if !status.Enabled || !newsContextContainsString(status.RequiredTools, name) {
		return errors.New("stock agent MCP tool is unavailable")
	}
	endpoint, err := url.Parse(status.URL)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" {
		return errors.New("stock agent MCP endpoint is not a loopback HTTP endpoint")
	}
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "news-context-cleanup-verification",
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": arguments},
	})
	if err != nil {
		return err
	}
	// ponytail: this fixed deadline only protects an in-process loopback safety
	// probe; it is not an owner-tunable research or provider timeout.
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("MCP HTTP status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, newsContextMCPVerificationResponseLimit+1))
	if err != nil {
		return err
	}
	if len(body) > newsContextMCPVerificationResponseLimit {
		return errors.New("MCP response exceeds the safety limit")
	}
	var decoded struct {
		Error  *mcpError `json:"error"`
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	if decoded.Error != nil {
		return errors.New(safelog.Text(decoded.Error.Message, 500))
	}
	if decoded.Result.IsError || len(decoded.Result.Content) == 0 || decoded.Result.Content[0].Type != "text" {
		return errors.New("MCP tool returned no readable result")
	}
	if err := json.Unmarshal([]byte(decoded.Result.Content[0].Text), output); err != nil {
		return err
	}
	return nil
}
