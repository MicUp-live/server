// Package app composes the MicUp HTTP application from domain dependencies.
package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"micup.live/server/internal/account"
	"micup.live/server/internal/catalog"
	"micup.live/server/internal/config"
	"micup.live/server/internal/email"
	"micup.live/server/internal/event"
	"micup.live/server/internal/handlers"
	"micup.live/server/internal/importjob"
	"micup.live/server/internal/models"
	"micup.live/server/internal/queue"
	"micup.live/server/internal/reference"
	"micup.live/server/internal/songbook"
	"micup.live/server/internal/store"
)

// Dependencies contains the persistence boundaries used by HTTP handlers.
type Dependencies struct {
	Auth          store.AuthStore
	Favorites     store.FavoritesStore
	Passkeys      store.PasskeyStore
	Songbooks     store.SongbookStore
	Requests      store.RequestStore
	Events        store.EventStore
	CatalogJobs   catalog.JobQueue
	Catalogs      store.CatalogStore
	CatalogReader interface {
		ListSongs(context.Context, string, string) ([]*models.Song, error)
	}
	CatalogChanges store.CatalogChangeStore
	ImportJobs     store.ImportJobStore
	ImportQueue    interface {
		Enqueue(context.Context, importjob.Job) error
	}
	ManifestWriter interface {
		Put(context.Context, string, []byte) error
		Presign(context.Context, string, int64) (string, error)
		DeleteKey(context.Context, string) error
		DeletePrefix(context.Context, string) error
	}
	ReferenceStore reference.Store
	DeveloperKeys  store.DeveloperKeyStore
	RateLimits     store.RateLimitStore
	SongbookAccess store.SongbookAccessStore
	EmailSender    email.Sender
}

