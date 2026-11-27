// Package queue encapsulates domain logic and application workflows for live karaoke queues.
package queue

import (
	"sort"
	"strings"
	"time"

	"micup.live/server/internal/models"
)

// FairPriorityNewcomerCredit is the number of completed songs credited to a first-time
// singer in fair_priority mode so latecomers weave in quickly without starving regulars
// who have waited longer than this credit.
const FairPriorityNewcomerCredit = 5

// PlacementResult describes how a newly accepted or staged request was placed in the queue.
type PlacementResult struct {
	NeedsReorder bool
	ReorderIDs   []string
}

type fairPriorityContext struct {
	totalCompleted   int
	lastCompletedSeq map[string]int
	weights          map[string]int
}

func completionTime(req *models.SongRequest) time.Time {
	if req.CompletedAt != nil && !req.CompletedAt.IsZero() {
		return *req.CompletedAt
	}
	if !req.UpdatedAt.IsZero() {
		return req.UpdatedAt
	}
	return req.CreatedAt
}

func syncCompletedAt(req *models.SongRequest, oldStatus models.RequestStatus, now time.Time) {
	if req.Status == models.StatusCompleted {
		if oldStatus != models.StatusCompleted {
			completedAt := now
			req.CompletedAt = &completedAt
		} else if req.CompletedAt == nil || req.CompletedAt.IsZero() {
			fallback := completionTime(req)
			if fallback.IsZero() {
				fallback = now
			}
			req.CompletedAt = &fallback
		}
	} else if oldStatus == models.StatusCompleted {
		req.CompletedAt = nil
	}
}

func newFairPriorityContext(event *models.Event, completedReqs []*models.SongRequest) fairPriorityContext {
	ordered := make([]*models.SongRequest, len(completedReqs))
	copy(ordered, completedReqs)
	sort.SliceStable(ordered, func(i, j int) bool {
		ti := completionTime(ordered[i])
		tj := completionTime(ordered[j])
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		if !ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
		}
		return ordered[i].ID < ordered[j].ID
	})

	lastSeq := make(map[string]int, len(ordered))
	for i, req := range ordered {
		norm := strings.ToLower(strings.TrimSpace(req.SingerName))
		lastSeq[norm] = i + 1
	}

	var weights map[string]int
	if event != nil {
		weights = models.NormalizeSingerWeights(event.SingerWeights)
	}

	return fairPriorityContext{
		totalCompleted:   len(ordered),
		lastCompletedSeq: lastSeq,
		weights:          weights,
	}
}

func (fp fairPriorityContext) effectiveSeq(normSinger string) int {
	var baseSeq int
	if seq, hasSung := fp.lastCompletedSeq[normSinger]; hasSung {
		baseSeq = seq
	} else {
		baseSeq = fp.totalCompleted - FairPriorityNewcomerCredit
		if baseSeq < 0 {
			baseSeq = 0
		}
	}
	return baseSeq - fp.weights[normSinger]
}

func normalizeSingerName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func sortActiveByRank(reqs []*models.SongRequest) {
	sort.SliceStable(reqs, func(i, j int) bool {
		if reqs[i].Rank != reqs[j].Rank {
			return reqs[i].Rank < reqs[j].Rank
		}
		return reqs[i].CreatedAt.Before(reqs[j].CreatedAt)
	})
}

func effectiveCurrentRound(event *models.Event) int {
	if event.CurrentRound <= 0 {
		return 1
	}
	return event.CurrentRound
}

func stageBufferSize(activeCount int) int {
	if activeCount < 3 {
		return activeCount
	}
	return 3
}

func tailRank(activeQueue []*models.SongRequest, nowMilli int64) int64 {
	if len(activeQueue) == 0 {
		return nowMilli
	}
	lastRank := activeQueue[len(activeQueue)-1].Rank
	if nowMilli > lastRank {
		return nowMilli
	}
	return lastRank + 10_000_000
}

