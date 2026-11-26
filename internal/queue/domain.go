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

// PlaceAcceptedRequest assigns Rank, Round, and SingerTurn to a request entering the active queue,
// inserting it according to the event's RotationMode while strictly respecting the 3-position stage buffer.
// It returns whether the queue needs displacement reordering and the ordered request IDs if so.
func PlaceAcceptedRequest(
	event *models.Event,
	songReq *models.SongRequest,
	allReqs []*models.SongRequest,
	now time.Time,
) PlacementResult {
	normSinger := strings.ToLower(strings.TrimSpace(songReq.SingerName))

	var activeQueue []*models.SongRequest
	var completedReqs []*models.SongRequest
	completedCounts := make(map[string]int)

	for _, er := range allReqs {
		if er.ID == songReq.ID {
			continue // skip self when updating an existing request
		}
		sName := strings.ToLower(strings.TrimSpace(er.SingerName))
		if er.Status == models.StatusCompleted {
			completedCounts[sName]++
			completedReqs = append(completedReqs, er)
		} else if er.Status == models.StatusAccepted || er.Status == models.StatusUpNext {
			activeQueue = append(activeQueue, er)
		}
	}

	// Sort activeQueue by Rank ascending, then CreatedAt ascending
	sort.SliceStable(activeQueue, func(i, j int) bool {
		if activeQueue[i].Rank != activeQueue[j].Rank {
			return activeQueue[i].Rank < activeQueue[j].Rank
		}
		return activeQueue[i].CreatedAt.Before(activeQueue[j].CreatedAt)
	})

	// SingerTurn: completed performances + active songs for this singer + 1
	completedCount := completedCounts[normSinger]
	activeAhead := 0
	for _, er := range activeQueue {
		if strings.ToLower(strings.TrimSpace(er.SingerName)) == normSinger {
			activeAhead++
		}
	}
	songReq.SingerTurn = completedCount + activeAhead + 1

	nowMilli := now.UTC().UnixMilli()

	if event.RotationMode == models.RotationModeFairPriority {
		songReq.Round = 0
		newCycle := activeAhead
		fp := newFairPriorityContext(event, completedReqs)
		newEffectiveSeq := fp.effectiveSeq(normSinger)

		// Stage Protection Buffer:
		// Top 3 songs (On Stage, On Deck, In the Hole) are locked and protected from newcomers.
		minInsertIdx := 3
		if len(activeQueue) < minInsertIdx {
			minInsertIdx = len(activeQueue)
		}

		singerActiveCounts := make(map[string]int)
		songCycles := make(map[string]int, len(activeQueue))
		for _, er := range activeQueue {
			sName := strings.ToLower(strings.TrimSpace(er.SingerName))
			songCycles[er.ID] = singerActiveCounts[sName]
			singerActiveCounts[sName]++
		}

		insertIdx := len(activeQueue)
		for i := minInsertIdx; i < len(activeQueue); i++ {
			currReq := activeQueue[i]
			currSinger := strings.ToLower(strings.TrimSpace(currReq.SingerName))
			currCycle := songCycles[currReq.ID]
			currEffectiveSeq := fp.effectiveSeq(currSinger)

			if newCycle < currCycle {
				insertIdx = i
				break
			} else if newCycle > currCycle {
				continue
			} else {
				// Equal cycle: compare effectiveSeq (least recently sung + host weight), then CreatedAt
				if newEffectiveSeq < currEffectiveSeq {
					insertIdx = i
					break
				} else if newEffectiveSeq > currEffectiveSeq {
					continue
				} else {
					if !songReq.CreatedAt.IsZero() && !currReq.CreatedAt.IsZero() && songReq.CreatedAt.Before(currReq.CreatedAt) {
						insertIdx = i
						break
					}
					// FIFO tie-breaker: earlier submission remains ahead
					continue
				}
			}
		}

		if len(activeQueue) == 0 {
			songReq.Rank = nowMilli
		} else if insertIdx >= len(activeQueue) {
			lastRank := activeQueue[len(activeQueue)-1].Rank
			if nowMilli > lastRank {
				songReq.Rank = nowMilli
			} else {
				songReq.Rank = lastRank + 10_000_000
			}
		} else {
			var prevRank int64
			if insertIdx == 0 {
				prevRank = 0
			} else {
				prevRank = activeQueue[insertIdx-1].Rank
			}
			nextRank := activeQueue[insertIdx].Rank
			if nextRank-prevRank > 1 {
				songReq.Rank = prevRank + (nextRank-prevRank)/2
			} else {
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
		}
	} else if event.RotationMode == models.RotationModeRoundRobin {
		currentRound := event.CurrentRound
		if currentRound <= 0 {
			currentRound = 1
		}

		activeRounds := make(map[int]bool)
		for _, er := range activeQueue {
			if strings.ToLower(strings.TrimSpace(er.SingerName)) == normSinger {
				erRound := models.ResolveRound(er.Round, er.Rank)
				activeRounds[erRound] = true
			}
		}

		effectiveRound := currentRound
		if completedCount+1 > effectiveRound {
			effectiveRound = completedCount + 1
		}
		for activeRounds[effectiveRound] {
			effectiveRound++
		}

		songReq.Round = effectiveRound

		// Find max intra-round rank for effectiveRound in activeQueue
		var maxIntra int64 = 0
		for _, er := range activeQueue {
			if models.ResolveRound(er.Round, er.Rank) == effectiveRound {
				intra := er.Rank % models.RoundRankMultiplier
				if intra > maxIntra {
					maxIntra = intra
				}
			}
		}
		intraRank := maxIntra + 1
		if intraRank <= 0 {
			intraRank = 1
		}
		songReq.Rank = (int64(songReq.Round) * models.RoundRankMultiplier) + intraRank
	} else {
		// FCFS or manual chronological order
		songReq.Round = 0
		if len(activeQueue) == 0 {
			songReq.Rank = nowMilli
		} else {
			lastRank := activeQueue[len(activeQueue)-1].Rank
			if nowMilli > lastRank {
				songReq.Rank = nowMilli
			} else {
				songReq.Rank = lastRank + 10_000_000
			}
		}
	}

	return PlacementResult{
		NeedsReorder: false,
		ReorderIDs:   nil,
	}
}

// PlanRotationTransition computes the re-evaluated ranks, rounds, and order of active and pending
// queue items for a rotation mode transition in memory without mutating storage.
// Strictly protects the top 3 stage positions ("On Stage", "On Deck", "In the Hole"),
// and normalizes staged (pending) requests.
func PlanRotationTransition(event *models.Event, allReqs []*models.SongRequest, oldMode, newMode string) ([]*models.SongRequest, error) {
	if len(allReqs) == 0 {
		return nil, nil
	}

	// Filter active requests, pending requests, and completed requests
	var activeReqs []*models.SongRequest
	var pendingReqs []*models.SongRequest
	var completedReqs []*models.SongRequest

	for _, req := range allReqs {
		if req.Status == models.StatusCompleted {
			completedReqs = append(completedReqs, req)
		} else if req.Status == models.StatusAccepted || req.Status == models.StatusUpNext {
			cp := *req
			activeReqs = append(activeReqs, &cp)
		} else if req.Status == models.StatusPending {
			cp := *req
			pendingReqs = append(pendingReqs, &cp)
		}
	}

	// Sort activeReqs by Rank ascending to establish current stage order
	sort.SliceStable(activeReqs, func(i, j int) bool {
		if activeReqs[i].Rank != activeReqs[j].Rank {
			return activeReqs[i].Rank < activeReqs[j].Rank
		}
		return activeReqs[i].CreatedAt.Before(activeReqs[j].CreatedAt)
	})

	currentRound := event.CurrentRound
	if currentRound <= 0 {
		currentRound = 1
	}

	var requestsToUpdate []*models.SongRequest

	// 1. Normalize staged (pending) requests
	for _, req := range pendingReqs {
		needsUpdate := false
		if newMode == models.RotationModeRoundRobin {
			if req.Round != currentRound {
				req.Round = currentRound
				needsUpdate = true
			}
		} else {
			if req.Round != 0 {
				req.Round = 0
				needsUpdate = true
			}
		}
		if needsUpdate {
			requestsToUpdate = append(requestsToUpdate, req)
		}
	}

	if len(activeReqs) == 0 {
		return requestsToUpdate, nil
	}

	// Stage protection: Top 3 songs (On Stage, On Deck, In the Hole) strictly survive any transition
	bufferSize := 3
	if len(activeReqs) < bufferSize {
		bufferSize = len(activeReqs)
	}

	protected := activeReqs[:bufferSize]
	tail := activeReqs[bufferSize:]

	var finalOrder []*models.SongRequest

	if newMode == models.RotationModeFairPriority {
		for _, req := range protected {
			req.Round = 0
		}

		activeInProtected := make(map[string]int)
		for _, req := range protected {
			norm := strings.ToLower(strings.TrimSpace(req.SingerName))
			activeInProtected[norm]++
		}

		tailSingerActive := make(map[string]int)
		for k, v := range activeInProtected {
			tailSingerActive[k] = v
		}

		fp := newFairPriorityContext(event, completedReqs)

		type tailItem struct {
			req          *models.SongRequest
			cycle        int
			effectiveSeq int
		}
		items := make([]tailItem, len(tail))
		for i, req := range tail {
			norm := strings.ToLower(strings.TrimSpace(req.SingerName))
			cycle := tailSingerActive[norm]
			tailSingerActive[norm]++
			items[i] = tailItem{
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

		finalOrder = make([]*models.SongRequest, 0, len(activeReqs))
		finalOrder = append(finalOrder, protected...)
		for _, it := range items {
			it.req.Round = 0
			finalOrder = append(finalOrder, it.req)
		}

	} else if newMode == models.RotationModeFCFS || newMode == models.RotationModeManual {
		for _, req := range protected {
			req.Round = 0
		}

		sort.SliceStable(tail, func(i, j int) bool {
			return tail[i].CreatedAt.Before(tail[j].CreatedAt)
		})

		finalOrder = make([]*models.SongRequest, 0, len(activeReqs))
		finalOrder = append(finalOrder, protected...)
		for _, req := range tail {
			req.Round = 0
			finalOrder = append(finalOrder, req)
		}

	} else if newMode == models.RotationModeRoundRobin {
		assignedRounds := make(map[string]map[int]bool)
		for _, req := range protected {
			norm := strings.ToLower(strings.TrimSpace(req.SingerName))
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
			norm := strings.ToLower(strings.TrimSpace(req.SingerName))
			if assignedRounds[norm] == nil {
				assignedRounds[norm] = make(map[int]bool)
			}
			r := currentRound
			for assignedRounds[norm][r] {
				r++
			}
			req.Round = r
			assignedRounds[norm][r] = true
		}

		// Sort ONLY the tail by (Round, CreatedAt).
		// The top 3 stage positions ('protected') remain strictly at indices 0, 1, 2!
		sort.SliceStable(tail, func(i, j int) bool {
			if tail[i].Round != tail[j].Round {
				return tail[i].Round < tail[j].Round
			}
			return tail[i].CreatedAt.Before(tail[j].CreatedAt)
		})

		finalOrder = make([]*models.SongRequest, 0, len(activeReqs))
		finalOrder = append(finalOrder, protected...)
		finalOrder = append(finalOrder, tail...)
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