// NewRouter builds the complete API route and middleware stack.
func NewRouter(cfg config.Config, dependencies Dependencies) (http.Handler, error) {
	songbookAccess := dependencies.SongbookAccess
	retentionDuration := time.Duration(defaultInt(cfg.QueueRetentionDays, 14)) * 24 * time.Hour

	authHandler, privacyHandler, err := newAuthAndPrivacyHandlers(cfg, dependencies, songbookAccess, retentionDuration)
	if err != nil {
		return nil, err
	}

	var passkeyHandler *handlers.PasskeyHandler
	if dependencies.Passkeys != nil {
		passkeyHandler, err = handlers.NewPasskeyHandler(dependencies.Auth, dependencies.Passkeys, authHandler)
		if err != nil {
			return nil, err
		}
	}
	songbookService := songbook.NewService(dependencies.Songbooks, dependencies.Events)
	songbookService.Access = songbookAccess
	songbookService.Auth = dependencies.Auth
	songbookService.CatalogChanges = dependencies.CatalogChanges
	songbookService.ImportJobs = dependencies.ImportJobs
	songbookService.Requests = dependencies.Requests
	songbookService.Catalogs = dependencies.Catalogs
	songbookService.Assets = dependencies.ManifestWriter
	songbookService.EmailSender = authHandler.EmailSender
	songbookService.QueueRetention = retentionDuration

	catalogService := catalog.NewService(
		dependencies.Songbooks,
		dependencies.Events,
		dependencies.CatalogJobs,
		dependencies.Catalogs,
		dependencies.CatalogReader,
		dependencies.CatalogChanges,
		dependencies.ImportJobs,
		dependencies.ImportQueue,
		dependencies.ManifestWriter,
	).WithAccess(songbookAccess)

	songbookHandler := &handlers.SongbookHandler{
		Events:          dependencies.Events,
		Songbooks:       dependencies.Songbooks,
		CatalogJobs:     dependencies.CatalogJobs,
		Catalogs:        dependencies.Catalogs,
		CatalogReader:   dependencies.CatalogReader,
		CatalogChanges:  dependencies.CatalogChanges,
		ImportJobs:      dependencies.ImportJobs,
		ImportQueue:     dependencies.ImportQueue,
		ManifestWriter:  dependencies.ManifestWriter,
		Access:          songbookAccess,
		Auth:            dependencies.Auth,
		Requests:        dependencies.Requests,
		Assets:          dependencies.ManifestWriter,
		EmailSender:     authHandler.EmailSender,
		PublicBaseURL:   cfg.PublicBaseURL,
		QueueRetention:  retentionDuration,
		SongbookService: songbookService,
		CatalogService:  catalogService,
	}
	eventService := event.NewService(dependencies.Events, dependencies.Songbooks, dependencies.Requests, retentionDuration).WithAccess(songbookAccess)
	queueService := queue.NewService(dependencies.Requests, dependencies.Events, []byte(cfg.AuthSecret))

	eventHandler := &handlers.EventHandler{
		Events:         dependencies.Events,
		Songbooks:      dependencies.Songbooks,
		Requests:       dependencies.Requests,
		Access:         songbookAccess,
		QueueRetention: retentionDuration,
		EventService:   eventService,
	}
	queueHandler := &handlers.QueueHandler{
		Events:    dependencies.Events,
		Songbooks: dependencies.Songbooks,
		Requests:  dependencies.Requests,
		Secret:    []byte(cfg.AuthSecret),
		QueueSvc:  queueService,
	}
	openkjHandler := &handlers.OpenKJHandler{
		Events:         dependencies.Events,
		Songbooks:      dependencies.Songbooks,
		CatalogChanges: dependencies.CatalogChanges,
		Requests:       dependencies.Requests,
		Access:         songbookAccess,
		Auth:           dependencies.Auth,
		QueueSvc:       queueService,
		EventSvc:       eventService,
	}
	var developerHandler *handlers.DeveloperHandler
	if dependencies.DeveloperKeys != nil {
		developerHandler = handlers.NewDeveloperHandler(dependencies.DeveloperKeys, dependencies.Auth)
		developerHandler.ScopeMode = cfg.DeveloperKeyScopeMode
	}
	mux := http.NewServeMux()
	authLimited := newRateLimiter(dependencies.RateLimits, "auth", defaultInt(cfg.AuthRateLimit, 10), time.Minute).middleware
	requestLimited := newRateLimiter(dependencies.RateLimits, "request", defaultInt(cfg.PublicRequestLimit, 12), time.Minute).middleware
	inviteLimited := newRateLimiter(dependencies.RateLimits, "invite", defaultInt(cfg.AuthRateLimit, 10), time.Minute).middleware

	registerHealthAndAuthRoutes(mux, dependencies, authHandler, privacyHandler, passkeyHandler, authLimited)

	guards := routeGuards{
		auth:      authHandler,
		developer: developerHandler,
		songbook:  songbookHandler,
		event:     eventHandler,
	}

	mux.HandleFunc("POST /api/v1/songbooks", guards.scopedHost(handlers.DeveloperScopeCatalogsWrite, songbookHandler.CreateSongbook))
	mux.HandleFunc("GET /api/v1/songbooks", guards.scopedHost(handlers.DeveloperScopeCatalogsWrite, songbookHandler.ListSongbooks))
	mux.HandleFunc("GET /api/v1/songbooks/{id}", guards.discoverBook(handlers.DeveloperScopeCatalogsWrite, songbookHandler.GetSongbook))
	mux.HandleFunc("PATCH /api/v1/songbooks/{id}", guards.manageBook(handlers.DeveloperScopeCatalogsWrite, songbookHandler.UpdateSongbook))
	mux.HandleFunc("DELETE /api/v1/songbooks/{id}", guards.manageBook(handlers.DeveloperScopeCatalogsWrite, songbookHandler.DeleteSongbook))

	// Songbook collaboration access and invitation routes (host session required)
	mux.HandleFunc("GET /api/v1/songbooks/{id}/access", guards.manageAccess(songbookHandler.GetSongbookAccess))
	mux.HandleFunc("POST /api/v1/songbooks/{id}/invitations", inviteLimited(guards.manageAccess(songbookHandler.CreateSongbookInvitation)))
	mux.HandleFunc("POST /api/v1/songbooks/{id}/invitations/{invitationID}/resend", inviteLimited(guards.manageAccess(songbookHandler.ResendSongbookInvitation)))
	mux.HandleFunc("DELETE /api/v1/songbooks/{id}/invitations/{invitationID}", guards.manageAccess(songbookHandler.CancelSongbookInvitation))
	mux.HandleFunc("PATCH /api/v1/songbooks/{id}/members/{userID}", guards.manageAccess(songbookHandler.UpdateSongbookMember))
	mux.HandleFunc("DELETE /api/v1/songbooks/{id}/members/{userID}", guards.manageAccess(songbookHandler.RemoveSongbookMember))
	mux.HandleFunc("GET /api/v1/songbook-invitations/preview", authLimited(authHandler.OptionalAuth(songbookHandler.PreviewSongbookInvitation)))
	mux.HandleFunc("POST /api/v1/songbook-invitations/accept", authHandler.RequireAuth(songbookHandler.AcceptSongbookInvitation))

	mux.HandleFunc("POST /api/v1/songbooks/{id}/songs", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.CreateSong))
	mux.HandleFunc("GET /api/v1/songbooks/{id}/songs", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.ListSongs))
	mux.HandleFunc("GET /api/v1/songbooks/{id}/managed-manifest", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.GetManagedManifest))
	mux.HandleFunc("GET /api/v1/songbooks/{id}/catalog-overrides", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.GetCatalogOverrides))
	mux.HandleFunc("POST /api/v1/songbooks/{id}/songs/batch-delete", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.BatchDeleteSongs))
	mux.HandleFunc("POST /api/v1/songbooks/{id}/songs/clear", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.ClearSongs))
	mux.HandleFunc("GET /api/v1/songbooks/{id}/songs/{songID}", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.GetSong))
	mux.HandleFunc("PATCH /api/v1/songbooks/{id}/songs/{songID}", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.UpdateSong))
	mux.HandleFunc("DELETE /api/v1/songbooks/{id}/songs/{songID}", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.DeleteSong))
	mux.HandleFunc("GET /api/v1/songbooks/{id}/public-manifest", songbookHandler.GetPublicManifest)
	mux.HandleFunc("GET /api/v1/songbooks/{id}/public-overrides", songbookHandler.GetPublicOverrides)
	mux.HandleFunc("POST /api/v1/songbooks/{id}/import-jobs", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.CreateImportJob))
	mux.HandleFunc("POST /api/v1/songbooks/{id}/import-uploads", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.CreateImportUpload))
	mux.HandleFunc("POST /api/v1/songbooks/{id}/import-jobs/{jobID}/complete", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.CompleteImportUpload))
	mux.HandleFunc("GET /api/v1/songbooks/{id}/import-jobs/{jobID}", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.GetImportJob))
	mux.HandleFunc("GET /api/v1/songbooks/{id}/import-jobs/latest", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.GetLatestImportJob))
	mux.HandleFunc("POST /api/v1/songbooks/{id}/parse-files", guards.editCatalog(handlers.DeveloperScopeCatalogsWrite, songbookHandler.ParseFiles))

	// Events Management (Host)
	mux.HandleFunc("POST /api/v1/events", guards.scopedHost(handlers.DeveloperScopeEventsWrite, eventHandler.CreateEvent))
	mux.HandleFunc("GET /api/v1/events", guards.scopedHost(handlers.DeveloperScopeEventsRead, eventHandler.ListEvents))
	mux.HandleFunc("GET /api/v1/events/{id}", guards.ownedEvent(handlers.DeveloperScopeEventsRead, eventHandler.GetEvent))
	mux.HandleFunc("PATCH /api/v1/events/{id}", guards.ownedEvent(handlers.DeveloperScopeEventsWrite, eventHandler.UpdateEvent))
	mux.HandleFunc("POST /api/v1/events/{id}/restart", guards.ownedEvent(handlers.DeveloperScopeEventsWrite, eventHandler.RestartEvent))
	mux.HandleFunc("POST /api/v1/events/{id}/openkj-key/rotate", guards.ownedEvent(handlers.DeveloperScopeEventsWrite, eventHandler.RotateOpenKJKey))
	mux.HandleFunc("POST /api/v1/events/{id}/pause", guards.ownedEvent(handlers.DeveloperScopeEventsWrite, eventHandler.PauseEvent))
	mux.HandleFunc("POST /api/v1/events/{id}/resume", guards.ownedEvent(handlers.DeveloperScopeEventsWrite, eventHandler.ResumeEvent))
	mux.HandleFunc("DELETE /api/v1/events/{id}", guards.ownedEvent(handlers.DeveloperScopeEventsWrite, eventHandler.DeleteEvent))

	// Public Event & Join Code resolution
	mux.HandleFunc("GET /api/v1/join/{code}", requestLimited(eventHandler.GetEventByCode))
	mux.HandleFunc("GET /api/v1/events/{id}/public", eventHandler.GetPublicEvent)
	mux.HandleFunc("POST /api/v1/events/{id}/kiosk/verify-pin", requestLimited(eventHandler.VerifyKioskPIN))
	mux.HandleFunc("POST /api/v1/events/{id}/requests", requestLimited(queueHandler.CreateEventRequest))
	mux.HandleFunc("GET /api/v1/events/{id}/queue", queueHandler.GetEventPublicQueue)
	mux.HandleFunc("GET /api/v1/events/{id}/requests/{requestID}", authHandler.OptionalAuth(queueHandler.GetRequest))
	mux.HandleFunc("PATCH /api/v1/events/{id}/requests/{requestID}/song", requestLimited(queueHandler.ReplaceEventRequestSong))
	mux.HandleFunc("DELETE /api/v1/events/{id}/requests/{requestID}", authHandler.OptionalAuth(queueHandler.DeleteRequest))

	// Host Event Queue Management
	mux.HandleFunc("GET /api/v1/events/{id}/requests", guards.ownedEvent(handlers.DeveloperScopeRequestsRead, queueHandler.ListEventRequests))
	mux.HandleFunc("PATCH /api/v1/events/{id}/requests/{requestID}", guards.ownedEvent(handlers.DeveloperScopeRequestsWrite, queueHandler.UpdateEventRequest))
	mux.HandleFunc("POST /api/v1/events/{id}/requests/{requestID}/defer", guards.ownedEvent(handlers.DeveloperScopeRequestsWrite, queueHandler.DeferEventRequest))
	mux.HandleFunc("POST /api/v1/events/{id}/requests/reorder", guards.ownedEvent(handlers.DeveloperScopeRequestsWrite, queueHandler.ReorderEventRequests))

	registerOpenKJRoutes(mux, openkjHandler)

	// API Keys (Host Personal Access Token management)
	if developerHandler != nil {
		mux.HandleFunc("POST /api/v1/api-keys", guards.hostOnly(developerHandler.CreateKey))
		mux.HandleFunc("GET /api/v1/api-keys", guards.hostOnly(developerHandler.ListKeys))
		mux.HandleFunc("DELETE /api/v1/api-keys/{id}", guards.hostOnly(developerHandler.RevokeKey))
	}

	// Undocumented Reference Catalog & Source Archive API
	registerReferenceRoutes(mux, dependencies.ReferenceStore)

	return loggingMiddleware(corsMiddleware(mux, cfg.AllowedOrigins)), nil
}