// PlaceAcceptedRequest assigns Rank, Round, and SingerTurn to a request entering the active queue,
// inserting it according to the event's RotationMode while strictly respecting the 3-position stage buffer.
// It returns whether the queue needs displacement reordering and the ordered request IDs if so.
func PlaceAcceptedRequest(
	event *models.Event,
	songReq *models.SongRequest,
	allReqs []*models.SongRequest,
	now time.Time,
) PlacementResult {
	normSinger := normalizeSingerName(songReq.SingerName)
	activeQueue, completedReqs, completedCounts := partitionPlacementRequests(songReq.ID, allReqs)
	sortActiveByRank(activeQueue)

	// SingerTurn: completed performances + active songs for this singer + 1
	activeAhead := countSingerActive(activeQueue, normSinger)
	songReq.SingerTurn = completedCounts[normSinger] + activeAhead + 1

	nowMilli := now.UTC().UnixMilli()

	switch event.RotationMode {
	case models.RotationModeFairPriority:
		return placeFairPriority(event, songReq, activeQueue, completedReqs, activeAhead, nowMilli)
	case models.RotationModeRoundRobin:
		placeRoundRobin(event, songReq, activeQueue, completedCounts[normSinger])
	default:
		// FCFS or manual chronological order
		songReq.Round = 0
		songReq.Rank = tailRank(activeQueue, nowMilli)
	}

	return PlacementResult{
		NeedsReorder: false,
		ReorderIDs:   nil,
	}
}

func partitionPlacementRequests(
	songReqID string,
	allReqs []*models.SongRequest,
) ([]*models.SongRequest, []*models.SongRequest, map[string]int) {
	var activeQueue []*models.SongRequest
	var completedReqs []*models.SongRequest
	completedCounts := make(map[string]int)

	for _, er := range allReqs {
		if er.ID == songReqID {
			continue // skip self when updating an existing request
		}
		switch er.Status {
		case models.StatusCompleted:
			completedCounts[normalizeSingerName(er.SingerName)]++
			completedReqs = append(completedReqs, er)
		case models.StatusAccepted, models.StatusUpNext:
			activeQueue = append(activeQueue, er)
		}
	}
	return activeQueue, completedReqs, completedCounts
}

func countSingerActive(activeQueue []*models.SongRequest, normSinger string) int {
	count := 0
	for _, er := range activeQueue {
		if normalizeSingerName(er.SingerName) == normSinger {
			count++
		}
	}
	return count
}

func placeFairPriority(
	event *models.Event,
	songReq *models.SongRequest,
	activeQueue, completedReqs []*models.SongRequest,
	activeAhead int,
	nowMilli int64,
) PlacementResult {
	songReq.Round = 0
	if len(activeQueue) == 0 {
		songReq.Rank = nowMilli
		return PlacementResult{}
	}

	insertIdx := findFairPriorityInsertIdx(event, songReq, activeQueue, completedReqs, activeAhead)
	if insertIdx >= len(activeQueue) {
		songReq.Rank = tailRank(activeQueue, nowMilli)
		return PlacementResult{}
	}

	return insertAtRankGap(songReq, activeQueue, insertIdx)
}

func buildSongCycles(activeQueue []*models.SongRequest) map[string]int {
	singerActiveCounts := make(map[string]int)
	songCycles := make(map[string]int, len(activeQueue))
	for _, er := range activeQueue {
		sName := normalizeSingerName(er.SingerName)
		songCycles[er.ID] = singerActiveCounts[sName]
		singerActiveCounts[sName]++
	}
	return songCycles
}

func findFairPriorityInsertIdx(
	event *models.Event,
	songReq *models.SongRequest,
	activeQueue, completedReqs []*models.SongRequest,
	newCycle int,
) int {
	normSinger := normalizeSingerName(songReq.SingerName)
	fp := newFairPriorityContext(event, completedReqs)
	newEffectiveSeq := fp.effectiveSeq(normSinger)
	songCycles := buildSongCycles(activeQueue)

	// Stage Protection Buffer:
	// Top 3 songs (On Stage, On Deck, In the Hole) are locked and protected from newcomers.
	minInsertIdx := stageBufferSize(len(activeQueue))
	for i := minInsertIdx; i < len(activeQueue); i++ {
		currReq := activeQueue[i]
		currCycle := songCycles[currReq.ID]
		currEffectiveSeq := fp.effectiveSeq(normalizeSingerName(currReq.SingerName))
		if shouldInsertBeforeFairPriority(songReq, currReq, newCycle, currCycle, newEffectiveSeq, currEffectiveSeq) {
			return i
		}
	}
	return len(activeQueue)
}

func shouldInsertBeforeFairPriority(
	songReq, currReq *models.SongRequest,
	newCycle, currCycle, newEffectiveSeq, currEffectiveSeq int,
) bool {
	if newCycle != currCycle {
		return newCycle < currCycle
	}
	// Equal cycle: compare effectiveSeq (least recently sung + host weight), then CreatedAt
	if newEffectiveSeq != currEffectiveSeq {
		return newEffectiveSeq < currEffectiveSeq
	}
	// FIFO tie-breaker: earlier submission remains ahead
	return !songReq.CreatedAt.IsZero() && !currReq.CreatedAt.IsZero() && songReq.CreatedAt.Before(currReq.CreatedAt)
}

