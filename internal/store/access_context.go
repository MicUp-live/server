package store

import "context"

type accessContextKey string

const (
	songbookEditActorKey accessContextKey = "songbook-edit-actor"
	songbookHostFenceKey accessContextKey = "songbook-host-fence"
)

// SongbookHostFence requests commit-time DynamoDB validation that ActorID
// currently holds hosting authority on SongbookID and that SongbookID is not deleting.
type SongbookHostFence struct {
	SongbookID string
	ActorID    string
	IsOwner    bool
}

// WithSongbookEditActor attaches a collaborator actor ID whose edit grant must
// be verified at commit time for catalog writes on a shared songbook.
func WithSongbookEditActor(ctx context.Context, actorID string) context.Context {
	if actorID == "" {
		return ctx
	}
	return context.WithValue(ctx, songbookEditActorKey, actorID)
}

// SongbookEditActorFromContext returns the collaborator actor ID requiring an
// edit-grant commit fence, if set.
func SongbookEditActorFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(songbookEditActorKey).(string)
	return v
}

// WithSongbookHostFence attaches commit-time hosting validation requirements
// for event creation, songbook reassignment, and ended-show reactivation.
func WithSongbookHostFence(ctx context.Context, fence SongbookHostFence) context.Context {
	if fence.SongbookID == "" || fence.ActorID == "" {
		return ctx
	}
	return context.WithValue(ctx, songbookHostFenceKey, fence)
}

// SongbookHostFenceFromContext returns the commit-time hosting fence, if set.
func SongbookHostFenceFromContext(ctx context.Context) (SongbookHostFence, bool) {
	if ctx == nil {
		return SongbookHostFence{}, false
	}
	v, ok := ctx.Value(songbookHostFenceKey).(SongbookHostFence)
	return v, ok && v.SongbookID != "" && v.ActorID != ""
}