func newAuthAndPrivacyHandlers(
	cfg config.Config,
	dependencies Dependencies,
	songbookAccess store.SongbookAccessStore,
	retentionDuration time.Duration,
) (*handlers.AuthHandler, *handlers.PrivacyHandler, error) {
	authHandler, err := handlers.NewAuthHandler(dependencies.Auth, cfg.AuthSecret)
	if err != nil {
		return nil, nil, err
	}
	authHandler.PublicBaseURL = cfg.PublicBaseURL
	authHandler.CookieSecure = cfg.Environment == config.Production
	if dependencies.EmailSender != nil {
		authHandler.EmailSender = dependencies.EmailSender
	}

	accountService := account.NewService(
		dependencies.Auth,
		dependencies.Passkeys,
		dependencies.Songbooks,
		dependencies.Events,
		dependencies.Requests,
		dependencies.Catalogs,
		dependencies.CatalogReader,
		dependencies.CatalogChanges,
		dependencies.ImportJobs,
		dependencies.ManifestWriter,
	).WithDeveloperKeys(dependencies.DeveloperKeys).WithAccess(songbookAccess)
	accountService.QueueRetention = retentionDuration

	privacyHandler := &handlers.PrivacyHandler{
		Auth:           dependencies.Auth,
		Passkeys:       dependencies.Passkeys,
		DeveloperKeys:  dependencies.DeveloperKeys,
		Songbooks:      dependencies.Songbooks,
		Events:         dependencies.Events,
		Requests:       dependencies.Requests,
		Catalogs:       dependencies.Catalogs,
		CatalogReader:  dependencies.CatalogReader,
		CatalogChanges: dependencies.CatalogChanges,
		ImportJobs:     dependencies.ImportJobs,
		Assets:         dependencies.ManifestWriter,
		Access:         songbookAccess,
		QueueRetention: retentionDuration,
		AuthHandler:    authHandler,
		AccountService: accountService,
	}
	return authHandler, privacyHandler, nil
}

