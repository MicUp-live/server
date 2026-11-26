package reference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type compiledSourceArtifact struct {
	config   SourceConfig
	archived ArchivedSource
	rawBytes []byte
	csvBytes []byte
}

// PipelineOptions configures a canonical reference compilation run.
type PipelineOptions struct {
	SourcesPath string
	OutDir      string
	ArchiveDir  string
	Offline     bool
	Store       Store
	HTTPClient  *http.Client
	Now         time.Time
	RetryDelay  time.Duration
	MaxRetries  int
	Logf        func(format string, args ...any)
}

// PipelineResult summarizes the outcome of RunPipeline.
type PipelineResult struct {
	Manifest       SourceManifest
	DictionaryGzip []byte
	DiscMapGzip    []byte
}

// ParseRawSource parses raw upstream bytes according to src.Format and produces
// the extracted CanonicalTracks, discMap entries, and normalized CSV archive bytes.
func ParseRawSource(src SourceConfig, rawBytes []byte) (tracks []CanonicalTrack, discs map[string]CanonicalTrack, csvBytes []byte, err error) {
	switch strings.ToLower(strings.TrimSpace(src.Format)) {
	case "karafun_csv":
		tracks, err = ParseKaraFunCSV(bytes.NewReader(rawBytes))
		if err != nil {
			return nil, nil, nil, err
		}
		return tracks, nil, rawBytes, nil

	case "zoom_csv":
		tracks, err = ParseZoomCSV(bytes.NewReader(rawBytes))
		if err != nil {
			return nil, nil, nil, err
		}
		return tracks, nil, rawBytes, nil

	case "disc_csv":
		discs, tracks, err = ParseDiscMapCSV(bytes.NewReader(rawBytes))
		if err != nil {
			return nil, nil, nil, err
		}
		return tracks, discs, rawBytes, nil

	case "billboard_csv":
		tracks, err = ParseBillboardCSV(bytes.NewReader(rawBytes), src.Genre, src.Provider)
		if err != nil {
			return nil, nil, nil, err
		}
		var buf bytes.Buffer
		if err := WriteTracksCSV(&buf, tracks); err != nil {
			return nil, nil, nil, err
		}
		return tracks, nil, buf.Bytes(), nil

	case "xlsx":
		discs, tracks, err = ParseMrEntertainerXLSX(rawBytes, src.Provider)
		if err != nil {
			return nil, nil, nil, err
		}
		var buf bytes.Buffer
		if err := WriteDiscMapCSV(&buf, discs); err != nil {
			return nil, nil, nil, err
		}
		return tracks, discs, buf.Bytes(), nil

	case "pdf":
		tmp, tmpErr := os.CreateTemp("", "micup-ref-*.pdf")
		if tmpErr != nil {
			return nil, nil, nil, fmt.Errorf("create temp PDF: %w", tmpErr)
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
		if _, wErr := tmp.Write(rawBytes); wErr != nil {
			tmp.Close()
			return nil, nil, nil, fmt.Errorf("write temp PDF: %w", wErr)
		}
		if cErr := tmp.Close(); cErr != nil {
			return nil, nil, nil, fmt.Errorf("close temp PDF: %w", cErr)
		}
		tracks, discs, err = ParsePDFSongbook(tmpName, src.Provider)
		if err != nil {
			return nil, nil, nil, err
		}
		var buf bytes.Buffer
		if len(discs) > 0 {
			if err := WriteDiscMapCSV(&buf, discs); err != nil {
				return nil, nil, nil, err
			}
		} else {
			if err := WriteTracksCSV(&buf, tracks); err != nil {
				return nil, nil, nil, err
			}
		}
		return tracks, discs, buf.Bytes(), nil

	default:
		return nil, nil, nil, fmt.Errorf("unsupported source format %q", src.Format)
	}
}

// RunPipeline executes the v2 canonical reference compilation pipeline:
// load prior manifest -> resolve/fetch raw bytes -> parse -> enforce guardrails -> write outputs.
func RunPipeline(ctx context.Context, opts PipelineOptions) (*PipelineResult, error) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	sourcesPath := strings.TrimSpace(opts.SourcesPath)
	if sourcesPath == "" {
		sourcesPath = "./internal/reference/sources.json"
	}
	cfg, err := LoadSourcesConfig(sourcesPath)
	if err != nil {
		return nil, err
	}

	prevManifest, err := loadPriorManifest(ctx, opts.Store, opts.ArchiveDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load prior manifest: %w", err)
	}

	var allTracks []CanonicalTrack
	discMap := make(map[string]CanonicalTrack)
	var compiledSources []compiledSourceArtifact
	var archivedList []ArchivedSource
	staleSources := make([]string, 0)

	for _, src := range cfg.AllSources() {
		var prevEntry *ArchivedSource
		if prevManifest != nil {
			if found, ok := prevManifest.FindSource(src.ID); ok {
				prevEntry = &found
			}
		}

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
				return nil, fmt.Errorf("required source %q failed: %w", src.ID, fetchErr)
			}
			logf("Warning: optional source %q skipped: %v", src.ID, fetchErr)
			continue
		}

		tracks, dm, csvBytes, parseErr := ParseRawSource(src, resolved.RawBytes)
		if parseErr != nil && resolved.Origin == OriginHTTP200 {
			// If fresh HTTP 200 payload failed parsing and we have a distinct cached copy, fall back to cache.
			ext, _, _ := RawExtAndContentType(src.Format)
			allowLegacyFlat := src.Format != "billboard_csv"
			if cachedRaw, _, cacheErr := loadCachedRawBytes(ctx, src, ext, prevEntry, opts.Store, opts.ArchiveDir, allowLegacyFlat); cacheErr == nil && !bytes.Equal(cachedRaw, resolved.RawBytes) {
				if cTracks, cDM, cCSV, cParseErr := ParseRawSource(src, cachedRaw); cParseErr == nil {
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
					tracks, dm, csvBytes, parseErr = cTracks, cDM, cCSV, nil
				}
			}
		}
		if parseErr != nil {
			if src.Required {
				return nil, fmt.Errorf("parse required source %q (%s): %w", src.ID, src.Format, parseErr)
			}
			logf("Warning: parse optional source %q failed: %v", src.ID, parseErr)
			continue
		}

		for k, v := range dm {
			if _, exists := discMap[k]; !exists || src.ID == "historical-discs" {
				discMap[k] = v
			}
		}
		allTracks = append(allTracks, tracks...)

		csvFilename := src.ID + ".csv"
		sourceURL := src.URL
		if sourceURL == "" {
			sourceURL = src.LocalPath
		}
		entry := ArchivedSource{
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
			TracksCount:    len(tracks),
			DiscsCount:     len(dm),
			FetchedAt:      resolved.FetchedAt,
			ETag:           resolved.ETag,
			LastModified:   resolved.LastModified,
			Origin:         resolved.Origin,
			Stale:          resolved.Stale,
			FallbackReason: resolved.FallbackReason,
		}
		if entry.Stale {
			staleSources = append(staleSources, entry.ID)
		}
		logf("Source %q (%s, %s): %d tracks, %d discs [sha256=%s]",
			entry.ID, entry.Lifecycle, entry.Origin, entry.TracksCount, entry.DiscsCount, entry.RawSHA256[:16])

		compiledSources = append(compiledSources, compiledSourceArtifact{
			config:   src,
			archived: entry,
			rawBytes: resolved.RawBytes,
			csvBytes: csvBytes,
		})
		archivedList = append(archivedList, entry)
	}

	dictGz, err := CompileDictionary(allTracks)
	if err != nil {
		return nil, fmt.Errorf("compile dictionary: %w", err)
	}
	var dictPayload struct {
		Tracks map[string]any `json:"tracks"`
	}
	if err := DecodeGzipJSON(dictGz, &dictPayload); err != nil {
		return nil, fmt.Errorf("decode compiled dictionary: %w", err)
	}
	totalUniqueTracks := len(dictPayload.Tracks)

	EnrichDiscMap(discMap, allTracks)
	discGz, err := CompileDiscMap(discMap)
	if err != nil {
		return nil, fmt.Errorf("compile disc map: %w", err)
	}

	inputsHash := ComputeInputsHash(archivedList)
	shortInputs := strings.TrimPrefix(inputsHash, "sha256:")
	if len(shortInputs) > 8 {
		shortInputs = shortInputs[:8]
	}
	buildID := fmt.Sprintf("%s-%s", now.Format("20060102T150405Z"), shortInputs)

	manifest := SourceManifest{
		Version:          2,
		BuildID:          buildID,
		CompiledAt:       now.Format(time.RFC3339),
		InputsHash:       inputsHash,
		DictionaryTracks: totalUniqueTracks,
		MappedDiscs:      len(discMap),
		Degraded:         len(staleSources) > 0,
		StaleSources:     staleSources,
		Sources:          archivedList,
	}

	if err := VerifyGuardrails(cfg, compiledSources, &manifest, prevManifest, now); err != nil {
		return nil, err
	}

	if err := writeLocalArtifacts(opts.OutDir, opts.ArchiveDir, compiledSources, dictGz, discGz, manifest); err != nil {
		return nil, err
	}

	return &PipelineResult{
		Manifest:       manifest,
		DictionaryGzip: dictGz,
		DiscMapGzip:    discGz,
	}, nil
}

