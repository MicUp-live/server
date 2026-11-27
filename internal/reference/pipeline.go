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
	now, sourcesPath, logf := resolvePipelineDefaults(opts)
	cfg, err := LoadSourcesConfig(sourcesPath)
	if err != nil {
		return nil, err
	}

	prevManifest, err := loadPriorManifest(ctx, opts.Store, opts.ArchiveDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load prior manifest: %w", err)
	}

	collected, err := collectPipelineSources(ctx, cfg, prevManifest, opts, now, logf)
	if err != nil {
		return nil, err
	}

	dictGz, totalUniqueTracks, discGz, err := compileCatalogArtifacts(collected.allTracks, collected.discMap)
	if err != nil {
		return nil, err
	}

	manifest := buildSourceManifest(now, totalUniqueTracks, len(collected.discMap), collected.staleSources, collected.archivedList)
	if err := VerifyGuardrails(cfg, collected.compiledSources, &manifest, prevManifest, now); err != nil {
		return nil, err
	}

	if err := writeLocalArtifacts(opts.OutDir, opts.ArchiveDir, collected.compiledSources, dictGz, discGz, manifest); err != nil {
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
		if err := verifySourceGuardrails(item, prev, now); err != nil {
			return err
		}
	}
	return verifyGlobalDropGuardrails(cfg, curr, prev)
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