func registerHealthAndAuthRoutes(
	mux *http.ServeMux,
	dependencies Dependencies,
	authHandler *handlers.AuthHandler,
	privacyHandler *handlers.PrivacyHandler,
	passkeyHandler *handlers.PasskeyHandler,
	authLimited func(http.HandlerFunc) http.HandlerFunc,
) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/v1/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v1/auth/register", authLimited(authHandler.Register))
	mux.HandleFunc("POST /api/v1/auth/login", authLimited(authHandler.Login))
	mux.HandleFunc("POST /api/v1/auth/refresh", authHandler.Refresh)
	mux.HandleFunc("GET /api/v1/auth/me", authHandler.RequireAuth(authHandler.Me))
	mux.HandleFunc("PATCH /api/v1/auth/me", authHandler.RequireAuth(authHandler.UpdateMe))
	mux.HandleFunc("POST /api/v1/auth/logout", authHandler.RequireAuth(authHandler.Logout))
	mux.HandleFunc("GET /api/v1/auth/me/export", authHandler.RequireAuth(privacyHandler.ExportData))
	mux.HandleFunc("DELETE /api/v1/auth/me", authHandler.RequireAccountDeletionAuth(privacyHandler.DeleteAccount))
	mux.HandleFunc("POST /api/v1/auth/verify-email/request", authHandler.RequireAuth(authLimited(authHandler.RequestEmailVerification)))
	mux.HandleFunc("POST /api/v1/auth/verify-email/confirm", authHandler.ConfirmEmailVerification)
	mux.HandleFunc("GET /api/v1/auth/verify-email/confirm", authHandler.ConfirmEmailVerification)
	mux.HandleFunc("POST /api/v1/auth/forgot-password", authLimited(authHandler.RequestPasswordReset))
	mux.HandleFunc("POST /api/v1/auth/reset-password", authLimited(authHandler.ConfirmPasswordReset))

	var favStore store.FavoritesStore = dependencies.Favorites
	if favStore == nil {
		favStore = dependencies.Auth
	}
	if favStore != nil {
		favHandler := handlers.NewFavoritesHandler(favStore)
		mux.HandleFunc("GET /api/v1/auth/favorites", authHandler.RequireAuth(favHandler.GetFavorites))
		mux.HandleFunc("PUT /api/v1/auth/favorites", authHandler.RequireAuth(favHandler.SaveFavorites))
	}

	if passkeyHandler != nil {
		mux.HandleFunc("POST /api/v1/auth/passkey/register-options", authLimited(passkeyHandler.RegisterOptions))
		mux.HandleFunc("POST /api/v1/auth/passkey/register-verify", authLimited(passkeyHandler.RegisterVerify))
		mux.HandleFunc("POST /api/v1/auth/passkey/login-options", authLimited(passkeyHandler.LoginOptions))
		mux.HandleFunc("POST /api/v1/auth/passkey/login-verify", authLimited(passkeyHandler.LoginVerify))
		mux.HandleFunc("GET /api/v1/auth/passkeys", authHandler.RequireAuth(passkeyHandler.ListPasskeys))
		mux.HandleFunc("DELETE /api/v1/auth/passkeys/{id}", authHandler.RequireAuth(passkeyHandler.DeletePasskey))
	}
}