// VerifyGuardrails enforces per-source track/disc floors, per-source & global shrink thresholds vs. prevManifest,
// and rolling source max_stale_days limits.
func VerifyGuardrails(
	cfg *SourcesConfig,
	sources []compiledSourceArtifact,
	curr *SourceManifest,
	prev *SourceManifest,
	now time.Time,
) error {
	for _, item := range sources {
		srcCfg := item.config
		arch := item.archived

		if srcCfg.MinTracks > 0 && arch.TracksCount < srcCfg.MinTracks {
			return fmt.Errorf("guardrail failed: source %q parsed %d tracks, below min_tracks floor %d",
				arch.ID, arch.TracksCount, srcCfg.MinTracks)
		}
		if srcCfg.MinDiscs > 0 && arch.DiscsCount < srcCfg.MinDiscs {
			return fmt.Errorf("guardrail failed: source %q parsed %d discs, below min_discs floor %d",
				arch.ID, arch.DiscsCount, srcCfg.MinDiscs)
		}

		if prev != nil && srcCfg.MaxDropPct > 0 {
			if prevSrc, ok := prev.FindSource(arch.ID); ok {
				if prevSrc.TracksCount > 0 {
					dropPct := float64(prevSrc.TracksCount-arch.TracksCount) * 100.0 / float64(prevSrc.TracksCount)
					if dropPct > srcCfg.MaxDropPct {
						return fmt.Errorf("guardrail failed: source %q tracks dropped by %.2f%% (%d -> %d), exceeding max_drop_pct %.2f%%",
							arch.ID, dropPct, prevSrc.TracksCount, arch.TracksCount, srcCfg.MaxDropPct)
					}
				}
				if prevSrc.DiscsCount > 0 && srcCfg.MinDiscs > 0 {
					dropPct := float64(prevSrc.DiscsCount-arch.DiscsCount) * 100.0 / float64(prevSrc.DiscsCount)
					if dropPct > srcCfg.MaxDropPct {
						return fmt.Errorf("guardrail failed: source %q discs dropped by %.2f%% (%d -> %d), exceeding max_drop_pct %.2f%%",
							arch.ID, dropPct, prevSrc.DiscsCount, arch.DiscsCount, srcCfg.MaxDropPct)
					}
				}
			}
		}

		if arch.Stale && srcCfg.MaxStaleDays > 0 {
			fetchedTime, err := time.Parse(time.RFC3339, arch.FetchedAt)
			if err != nil {
				return fmt.Errorf("guardrail failed: rolling source %q is stale and has invalid/missing fetched_at %q", arch.ID, arch.FetchedAt)
			}
			ageDays := now.Sub(fetchedTime).Hours() / 24.0
			if ageDays > float64(srcCfg.MaxStaleDays) {
				return fmt.Errorf("guardrail failed: rolling source %q is stale (last fetched %s, %.1f days ago > max_stale_days %d; fallback_reason=%s)",
					arch.ID, arch.FetchedAt, ageDays, srcCfg.MaxStaleDays, arch.FallbackReason)
			}
		}
	}

	if prev != nil && cfg != nil && cfg.MaxGlobalDropPct > 0 {
		if prev.DictionaryTracks > 0 {
			dropPct := float64(prev.DictionaryTracks-curr.DictionaryTracks) * 100.0 / float64(prev.DictionaryTracks)
			if dropPct > cfg.MaxGlobalDropPct {
				return fmt.Errorf("guardrail failed: global dictionary_tracks dropped by %.2f%% (%d -> %d), exceeding max_global_drop_pct %.2f%%",
					dropPct, prev.DictionaryTracks, curr.DictionaryTracks, cfg.MaxGlobalDropPct)
			}
		}
		if prev.MappedDiscs > 0 {
			dropPct := float64(prev.MappedDiscs-curr.MappedDiscs) * 100.0 / float64(prev.MappedDiscs)
			if dropPct > cfg.MaxGlobalDropPct {
				return fmt.Errorf("guardrail failed: global mapped_discs dropped by %.2f%% (%d -> %d), exceeding max_global_drop_pct %.2f%%",
					dropPct, prev.MappedDiscs, curr.MappedDiscs, cfg.MaxGlobalDropPct)
			}
		}
	}

	return nil
}

