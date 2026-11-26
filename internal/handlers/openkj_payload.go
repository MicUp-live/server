package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// OpenKJSong represents an incoming track from an OpenKJ sync.
type OpenKJSong struct {
	Artist string `json:"artist"`
	Title  string `json:"title"`
	SongID string `json:"songid,omitempty"`
	Disc   string `json:"disc,omitempty"`
}

// OpenKJRequestDTO formats MicUp requests for the OpenKJ desktop client.
type OpenKJRequestDTO struct {
	RequestID   int    `json:"request_id"`
	ID          string `json:"id"`
	Singer      string `json:"singer"`
	Artist      string `json:"artist"`
	Title       string `json:"title"`
	KeyChange   int    `json:"key_change"`
	Key         int    `json:"key"`
	Notes       string `json:"notes"`
	RequestTime int64  `json:"request_time"`
	Timestamp   int64  `json:"timestamp"`
}

// openKJRequestID maps a MicUp string request ID to a deterministic positive
// 32-bit integer expected by OpenKJ's OkjsRequest.requestId field.
func openKJRequestID(id string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	n := int(h.Sum32() & 0x7fffffff)
	if n == 0 {
		return 1
	}
	return n
}

// putOpenKJSongs writes OpenKJ songs into the catalog-changes overlay with bounded concurrency.
func putOpenKJSongs(ctx context.Context, changes store.CatalogChangeStore, songbookID string, rawSongs []OpenKJSong) (int, error) {
	now := time.Now().UTC()
	pending := make([]*models.CatalogChange, 0, len(rawSongs))
	for _, item := range rawSongs {
		artist := strings.TrimSpace(item.Artist)
		title := strings.TrimSpace(item.Title)
		if artist == "" && title == "" {
			continue
		}
		disc := strings.TrimSpace(item.Disc)
		if disc == "" {
			disc = strings.TrimSpace(item.SongID)
		}

		hasher := sha256.New()
		hasher.Write([]byte(fmt.Sprintf("%s:%s:%s", strings.ToLower(artist), strings.ToLower(title), strings.ToLower(disc))))
		fp := "okj-" + hex.EncodeToString(hasher.Sum(nil))[:16]

		label := models.BuildLabel(artist, title)
		searchText := models.BuildSearchText(artist, title, disc, label, "")
		song := &models.Song{
			ID:          fp,
			SongbookID:  songbookID,
			Artist:      artist,
			Title:       title,
			Disc:        disc,
			Label:       label,
			SearchText:  searchText,
			Fingerprint: fp,
			IsAvailable: true,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		pending = append(pending, &models.CatalogChange{
			SongbookID: songbookID,
			SongID:     song.ID,
			Song:       song,
		})
	}
	if len(pending) == 0 {
		return 0, nil
	}

	const maxConcurrency = 16
	workers := maxConcurrency
	if len(pending) < workers {
		workers = len(pending)
	}

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan *models.CatalogChange, len(pending))
	for _, ch := range pending {
		jobs <- ch
	}
	close(jobs)

	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ch := range jobs {
				if workCtx.Err() != nil {
					return
				}
				if err := changes.PutCatalogChange(workCtx, ch); err != nil {
					errOnce.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return 0, firstErr
	}
	return len(pending), nil
}

type openkjPayloadKey struct{}

type openkjBodyParams struct {
	Action    string
	Key       string
	Accepting *bool
	RequestID string
}

// attachOpenKJBodyParams peeks JSON or form parameters from POST bodies while preserving r.Body for downstream handlers.
func attachOpenKJBodyParams(r *http.Request) *http.Request {
	if r.Method != http.MethodPost || r.Body == nil {
		return r
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(ct, "application/x-www-form-urlencoded") {
		_ = r.ParseForm()
		return r
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, importBodyLimit))
	if err != nil {
		return r
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return r
	}
	if trimmed[0] != '{' {
		if !strings.Contains(ct, "application/json") && trimmed[0] != '[' {
			if values, err := url.ParseQuery(string(body)); err == nil {
				params := openkjBodyParams{
					Action:    firstNonEmpty(values.Get("action"), values.Get("command")),
					Key:       firstNonEmpty(values.Get("key"), values.Get("api_key"), values.Get("venue"), values.Get("venue_id")),
					RequestID: firstNonEmpty(values.Get("request_id"), values.Get("id")),
				}
				if acc := firstNonEmpty(values.Get("accepting")); acc != "" {
					b := acc == "1" || strings.EqualFold(acc, "true")
					params.Accepting = &b
				}
				return r.WithContext(context.WithValue(r.Context(), openkjPayloadKey{}, params))
			}
		}
		return r
	}

	var raw map[string]any
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return r
	}

	params := openkjBodyParams{
		Action:    firstNonEmpty(jsonStringValue(raw["action"]), jsonStringValue(raw["command"])),
		Key:       firstNonEmpty(jsonStringValue(raw["key"]), jsonStringValue(raw["api_key"]), jsonStringValue(raw["venue"]), jsonStringValue(raw["venue_id"])),
		Accepting: jsonBoolValue(raw["accepting"]),
		RequestID: firstNonEmpty(jsonStringValue(raw["request_id"]), jsonStringValue(raw["id"])),
	}
	return r.WithContext(context.WithValue(r.Context(), openkjPayloadKey{}, params))
}

func openkjParamsFromContext(r *http.Request) openkjBodyParams {
	params, _ := r.Context().Value(openkjPayloadKey{}).(openkjBodyParams)
	return params
}

func jsonStringValue(v any) string {
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case float64:
		return strconv.FormatInt(int64(val), 10)
	default:
		return ""
	}
}

func jsonBoolValue(v any) *bool {
	switch val := v.(type) {
	case bool:
		b := val
		return &b
	case float64:
		b := val != 0
		return &b
	case string:
		trimmed := strings.TrimSpace(val)
		if trimmed == "" {
			return nil
		}
		b := trimmed == "1" || strings.EqualFold(trimmed, "true")
		return &b
	default:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeOpenKJAction(action string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(action)), "_", "")
}
