package main

import "testing"

func TestParseStockV2EmbeddingMigrationOptions(t *testing.T) {
	opts, err := parseStockV2EmbeddingMigrationOptions([]string{
		"--config", "/tmp/phantom.toml",
		"--target-model-name", "BAAI/bge-m3",
		"--batch-size", "200",
		"--rate-limit-ms", "500",
	})
	if err != nil {
		t.Fatalf("parse migration options: %v", err)
	}
	if opts.configPath != "/tmp/phantom.toml" || opts.targetModelName != "BAAI/bge-m3" || opts.batchSize != 200 || opts.rateLimitMS != 500 {
		t.Fatalf("options=%+v", opts)
	}
	if _, err := parseStockV2EmbeddingMigrationOptions([]string{"--batch-size", "201"}); err == nil {
		t.Fatal("batch size above service maximum must fail")
	}
}