func loadPriorManifest(ctx context.Context, store Store, archiveDir string) (*SourceManifest, error) {
	if store != nil {
		obj, err := store.GetObject(ctx, ManifestObjectKey, 0)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		} else if len(obj.Body) > 0 {
			var m SourceManifest
			if decErr := DecodeGzipJSON(obj.Body, &m); decErr != nil {
				return nil, fmt.Errorf("decode S3 %s: %w", ManifestObjectKey, decErr)
			}
			return &m, nil
		}
	}
	if strings.TrimSpace(archiveDir) != "" {
		raw, err := os.ReadFile(filepath.Join(archiveDir, "manifest.json"))
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			return nil, os.ErrNotExist
		}
		var m SourceManifest
		if decErr := DecodeGzipJSON(raw, &m); decErr != nil {
			return nil, fmt.Errorf("decode local manifest.json: %w", decErr)
		}
		return &m, nil
	}
	return nil, os.ErrNotExist
}

func writeLocalArtifacts(outDir, archiveDir string, sources []compiledSourceArtifact, dictGz, discGz []byte, manifest SourceManifest) error {
	if strings.TrimSpace(outDir) != "" {
		if err := os.MkdirAll(outDir, 0755); err != nil {
			return fmt.Errorf("create output dir %q: %w", outDir, err)
		}
		if err := os.WriteFile(filepath.Join(outDir, "dictionary.json.gz"), dictGz, 0644); err != nil {
			return fmt.Errorf("write dictionary.json.gz: %w", err)
		}
		if err := os.WriteFile(filepath.Join(outDir, "disc_map.json.gz"), discGz, 0644); err != nil {
			return fmt.Errorf("write disc_map.json.gz: %w", err)
		}
	}
	if strings.TrimSpace(archiveDir) != "" {
		if err := os.MkdirAll(archiveDir, 0755); err != nil {
			return fmt.Errorf("create archive dir %q: %w", archiveDir, err)
		}
		for _, item := range sources {
			rawDir := filepath.Join(archiveDir, "raw", item.archived.ID)
			if err := os.MkdirAll(rawDir, 0755); err != nil {
				return fmt.Errorf("create raw archive subdir %q: %w", rawDir, err)
			}
			if err := os.WriteFile(filepath.Join(rawDir, item.archived.RawFilename), item.rawBytes, 0644); err != nil {
				return fmt.Errorf("write raw archive %q: %w", item.archived.RawFilename, err)
			}
			if err := os.WriteFile(filepath.Join(archiveDir, item.archived.CSVFilename), item.csvBytes, 0644); err != nil {
				return fmt.Errorf("write csv archive %q: %w", item.archived.CSVFilename, err)
			}
		}
		rawManifest, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal manifest.json: %w", err)
		}
		if err := os.WriteFile(filepath.Join(archiveDir, "manifest.json"), rawManifest, 0644); err != nil {
			return fmt.Errorf("write manifest.json: %w", err)
		}
	}
	return nil
}