func insertAtRankGap(songReq *models.SongRequest, activeQueue []*models.SongRequest, insertIdx int) PlacementResult {
	var prevRank int64
	if insertIdx > 0 {
		prevRank = activeQueue[insertIdx-1].Rank
	}
	nextRank := activeQueue[insertIdx].Rank
	if nextRank-prevRank > 1 {
		songReq.Rank = prevRank + (nextRank-prevRank)/2
		return PlacementResult{}
	}

	songReq.Rank = prevRank + 1
	reorderedIDs := make([]string, 0, len(activeQueue)+1)
	for i := 0; i < insertIdx; i++ {
		reorderedIDs = append(reorderedIDs, activeQueue[i].ID)
	}
	reorderedIDs = append(reorderedIDs, songReq.ID)
	for i := insertIdx; i < len(activeQueue); i++ {
		reorderedIDs = append(reorderedIDs, activeQueue[i].ID)
	}
	return PlacementResult{
		NeedsReorder: true,
		ReorderIDs:   reorderedIDs,
	}
}

func placeRoundRobin(
	event *models.Event,
	songReq *models.SongRequest,
	activeQueue []*models.SongRequest,
	completedCount int,
) {
	normSinger := normalizeSingerName(songReq.SingerName)
	activeRounds := make(map[int]bool)
	for _, er := range activeQueue {
		if normalizeSingerName(er.SingerName) == normSinger {
			erRound := models.ResolveRound(er.Round, er.Rank)
			activeRounds[erRound] = true
		}
	}

	effectiveRound := effectiveCurrentRound(event)
	if completedCount+1 > effectiveRound {
		effectiveRound = completedCount + 1
	}
	for activeRounds[effectiveRound] {
		effectiveRound++
	}

	songReq.Round = effectiveRound
	intraRank := maxIntraRoundRank(activeQueue, effectiveRound) + 1
	if intraRank <= 0 {
		intraRank = 1
	}
	songReq.Rank = (int64(songReq.Round) * models.RoundRankMultiplier) + intraRank
}

func maxIntraRoundRank(activeQueue []*models.SongRequest, round int) int64 {
	var maxIntra int64
	for _, er := range activeQueue {
		if models.ResolveRound(er.Round, er.Rank) != round {
			continue
		}
		if intra := er.Rank % models.RoundRankMultiplier; intra > maxIntra {
			maxIntra = intra
		}
	}
	return maxIntra
}

// PlanRotationTransition computes the re-evaluated ranks, rounds, and order of active and pending
// queue items for a rotation mode transition in memory without mutating storage.
// Strictly protects the top 3 stage positions ("On Stage", "On Deck", "In the Hole"),
// and normalizes staged (pending) requests.
func PlanRotationTransition(event *models.Event, allReqs []*models.SongRequest, oldMode, newMode string) ([]*models.SongRequest, error) {
	if len(allReqs) == 0 {
		return nil, nil
	}

	activeReqs, pendingReqs, completedReqs := partitionTransitionRequests(allReqs)
	sortActiveByRank(activeReqs)

	currentRound := effectiveCurrentRound(event)
	requestsToUpdate := normalizePendingRequests(pendingReqs, newMode, currentRound)
	if len(activeReqs) == 0 {
		return requestsToUpdate, nil
	}

	// Stage protection: Top 3 songs (On Stage, On Deck, In the Hole) strictly survive any transition
	bufferSize := stageBufferSize(len(activeReqs))
	protected := activeReqs[:bufferSize]
	tail := activeReqs[bufferSize:]

	var finalOrder []*models.SongRequest
	switch newMode {
	case models.RotationModeFairPriority:
		finalOrder = transitionFairPriority(event, protected, tail, completedReqs)
	case models.RotationModeFCFS, models.RotationModeManual:
		finalOrder = transitionChronological(protected, tail)
	case models.RotationModeRoundRobin:
		finalOrder = transitionRoundRobin(protected, tail, currentRound)
	}

	for i, req := range finalOrder {
		rank := int64(i + 1)
		if req.Round > 0 {
			rank = (int64(req.Round) * models.RoundRankMultiplier) + int64(i+1)
		}
		req.Rank = rank
		requestsToUpdate = append(requestsToUpdate, req)
	}

	return requestsToUpdate, nil
}

