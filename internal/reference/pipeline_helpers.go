package reference

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"
)

type collectedPipelineSources struct {
	allTracks       []CanonicalTrack
	discMap         map[string]CanonicalTrack
	compiledSources []compiledSourceArtifact
	archivedList    []ArchivedSource
	staleSources    []string
}

func resolvePipelineDefaults(opts PipelineOptions) (time.Time, string, func(string, ...any)) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	sourcesPath := strings.TrimSpace(opts.SourcesPath)
	if sourcesPath == "" {
		sourcesPath = "./internal/reference/sources.json"
	}
	return now.UTC(), sourcesPath, logf
}

func collectPipelineSources(
	ctx context.Context,
	cfg *SourcesConfig,
	prevManifest *SourceManifest,
	opts PipelineOptions,
	now time.Time,
	logf func(string, ...any),
) (*collectedPipelineSources, error) {
	collected := &collectedPipelineSources{
		discMap:      make(map[string]CanonicalTrack),
		staleSources: make([]string, 0),
	}

	for _, src := range cfg.AllSources() {
		artifact, tracks, dm, err := processPipelineSource(ctx, src, prevManifest, opts, now, logf)
		if err != nil {
			return nil, err
		}
		if artifact == nil {
			continue
		}
		mergeDiscMap(collected.discMap, dm, src.ID)
		collected.allTracks = append(collected.allTracks, tracks...)
		if artifact.archived.Stale {
			collected.staleSources = append(collected.staleSources, artifact.archived.ID)
		}
		collected.compiledSources = append(collected.compiledSources, *artifact)
		collected.archivedList = append(collected.archivedList, artifact.archived)
	}

	return collected, nil
}

func processPipelineSource(
	ctx context.Context,
	src SourceConfig,
	prevManifest *SourceManifest,
	opts PipelineOptions,
	now time.Time,
	logf func(string, ...any),
) (*compiledSourceArtifact, []CanonicalTrack, map[string]CanonicalTrack, error) {
	prevEntry := findPrevArchivedSource(prevManifest, src.ID)

	resolved, fetchErr := FetchOrResolveSource(ctx, src, prevEntry, FetchOptions{
		HTTPClient: opts.HTTPClient,
		Store:      opts.Store,
		ArchiveDir: opts.ArchiveDir,
		Offline:    opts.Offline,
		Now:        now,
		RetryDelay: opts.RetryDelay,
		MaxRetries: opts.MaxRetries,
	})
	if fetchErr != nil {
		if src.Required {
			return nil, nil, nil, fmt.Errorf("required source %q failed: %w", src.ID, fetchErr)
		}
		logf("Warning: optional source %q skipped: %v", src.ID, fetchErr)
		return nil, nil, nil, nil
	}

	tracks, dm, csvBytes, parseErr := parseSourceWithFallback(ctx, src, resolved, prevEntry, opts, now)
	if parseErr != nil {
		if src.Required {
			return nil, nil, nil, fmt.Errorf("parse required source %q (%s): %w", src.ID, src.Format, parseErr)
		}
		logf("Warning: parse optional source %q failed: %v", src.ID, parseErr)
		return nil, nil, nil, nil
	}

	entry := buildArchivedSource(src, resolved, csvBytes, len(tracks), len(dm))
	logf("Source %q (%s, %s): %d tracks, %d discs [sha256=%s]",
		entry.ID, entry.Lifecycle, entry.Origin, entry.TracksCount, entry.DiscsCount, entry.RawSHA256[:16])

	artifact := &compiledSourceArtifact{
		config:   src,
		archived: entry,
		rawBytes: resolved.RawBytes,
		csvBytes: csvBytes,
	}
	return artifact, tracks, dm, nil
}

func findPrevArchivedSource(prevManifest *SourceManifest, id string) *ArchivedSource {
	if prevManifest == nil {
		return nil
	}
	found, ok := prevManifest.FindSource(id)
	if !ok {
		return nil
	}
	return &found
}

func parseSourceWithFallback(
	ctx context.Context,
	src SourceConfig,
	resolved *ResolvedRawSource,
	prevEntry *ArchivedSource,
	opts PipelineOptions,
	now time.Time,
) ([]CanonicalTrack, map[string]CanonicalTrack, []byte, error) {
	tracks, dm, csvBytes, parseErr := ParseRawSource(src, resolved.RawBytes)
	if parseErr == nil || resolved.Origin != OriginHTTP200 {
		return tracks, dm, csvBytes, parseErr
	}

	// If fresh HTTP 200 payload failed parsing and we have a distinct cached copy, fall back to cache.
	ext, _, _ := RawExtAndContentType(src.Format)
	allowLegacyFlat := src.Format != "billboard_csv"
	cachedRaw, _, cacheErr := loadCachedRawBytes(ctx, src, ext, prevEntry, opts.Store, opts.ArchiveDir, allowLegacyFlat)
	if cacheErr != nil || bytes.Equal(cachedRaw, resolved.RawBytes) {
		return tracks, dm, csvBytes, parseErr
	}

	cTracks, cDM, cCSV, cParseErr := ParseRawSource(src, cachedRaw)
	if cParseErr != nil {
		return tracks, dm, csvBytes, parseErr
	}

	applyCachedFallback(src, resolved, prevEntry, cachedRaw, ext, parseErr, now)
	return cTracks, cDM, cCSV, nil
}