func registerOpenKJRoutes(mux *http.ServeMux, openkjHandler *handlers.OpenKJHandler) {
	// OpenKJ Request-Server Compatibility Adapter
	mux.HandleFunc("GET /api/v1/openkj/api.php", openkjHandler.HandleAction)
	mux.HandleFunc("POST /api/v1/openkj/api.php", openkjHandler.HandleAction)
	mux.HandleFunc("GET /api/v1/openkj", openkjHandler.HandleAction)
	mux.HandleFunc("POST /api/v1/openkj", openkjHandler.HandleAction)
	mux.HandleFunc("GET /api/openkj/api.php", openkjHandler.HandleAction)
	mux.HandleFunc("POST /api/openkj/api.php", openkjHandler.HandleAction)
	mux.HandleFunc("GET /api/openkj", openkjHandler.HandleAction)
	mux.HandleFunc("POST /api/openkj", openkjHandler.HandleAction)
	mux.HandleFunc("GET /openkj/api.php", openkjHandler.HandleAction)
	mux.HandleFunc("POST /openkj/api.php", openkjHandler.HandleAction)
}

func registerReferenceRoutes(mux *http.ServeMux, referenceStore reference.Store) {
	referenceHandler := handlers.NewReferenceHandler(referenceStore)
	mux.HandleFunc("GET /api/v1/reference/sources", referenceHandler.ListSources)
	mux.HandleFunc("GET /api/v1/reference/sources/{id}", referenceHandler.GetSource)
	mux.HandleFunc("GET /api/v1/reference/dictionary", referenceHandler.GetDictionary)
	mux.HandleFunc("GET /api/v1/reference/discs", referenceHandler.GetDiscs)
	mux.HandleFunc("GET /api/v1/reference/discs/{code}", referenceHandler.GetDiscByCode)
}