func partitionTransitionRequests(allReqs []*models.SongRequest) ([]*models.SongRequest, []*models.SongRequest, []*models.SongRequest) {
	var activeReqs []*models.SongRequest
	var pendingReqs []*models.SongRequest
	var completedReqs []*models.SongRequest

	for _, req := range allReqs {
		switch req.Status {
		case models.StatusCompleted:
			completedReqs = append(completedReqs, req)
		case models.StatusAccepted, models.StatusUpNext:
			cp := *req
			activeReqs = append(activeReqs, &cp)
		case models.StatusPending:
			cp := *req
			pendingReqs = append(pendingReqs, &cp)
		}
	}
	return activeReqs, pendingReqs, completedReqs
}

func normalizePendingRequests(pendingReqs []*models.SongRequest, newMode string, currentRound int) []*models.SongRequest {
	targetRound := 0
	if newMode == models.RotationModeRoundRobin {
		targetRound = currentRound
	}

	var updated []*models.SongRequest
	for _, req := range pendingReqs {
		if req.Round != targetRound {
			req.Round = targetRound
			updated = append(updated, req)
		}
	}
	return updated
}

type fairPriorityTailItem struct {
	req          *models.SongRequest
	cycle        int
	effectiveSeq int
}

func transitionFairPriority(
	event *models.Event,
	protected, tail, completedReqs []*models.SongRequest,
) []*models.SongRequest {
	singerActive := make(map[string]int, len(protected))
	for _, req := range protected {
		req.Round = 0
		singerActive[normalizeSingerName(req.SingerName)]++
	}

	fp := newFairPriorityContext(event, completedReqs)
	items := make([]fairPriorityTailItem, len(tail))
	for i, req := range tail {
		norm := normalizeSingerName(req.SingerName)
		cycle := singerActive[norm]
		singerActive[norm]++
		items[i] = fairPriorityTailItem{
			req:          req,
			cycle:        cycle,
			effectiveSeq: fp.effectiveSeq(norm),
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].cycle != items[j].cycle {
			return items[i].cycle < items[j].cycle
		}
		if items[i].effectiveSeq != items[j].effectiveSeq {
			return items[i].effectiveSeq < items[j].effectiveSeq
		}
		return items[i].req.CreatedAt.Before(items[j].req.CreatedAt)
	})

	finalOrder := make([]*models.SongRequest, 0, len(protected)+len(tail))
	finalOrder = append(finalOrder, protected...)
	for _, it := range items {
		it.req.Round = 0
		finalOrder = append(finalOrder, it.req)
	}
	return finalOrder
}

func transitionChronological(protected, tail []*models.SongRequest) []*models.SongRequest {
	for _, req := range protected {
		req.Round = 0
	}

	sort.SliceStable(tail, func(i, j int) bool {
		return tail[i].CreatedAt.Before(tail[j].CreatedAt)
	})

	finalOrder := make([]*models.SongRequest, 0, len(protected)+len(tail))
	finalOrder = append(finalOrder, protected...)
	for _, req := range tail {
		req.Round = 0
		finalOrder = append(finalOrder, req)
	}
	return finalOrder
}

func transitionRoundRobin(protected, tail []*models.SongRequest, currentRound int) []*models.SongRequest {
	assignedRounds := make(map[string]map[int]bool)
	for _, req := range protected {
		norm := normalizeSingerName(req.SingerName)
		if assignedRounds[norm] == nil {
			assignedRounds[norm] = make(map[int]bool)
		}
		req.Round = currentRound
		assignedRounds[norm][currentRound] = true
	}

	sort.SliceStable(tail, func(i, j int) bool {
		return tail[i].CreatedAt.Before(tail[j].CreatedAt)
	})

	for _, req := range tail {
		assignSingerRound(req, currentRound, assignedRounds)
	}

	// Sort ONLY the tail by (Round, CreatedAt).
	// The top 3 stage positions ('protected') remain strictly at indices 0, 1, 2!
	sort.SliceStable(tail, func(i, j int) bool {
		if tail[i].Round != tail[j].Round {
			return tail[i].Round < tail[j].Round
		}
		return tail[i].CreatedAt.Before(tail[j].CreatedAt)
	})

	finalOrder := make([]*models.SongRequest, 0, len(protected)+len(tail))
	finalOrder = append(finalOrder, protected...)
	finalOrder = append(finalOrder, tail...)
	return finalOrder
}

func assignSingerRound(req *models.SongRequest, minRound int, assignedRounds map[string]map[int]bool) {
	norm := normalizeSingerName(req.SingerName)
	if assignedRounds[norm] == nil {
		assignedRounds[norm] = make(map[int]bool)
	}
	r := minRound
	for assignedRounds[norm][r] {
		r++
	}
	req.Round = r
	assignedRounds[norm][r] = true
}