func applyCachedFallback(
	src SourceConfig,
	resolved *ResolvedRawSource,
	prevEntry *ArchivedSource,
	cachedRaw []byte,
	ext string,
	parseErr error,
	now time.Time,
) {
	rawKey, rawFilename, fullSHA := ContentAddressedRawKey(src.ID, ext, cachedRaw)
	fetchedAt, prevETag, prevLastMod := now.Format(time.RFC3339), "", ""
	if prevEntry != nil {
		if prevEntry.FetchedAt != "" {
			fetchedAt = prevEntry.FetchedAt
		}
		if prevEntry.RawSHA256 != "" && prevEntry.RawSHA256 == fullSHA {
			prevETag = prevEntry.ETag
			prevLastMod = prevEntry.LastModified
		}
	}
	resolved.RawBytes = cachedRaw
	resolved.RawKey = rawKey
	resolved.RawFilename = rawFilename
	resolved.RawSHA256 = fullSHA
	resolved.FetchedAt = fetchedAt
	resolved.ETag = prevETag
	resolved.LastModified = prevLastMod
	resolved.Origin = OriginS3Fallback
	resolved.Stale = src.Lifecycle == LifecycleRolling
	resolved.FallbackReason = fmt.Sprintf("parse failed: %v", parseErr)
}

func mergeDiscMap(dst, src map[string]CanonicalTrack, sourceID string) {
	for k, v := range src {
		if _, exists := dst[k]; !exists || sourceID == "historical-discs" {
			dst[k] = v
		}
	}
}

func buildArchivedSource(src SourceConfig, resolved *ResolvedRawSource, csvBytes []byte, tracksCount, discsCount int) ArchivedSource {
	csvFilename := src.ID + ".csv"
	sourceURL := src.URL
	if sourceURL == "" {
		sourceURL = src.LocalPath
	}
	return ArchivedSource{
		ID:             src.ID,
		Name:           src.Name,
		Provider:       src.Provider,
		Format:         resolved.ShortFormat,
		Lifecycle:      src.Lifecycle,
		SourceURL:      sourceURL,
		RawFilename:    resolved.RawFilename,
		RawKey:         resolved.RawKey,
		RawContentType: resolved.RawContentType,
		RawSizeBytes:   int64(len(resolved.RawBytes)),
		RawSHA256:      resolved.RawSHA256,
		CSVFilename:    csvFilename,
		CSVKey:         SourcesS3Prefix + csvFilename,
		CSVSizeBytes:   int64(len(csvBytes)),
		TracksCount:    tracksCount,
		DiscsCount:     discsCount,
		FetchedAt:      resolved.FetchedAt,
		ETag:           resolved.ETag,
		LastModified:   resolved.LastModified,
		Origin:         resolved.Origin,
		Stale:          resolved.Stale,
		FallbackReason: resolved.FallbackReason,
	}
}

func compileCatalogArtifacts(allTracks []CanonicalTrack, discMap map[string]CanonicalTrack) ([]byte, int, []byte, error) {
	dictGz, err := CompileDictionary(allTracks)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("compile dictionary: %w", err)
	}
	var dictPayload struct {
		Tracks map[string]any `json:"tracks"`
	}
	if err := DecodeGzipJSON(dictGz, &dictPayload); err != nil {
		return nil, 0, nil, fmt.Errorf("decode compiled dictionary: %w", err)
	}

	EnrichDiscMap(discMap, allTracks)
	discGz, err := CompileDiscMap(discMap)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("compile disc map: %w", err)
	}
	return dictGz, len(dictPayload.Tracks), discGz, nil
}

func buildSourceManifest(
	now time.Time,
	totalUniqueTracks int,
	mappedDiscs int,
	staleSources []string,
	archivedList []ArchivedSource,
) SourceManifest {
	inputsHash := ComputeInputsHash(archivedList)
	shortInputs := strings.TrimPrefix(inputsHash, "sha256:")
	if len(shortInputs) > 8 {
		shortInputs = shortInputs[:8]
	}
	buildID := fmt.Sprintf("%s-%s", now.Format("20060102T150405Z"), shortInputs)

	return SourceManifest{
		Version:          2,
		BuildID:          buildID,
		CompiledAt:       now.Format(time.RFC3339),
		InputsHash:       inputsHash,
		DictionaryTracks: totalUniqueTracks,
		MappedDiscs:      mappedDiscs,
		Degraded:         len(staleSources) > 0,
		StaleSources:     staleSources,
		Sources:          archivedList,
	}
}