type routeGuards struct {
	auth      *handlers.AuthHandler
	developer *handlers.DeveloperHandler
	songbook  *handlers.SongbookHandler
	event     *handlers.EventHandler
}

func (g routeGuards) hostOnly(next http.HandlerFunc) http.HandlerFunc {
	return g.auth.RequireAuth(g.auth.RequireHost(next))
}

func (g routeGuards) scopedHost(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ")
		if strings.HasPrefix(token, "mk_live_") && g.developer != nil {
			developerNext := next
			if scope != "" {
				developerNext = g.developer.RequireScope(scope, next)
			}
			g.developer.RequireDeveloperKey(developerNext).ServeHTTP(w, r)
			return
		}
		g.hostOnly(next).ServeHTTP(w, r)
	}
}

func (g routeGuards) discoverBook(scope string, next http.HandlerFunc) http.HandlerFunc {
	return g.scopedHost(scope, g.songbook.RequireAction(songbook.ActionDiscover, next))
}

func (g routeGuards) editCatalog(scope string, next http.HandlerFunc) http.HandlerFunc {
	return g.scopedHost(scope, g.songbook.RequireAction(songbook.ActionEditCatalog, next))
}

func (g routeGuards) manageBook(scope string, next http.HandlerFunc) http.HandlerFunc {
	return g.scopedHost(scope, g.songbook.RequireAction(songbook.ActionManageBook, next))
}

func (g routeGuards) manageAccess(next http.HandlerFunc) http.HandlerFunc {
	return g.hostOnly(g.songbook.RequireAction(songbook.ActionManageAccess, next))
}

func (g routeGuards) ownedEvent(scope string, next http.HandlerFunc) http.HandlerFunc {
	return g.scopedHost(scope, g.event.RequireOwnership(next))
}

func defaultInt(value, fallback int) int {
	if value < 1 {
		return fallback
	}
	return value
}
