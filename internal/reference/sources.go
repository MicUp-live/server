package reference

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	LifecycleImmutable = "immutable"
	LifecycleRolling   = "rolling"

	OriginLocalFile  = "local_file"
	OriginLocalCache = "local_cache"
	OriginS3Cache    = "s3_cache"
	OriginHTTP200    = "http_200"
	OriginHTTP304    = "http_304"
	OriginS3Fallback = "s3_fallback"
)

// SourceConfig describes an upstream or local reference catalog feed in sources.json (v2).
type SourceConfig struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Provider     string  `json:"provider"`
	Genre        string  `json:"genre,omitempty"`
	Format       string  `json:"format"`    // "karafun_csv", "zoom_csv", "disc_csv", "pdf", "xlsx", "billboard_csv"
	Lifecycle    string  `json:"lifecycle"` // "immutable" or "rolling"
	Required     bool    `json:"required"`
	URL          string  `json:"url,omitempty"`
	LocalPath    string  `json:"local_path,omitempty"`
	MinTracks    int     `json:"min_tracks"`
	MinDiscs     int     `json:"min_discs"`
	MaxDropPct   float64 `json:"max_drop_pct"`
	MaxStaleDays int     `json:"max_stale_days,omitempty"`
	Optional     bool    `json:"optional,omitempty"` // legacy v1 compatibility
}

// RemoteSourceConfig aliases SourceConfig for backwards compatibility.
type RemoteSourceConfig = SourceConfig

// SourcesConfig holds the declarative v2 upstream and local catalog feeds.
type SourcesConfig struct {
	Version          int            `json:"version,omitempty"`
	MaxGlobalDropPct float64        `json:"max_global_drop_pct,omitempty"`
	Sources          []SourceConfig `json:"sources,omitempty"`
	RemoteSources    []SourceConfig `json:"remote_sources,omitempty"` // legacy v1 compatibility
}

// AllSources returns the unified source slice from v2 ("sources") or v1 ("remote_sources"),
// normalizing defaults for lifecycle and required flags.
func (c *SourcesConfig) AllSources() []SourceConfig {
	if c == nil {
		return nil
	}
	raw := c.Sources
	if len(raw) == 0 {
		raw = c.RemoteSources
	}
	out := make([]SourceConfig, 0, len(raw))
	for _, s := range raw {
		s.ID = strings.ToLower(strings.TrimSpace(s.ID))
		if s.ID == "" {
			s.ID = FormatSourceSlug(s.Name)
		}
		s.Lifecycle = strings.ToLower(strings.TrimSpace(s.Lifecycle))
		if s.Lifecycle == "" {
			s.Lifecycle = LifecycleRolling
		}
		s.Format = strings.ToLower(strings.TrimSpace(s.Format))
		if c.Version < 2 && !s.Required && !s.Optional {
			s.Required = true
		}
		out = append(out, s)
	}
	return out
}

// LoadSourcesConfig reads and validates a v2 (or v1) sources.json manifest.
func LoadSourcesConfig(path string) (*SourcesConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sources config %q: %w", path, err)
	}
	var cfg SourcesConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse sources config %q: %w", path, err)
	}
	cfg.Sources = cfg.AllSources()
	if len(cfg.Sources) == 0 {
		return nil, fmt.Errorf("sources config %q has no sources", path)
	}
	seen := make(map[string]bool, len(cfg.Sources))
	for _, s := range cfg.Sources {
		if s.ID == "" || strings.Contains(s.ID, "/") || strings.Contains(s.ID, "\\") || strings.Contains(s.ID, "..") {
			return nil, fmt.Errorf("invalid source id %q in %q", s.ID, path)
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("duplicate source id %q in %q", s.ID, path)
		}
		seen[s.ID] = true
		if s.Lifecycle != LifecycleImmutable && s.Lifecycle != LifecycleRolling {
			return nil, fmt.Errorf("source %q has invalid lifecycle %q (want %q or %q)", s.ID, s.Lifecycle, LifecycleImmutable, LifecycleRolling)
		}
		switch s.Format {
		case "karafun_csv", "zoom_csv", "disc_csv", "pdf", "xlsx", "billboard_csv":
		default:
			return nil, fmt.Errorf("source %q has unsupported format %q", s.ID, s.Format)
		}
	}
	if len(cfg.RemoteSources) == 0 {
		cfg.RemoteSources = cfg.Sources
	}
	if cfg.MaxGlobalDropPct <= 0 {
		cfg.MaxGlobalDropPct = 5.0
	}
	return &cfg, nil
}

// RawExtAndContentType returns the canonical file extension, MIME type, and short format label for a SourceConfig.Format (or extension).
func RawExtAndContentType(format string) (ext string, contentType string, shortFormat string) {
	switch strings.ToLower(strings.TrimPrefix(strings.TrimSpace(format), ".")) {
	case "pdf":
		return ".pdf", "application/pdf", "pdf"
	case "xlsx":
		return ".xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx"
	default:
		return ".csv", "text/csv; charset=utf-8", "csv"
	}
}

// ContentAddressedRawKey computes the content-addressed S3 key for raw upstream bytes:
// "reference/v1/sources/raw/<id>/<sha256_16><ext>" along with the full hex SHA-256 and filename.
func ContentAddressedRawKey(id, ext string, rawBytes []byte) (rawKey, rawFilename, fullSHA256 string) {
	sum := sha256.Sum256(rawBytes)
	fullSHA256 = hex.EncodeToString(sum[:])
	shortHash := fullSHA256[:16]
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	rawFilename = shortHash + ext
	rawKey = fmt.Sprintf("reference/v1/sources/raw/%s/%s", id, rawFilename)
	return rawKey, rawFilename, fullSHA256
}

// ComputeInputsHash computes a deterministic SHA-256 over sorted "<id>:<raw_sha256>" pairs.
func ComputeInputsHash(sources []ArchivedSource) string {
	pairs := make([]string, 0, len(sources))
	for _, s := range sources {
		pairs = append(pairs, s.ID+":"+s.RawSHA256)
	}
	sort.Strings(pairs)
	sum := sha256.Sum256([]byte(strings.Join(pairs, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FormatSourceSlug normalizes a source name or filename into a URL-safe slug ID.
func FormatSourceSlug(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimSuffix(s, filepath.Ext(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