func verifySourceGuardrails(item compiledSourceArtifact, prev *SourceManifest, now time.Time) error {
	srcCfg := item.config
	arch := item.archived
	if err := verifySourceFloors(srcCfg, arch); err != nil {
		return err
	}
	if err := verifySourceDrop(srcCfg, arch, prev); err != nil {
		return err
	}
	return verifySourceStaleness(srcCfg, arch, now)
}

func verifySourceFloors(srcCfg SourceConfig, arch ArchivedSource) error {
	if srcCfg.MinTracks > 0 && arch.TracksCount < srcCfg.MinTracks {
		return fmt.Errorf("guardrail failed: source %q parsed %d tracks, below min_tracks floor %d",
			arch.ID, arch.TracksCount, srcCfg.MinTracks)
	}
	if srcCfg.MinDiscs > 0 && arch.DiscsCount < srcCfg.MinDiscs {
		return fmt.Errorf("guardrail failed: source %q parsed %d discs, below min_discs floor %d",
			arch.ID, arch.DiscsCount, srcCfg.MinDiscs)
	}
	return nil
}

func verifySourceDrop(srcCfg SourceConfig, arch ArchivedSource, prev *SourceManifest) error {
	if prev == nil || srcCfg.MaxDropPct <= 0 {
		return nil
	}
	prevSrc, ok := prev.FindSource(arch.ID)
	if !ok {
		return nil
	}
	if dropPct, exceeded := exceedsDropThreshold(prevSrc.TracksCount, arch.TracksCount, srcCfg.MaxDropPct); exceeded {
		return fmt.Errorf("guardrail failed: source %q tracks dropped by %.2f%% (%d -> %d), exceeding max_drop_pct %.2f%%",
			arch.ID, dropPct, prevSrc.TracksCount, arch.TracksCount, srcCfg.MaxDropPct)
	}
	if srcCfg.MinDiscs > 0 {
		if dropPct, exceeded := exceedsDropThreshold(prevSrc.DiscsCount, arch.DiscsCount, srcCfg.MaxDropPct); exceeded {
			return fmt.Errorf("guardrail failed: source %q discs dropped by %.2f%% (%d -> %d), exceeding max_drop_pct %.2f%%",
				arch.ID, dropPct, prevSrc.DiscsCount, arch.DiscsCount, srcCfg.MaxDropPct)
		}
	}
	return nil
}

func verifySourceStaleness(srcCfg SourceConfig, arch ArchivedSource, now time.Time) error {
	if !arch.Stale || srcCfg.MaxStaleDays <= 0 {
		return nil
	}
	fetchedTime, err := time.Parse(time.RFC3339, arch.FetchedAt)
	if err != nil {
		return fmt.Errorf("guardrail failed: rolling source %q is stale and has invalid/missing fetched_at %q", arch.ID, arch.FetchedAt)
	}
	ageDays := now.Sub(fetchedTime).Hours() / 24.0
	if ageDays > float64(srcCfg.MaxStaleDays) {
		return fmt.Errorf("guardrail failed: rolling source %q is stale (last fetched %s, %.1f days ago > max_stale_days %d; fallback_reason=%s)",
			arch.ID, arch.FetchedAt, ageDays, srcCfg.MaxStaleDays, arch.FallbackReason)
	}
	return nil
}

func verifyGlobalDropGuardrails(cfg *SourcesConfig, curr, prev *SourceManifest) error {
	if prev == nil || cfg == nil || cfg.MaxGlobalDropPct <= 0 {
		return nil
	}
	if dropPct, exceeded := exceedsDropThreshold(prev.DictionaryTracks, curr.DictionaryTracks, cfg.MaxGlobalDropPct); exceeded {
		return fmt.Errorf("guardrail failed: global dictionary_tracks dropped by %.2f%% (%d -> %d), exceeding max_global_drop_pct %.2f%%",
			dropPct, prev.DictionaryTracks, curr.DictionaryTracks, cfg.MaxGlobalDropPct)
	}
	if dropPct, exceeded := exceedsDropThreshold(prev.MappedDiscs, curr.MappedDiscs, cfg.MaxGlobalDropPct); exceeded {
		return fmt.Errorf("guardrail failed: global mapped_discs dropped by %.2f%% (%d -> %d), exceeding max_global_drop_pct %.2f%%",
			dropPct, prev.MappedDiscs, curr.MappedDiscs, cfg.MaxGlobalDropPct)
	}
	return nil
}

func exceedsDropThreshold(prevCount, currCount int, maxDropPct float64) (float64, bool) {
	if prevCount <= 0 {
		return 0, false
	}
	dropPct := float64(prevCount-currCount) * 100.0 / float64(prevCount)
	return dropPct, dropPct > maxDropPct
}
