package queue_test

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/queue"
)

func TestPlaceAcceptedRequest_FCFS(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	event := &models.Event{
		ID:           "ev-1",
		RotationMode: models.RotationModeFCFS,
	}

	req1 := &models.SongRequest{
		ID:         "r1",
		SingerName: "Alice",
		Status:     models.StatusAccepted,
	}
	res1 := queue.PlaceAcceptedRequest(event, req1, nil, now)
	if res1.NeedsReorder {
		t.Errorf("expected no reorder for first request")
	}
	if req1.Rank != now.UnixMilli() {
		t.Errorf("expected rank %d, got %d", now.UnixMilli(), req1.Rank)
	}
	if req1.Round != 0 {
		t.Errorf("expected round 0, got %d", req1.Round)
	}
	if req1.SingerTurn != 1 {
		t.Errorf("expected SingerTurn 1, got %d", req1.SingerTurn)
	}

	req2 := &models.SongRequest{
		ID:         "r2",
		SingerName: "Bob",
		Status:     models.StatusAccepted,
	}
	res2 := queue.PlaceAcceptedRequest(event, req2, []*models.SongRequest{req1}, now.Add(time.Second))
	if res2.NeedsReorder {
		t.Errorf("expected no reorder")
	}
	if req2.Rank <= req1.Rank {
		t.Errorf("expected req2.Rank > req1.Rank, got req2=%d, req1=%d", req2.Rank, req1.Rank)
	}
}

func TestPlaceAcceptedRequest_RoundRobin(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	event := &models.Event{
		ID:           "ev-1",
		RotationMode: models.RotationModeRoundRobin,
		CurrentRound: 1,
	}

	req1 := &models.SongRequest{
		ID:         "r1",
		SingerName: "Alice",
		Status:     models.StatusAccepted,
	}
	queue.PlaceAcceptedRequest(event, req1, nil, now)
	if req1.Round != 1 {
		t.Errorf("expected round 1, got %d", req1.Round)
	}

	// Alice submits a second song: should be round 2
	req2 := &models.SongRequest{
		ID:         "r2",
		SingerName: "Alice",
		Status:     models.StatusAccepted,
	}
	queue.PlaceAcceptedRequest(event, req2, []*models.SongRequest{req1}, now.Add(time.Minute))
	if req2.Round != 2 {
		t.Errorf("expected round 2 for Alice's 2nd song, got %d", req2.Round)
	}

	// Bob submits his first song: should be round 1
	req3 := &models.SongRequest{
		ID:         "r3",
		SingerName: "Bob",
		Status:     models.StatusAccepted,
	}
	queue.PlaceAcceptedRequest(event, req3, []*models.SongRequest{req1, req2}, now.Add(2*time.Minute))
	if req3.Round != 1 {
		t.Errorf("expected round 1 for Bob's 1st song, got %d", req3.Round)
	}
	if req3.Rank <= req1.Rank {
		t.Errorf("expected Bob's round 1 song to have rank > Alice's round 1 song")
	}
	if req3.Rank >= req2.Rank {
		t.Errorf("expected Bob's round 1 song to have rank < Alice's round 2 song")
	}
}

func TestPlaceAcceptedRequest_FairPriority_StageLock(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	event := &models.Event{
		ID:           "ev-1",
		RotationMode: models.RotationModeFairPriority,
	}

	// 3 existing songs on stage (top 3 stage buffer)
	r1 := &models.SongRequest{ID: "r1", SingerName: "Alice", Status: models.StatusAccepted, Rank: 1000, CreatedAt: now}
	r2 := &models.SongRequest{ID: "r2", SingerName: "Bob", Status: models.StatusAccepted, Rank: 2000, CreatedAt: now.Add(time.Second)}
	r3 := &models.SongRequest{ID: "r3", SingerName: "Charlie", Status: models.StatusAccepted, Rank: 3000, CreatedAt: now.Add(2 * time.Second)}
	r4 := &models.SongRequest{ID: "r4", SingerName: "Alice", Status: models.StatusAccepted, Rank: 4000, CreatedAt: now.Add(3 * time.Second)} // Alice cycle 1

	existing := []*models.SongRequest{r1, r2, r3, r4}

	// Newcomer Dave (cycle 0, completed 0) enters.
	// Must NOT displace r1, r2, r3 (stage lock), but SHOULD weave ahead of r4 (Alice cycle 1).
	daveReq := &models.SongRequest{
		ID:         "r-dave",
		SingerName: "Dave",
		Status:     models.StatusAccepted,
	}

	queue.PlaceAcceptedRequest(event, daveReq, existing, now.Add(10*time.Second))

	if daveReq.Rank <= r3.Rank {
		t.Errorf("expected Dave to be behind stage buffer (r3=%d), got %d", r3.Rank, daveReq.Rank)
	}
	if daveReq.Rank >= r4.Rank {
		t.Errorf("expected Dave to weave ahead of Alice's 2nd song (r4=%d), got %d", r4.Rank, daveReq.Rank)
	}
}

func TestPlanRotationTransition_StageLockPreservation(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	event := &models.Event{
		ID:           "ev-1",
		RotationMode: models.RotationModeRoundRobin,
		CurrentRound: 1,
	}

	r1 := &models.SongRequest{ID: "r1", SingerName: "SingerA", Status: models.StatusAccepted, Rank: 1000, Round: 1, CreatedAt: now}
	r2 := &models.SongRequest{ID: "r2", SingerName: "SingerB", Status: models.StatusAccepted, Rank: 2000, Round: 1, CreatedAt: now.Add(time.Second)}
	r3 := &models.SongRequest{ID: "r3", SingerName: "SingerC", Status: models.StatusAccepted, Rank: 3000, Round: 1, CreatedAt: now.Add(2 * time.Second)}
	r4 := &models.SongRequest{ID: "r4", SingerName: "SingerA", Status: models.StatusAccepted, Rank: 10000000000001, Round: 2, CreatedAt: now.Add(3 * time.Second)}
	r5 := &models.SongRequest{ID: "r5", SingerName: "SingerD", Status: models.StatusAccepted, Rank: 4000, Round: 1, CreatedAt: now.Add(4 * time.Second)}

	allReqs := []*models.SongRequest{r1, r2, r3, r4, r5}

	// Transition to Fair Priority
	updated, err := queue.PlanRotationTransition(event, allReqs, models.RotationModeRoundRobin, models.RotationModeFairPriority)
	if err != nil {
		t.Fatalf("PlanRotationTransition failed: %v", err)
	}

	if len(updated) != 5 {
		t.Fatalf("expected 5 updated requests, got %d", len(updated))
	}

	// First 3 MUST be r1, r2, r3 in exact order
	if updated[0].ID != "r1" || updated[1].ID != "r2" || updated[2].ID != "r3" {
		t.Errorf("stage buffer not preserved: got [%s, %s, %s]", updated[0].ID, updated[1].ID, updated[2].ID)
	}

	// SingerD (cycle 0) should be ahead of SingerA's 2nd song r4 (cycle 1)
	if updated[3].ID != "r5" {
		t.Errorf("expected r5 (SingerD) at index 3, got %s", updated[3].ID)
	}
	if updated[4].ID != "r4" {
		t.Errorf("expected r4 (SingerA) at index 4, got %s", updated[4].ID)
	}
}

func completedReq(id, singer string, completedAt time.Time) *models.SongRequest {
	c := completedAt
	return &models.SongRequest{
		ID:          id,
		SingerName:  singer,
		Status:      models.StatusCompleted,
		CreatedAt:   completedAt.Add(-5 * time.Minute),
		UpdatedAt:   completedAt,
		CompletedAt: &c,
	}
}

func activeReq(id, singer string, rank int64, createdAt time.Time) *models.SongRequest {
	return &models.SongRequest{
		ID:         id,
		SingerName: singer,
		Status:     models.StatusAccepted,
		Rank:       rank,
		CreatedAt:  createdAt,
	}
}

func sortedActiveIDs(reqs []*models.SongRequest) []string {
	var active []*models.SongRequest
	for _, r := range reqs {
		if r.Status == models.StatusAccepted || r.Status == models.StatusUpNext {
			active = append(active, r)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		if active[i].Rank != active[j].Rank {
			return active[i].Rank < active[j].Rank
		}
		return active[i].CreatedAt.Before(active[j].CreatedAt)
	})
	ids := make([]string, len(active))
	for i, r := range active {
		ids[i] = r.ID
	}
	return ids
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFairPriority_LeastRecentlySungAndWeights(t *testing.T) {
	base := time.Date(2026, 11, 24, 21, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		weights        map[string]int
		completed      []*models.SongRequest
		initialActive  []*models.SongRequest
		incoming       []*models.SongRequest // inserted sequentially via PlaceAcceptedRequest
		wantOrder      []string              // expected final active queue IDs
		wantSingerTurn map[string]int        // optional SingerTurn checks on incoming requests
	}{
		{
			name: "newcomer finishes song #1 and song #2 merges behind regulars who waited longer",
			completed: []*models.SongRequest{
				// Earlier in the night, regulars A and B each sang twice (high completedCount)
				completedReq("c1", "RegA", base.Add(1*time.Minute)),
				completedReq("c2", "RegB", base.Add(2*time.Minute)),
				completedReq("c3", "RegA", base.Add(3*time.Minute)), // RegA lastCompletedSeq = 3
				completedReq("c4", "RegB", base.Add(4*time.Minute)), // RegB lastCompletedSeq = 4
				// Newcomer just finished song #1 (completedCount = 1, lastCompletedSeq = 5)
				completedReq("c5", "Newcomer", base.Add(5*time.Minute)),
			},
			initialActive: []*models.SongRequest{
				// Top 3 stage lock
				activeReq("s1", "Stage1", 1000, base.Add(6*time.Minute)),
				activeReq("s2", "Stage2", 2000, base.Add(7*time.Minute)),
				activeReq("s3", "Stage3", 3000, base.Add(8*time.Minute)),
				// Regulars waiting in tail (cycle 0)
				activeReq("regA-3", "RegA", 4000, base.Add(9*time.Minute)),
				activeReq("regB-3", "RegB", 5000, base.Add(10*time.Minute)),
			},
			incoming: []*models.SongRequest{
				// Newcomer submits song #2 (cycle 0, effectiveSeq = 5 > RegA=3, RegB=4)
				activeReq("new-2", "Newcomer", 0, base.Add(11*time.Minute)),
			},
			wantOrder: []string{"s1", "s2", "s3", "regA-3", "regB-3", "new-2"},
			wantSingerTurn: map[string]int{
				"new-2": 2, // 1 completed + 0 activeAhead + 1
			},
		},
		{
			name: "11 PM busload of newcomers does not starve regulars waiting longer than newcomerCredit",
			completed: func() []*models.SongRequest {
				// 10 completed songs total; newcomerCredit = 5 => newcomer baseSeq = max(0, 10-5) = 5
				reqs := make([]*models.SongRequest, 0, 10)
				for i := 1; i <= 10; i++ {
					singer := fmt.Sprintf("Other%d", i)
					switch i {
					case 3:
						singer = "WaitingReg" // lastCompletedSeq = 3 (< 5, waited 7 songs > credit)
					case 8:
						singer = "RecentReg" // lastCompletedSeq = 8 (> 5, waited 2 songs < credit)
					}
					reqs = append(reqs, completedReq(fmt.Sprintf("c%d", i), singer, base.Add(time.Duration(i)*time.Minute)))
				}
				return reqs
			}(),
			initialActive: []*models.SongRequest{
				activeReq("s1", "Stage1", 1000, base.Add(11*time.Minute)),
				activeReq("s2", "Stage2", 2000, base.Add(12*time.Minute)),
				activeReq("s3", "Stage3", 3000, base.Add(13*time.Minute)),
				activeReq("wait-reg", "WaitingReg", 4000, base.Add(14*time.Minute)),
				activeReq("recent-reg", "RecentReg", 5000, base.Add(15*time.Minute)),
			},
			incoming: []*models.SongRequest{
				activeReq("bus-1", "BusNew1", 0, base.Add(16*time.Minute)),
				activeReq("bus-2", "BusNew2", 0, base.Add(17*time.Minute)),
				activeReq("bus-3", "BusNew3", 0, base.Add(18*time.Minute)),
			},
			// WaitingReg (seq 3) stays ahead of all 3 newcomers (seq 5),
			// while newcomers weave ahead of RecentReg (seq 8) in FIFO order.
			wantOrder: []string{"s1", "s2", "s3", "wait-reg", "bus-1", "bus-2", "bus-3", "recent-reg"},
			wantSingerTurn: map[string]int{
				"bus-1": 1,
				"bus-2": 1,
				"bus-3": 1,
			},
		},
		{
			name: "regular takes a break and returns ahead of more recent singers and latecomers",
			completed: func() []*models.SongRequest {
				// 12 completed songs; newcomer baseSeq = 12 - 5 = 7
				reqs := make([]*models.SongRequest, 0, 12)
				for i := 1; i <= 12; i++ {
					singer := fmt.Sprintf("Other%d", i)
					switch i {
					case 2:
						singer = "ReturningReg" // lastCompletedSeq = 2
					case 10:
						singer = "RecentA" // lastCompletedSeq = 10
					case 12:
						singer = "RecentB" // lastCompletedSeq = 12
					}
					reqs = append(reqs, completedReq(fmt.Sprintf("c%d", i), singer, base.Add(time.Duration(i)*time.Minute)))
				}
				return reqs
			}(),
			initialActive: []*models.SongRequest{
				activeReq("s1", "Stage1", 1000, base.Add(13*time.Minute)),
				activeReq("s2", "Stage2", 2000, base.Add(14*time.Minute)),
				activeReq("s3", "Stage3", 3000, base.Add(15*time.Minute)),
				activeReq("new-1", "Newcomer", 4000, base.Add(16*time.Minute)), // baseSeq = 7
				activeReq("rec-a", "RecentA", 5000, base.Add(17*time.Minute)),  // baseSeq = 10
				activeReq("rec-b", "RecentB", 6000, base.Add(18*time.Minute)),  // baseSeq = 12
			},
			incoming: []*models.SongRequest{
				activeReq("ret-reg", "ReturningReg", 0, base.Add(19*time.Minute)), // baseSeq = 2
			},
			wantOrder: []string{"s1", "s2", "s3", "ret-reg", "new-1", "rec-a", "rec-b"},
			wantSingerTurn: map[string]int{
				"ret-reg": 2,
			},
		},
		{
			name: "VIP weight +3 advances singer within cycle without breaking stage lock or causing back-to-back turns",
			weights: map[string]int{
				"  BirthdayVIP  ": 3, // verify normalization & case-insensitivity
			},
			completed: []*models.SongRequest{
				completedReq("c1", "PeerA", base.Add(1*time.Minute)),       // seq 1
				completedReq("c2", "PeerB", base.Add(2*time.Minute)),       // seq 2
				completedReq("c3", "PeerC", base.Add(3*time.Minute)),       // seq 3
				completedReq("c4", "PeerD", base.Add(4*time.Minute)),       // seq 4
				completedReq("c5", "BirthdayVIP", base.Add(5*time.Minute)), // seq 5 -> effectiveSeq = 5 - 3 = 2
			},
			initialActive: []*models.SongRequest{
				activeReq("s1", "Stage1", 1000, base.Add(6*time.Minute)),
				activeReq("s2", "Stage2", 2000, base.Add(7*time.Minute)),
				activeReq("s3", "Stage3", 3000, base.Add(8*time.Minute)),
				activeReq("pA", "PeerA", 4000, base.Add(9*time.Minute)),  // effectiveSeq 1
				activeReq("pC", "PeerC", 5000, base.Add(10*time.Minute)), // effectiveSeq 3
				activeReq("pD", "PeerD", 6000, base.Add(11*time.Minute)), // effectiveSeq 4
			},
			incoming: []*models.SongRequest{
				// VIP 1st active song (cycle 0, effectiveSeq 2): jumps ahead of pC (3) and pD (4), behind s1..s3 and pA (1)
				activeReq("vip-1", "BirthdayVIP", 0, base.Add(12*time.Minute)),
				// VIP 2nd active song (cycle 1, effectiveSeq 2): must NOT cause back-to-back; stays behind all cycle 0 peers!
				activeReq("vip-2", "BirthdayVIP", 0, base.Add(13*time.Minute)),
			},
			wantOrder: []string{"s1", "s2", "s3", "pA", "vip-1", "pC", "pD", "vip-2"},
			wantSingerTurn: map[string]int{
				"vip-1": 2,
				"vip-2": 3,
			},
		},
		{
			name: "negative weight -4 quietly pushes singer behind equal-cycle peers",
			weights: map[string]int{
				"throttled": -4,
			},
			completed: []*models.SongRequest{
				completedReq("c1", "Throttled", base.Add(1*time.Minute)), // seq 1 -> effectiveSeq = 1 - (-4) = 5
				completedReq("c2", "PeerA", base.Add(2*time.Minute)),     // seq 2
				completedReq("c3", "PeerB", base.Add(3*time.Minute)),     // seq 3
				completedReq("c4", "PeerC", base.Add(4*time.Minute)),     // seq 4
			},
			initialActive: []*models.SongRequest{
				activeReq("s1", "Stage1", 1000, base.Add(5*time.Minute)),
				activeReq("s2", "Stage2", 2000, base.Add(6*time.Minute)),
				activeReq("s3", "Stage3", 3000, base.Add(7*time.Minute)),
				activeReq("pA", "PeerA", 4000, base.Add(8*time.Minute)),
				activeReq("pB", "PeerB", 5000, base.Add(9*time.Minute)),
				activeReq("pC", "PeerC", 6000, base.Add(10*time.Minute)),
				// Cycle 1 song for PeerA should still remain behind Throttled's cycle 0 song
				activeReq("pA-2", "PeerA", 7000, base.Add(11*time.Minute)),
			},
			incoming: []*models.SongRequest{
				activeReq("throt-1", "Throttled", 0, base.Add(12*time.Minute)),
			},
			wantOrder: []string{"s1", "s2", "s3", "pA", "pB", "pC", "throt-1", "pA-2"},
			wantSingerTurn: map[string]int{
				"throt-1": 2,
			},
		},
		{
			name: "weights are clamped to [-10, +10]",
			weights: map[string]int{
				"supervip": 99,  // clamped to +10
				"superlow": -99, // clamped to -10
			},
			completed: func() []*models.SongRequest {
				reqs := make([]*models.SongRequest, 0, 15)
				for i := 1; i <= 15; i++ {
					singer := fmt.Sprintf("Other%d", i)
					switch i {
					case 1:
						singer = "SuperLow" // seq 1 - (-10) = 11
					case 4:
						singer = "PeerA" // seq 4
					case 12:
						singer = "PeerB" // seq 12
					case 15:
						singer = "SuperVIP" // seq 15 - 10 = 5 (would be -84 if unclamped, ahead of PeerA!)
					}
					reqs = append(reqs, completedReq(fmt.Sprintf("c%d", i), singer, base.Add(time.Duration(i)*time.Minute)))
				}
				return reqs
			}(),
			initialActive: []*models.SongRequest{
				activeReq("s1", "Stage1", 1000, base.Add(16*time.Minute)),
				activeReq("s2", "Stage2", 2000, base.Add(17*time.Minute)),
				activeReq("s3", "Stage3", 3000, base.Add(18*time.Minute)),
				activeReq("pA", "PeerA", 4000, base.Add(19*time.Minute)), // effectiveSeq 4
				activeReq("pB", "PeerB", 5000, base.Add(20*time.Minute)), // effectiveSeq 12
			},
			incoming: []*models.SongRequest{
				activeReq("vip", "SuperVIP", 0, base.Add(21*time.Minute)), // effectiveSeq 5
				activeReq("low", "SuperLow", 0, base.Add(22*time.Minute)), // effectiveSeq 11
			},
			// Clamped: PeerA (4) < SuperVIP (5) < SuperLow (11) < PeerB (12)
			wantOrder: []string{"s1", "s2", "s3", "pA", "vip", "low", "pB"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name+"/PlaceAcceptedRequest", func(t *testing.T) {
			event := &models.Event{
				ID:            "ev-fp",
				RotationMode:  models.RotationModeFairPriority,
				SingerWeights: tc.weights,
			}
			allReqs := make([]*models.SongRequest, 0, len(tc.completed)+len(tc.initialActive)+len(tc.incoming))
			for _, r := range tc.completed {
				cp := *r
				allReqs = append(allReqs, &cp)
			}
			for _, r := range tc.initialActive {
				cp := *r
				allReqs = append(allReqs, &cp)
			}
			for _, inc := range tc.incoming {
				cp := *inc
				res := queue.PlaceAcceptedRequest(event, &cp, allReqs, cp.CreatedAt)
				allReqs = append(allReqs, &cp)
				if res.NeedsReorder {
					// Apply reorder ranks to mirror store commit
					rankByID := make(map[string]int64, len(res.ReorderIDs))
					for i, id := range res.ReorderIDs {
						rankByID[id] = int64(i + 1)
					}
					for _, r := range allReqs {
						if rank, ok := rankByID[r.ID]; ok {
							r.Rank = rank
						}
					}
				}
				if wantTurn, ok := tc.wantSingerTurn[cp.ID]; ok && cp.SingerTurn != wantTurn {
					t.Errorf("request %s SingerTurn = %d, want %d", cp.ID, cp.SingerTurn, wantTurn)
				}
			}
			got := sortedActiveIDs(allReqs)
			if !equalStringSlices(got, tc.wantOrder) {
				t.Fatalf("PlaceAcceptedRequest order = %v, want %v", got, tc.wantOrder)
			}
		})

		t.Run(tc.name+"/PlanRotationTransition", func(t *testing.T) {
			event := &models.Event{
				ID:            "ev-fp",
				RotationMode:  models.RotationModeFCFS,
				SingerWeights: tc.weights,
			}
			// Put all initialActive + incoming into FCFS order (incoming appended at end)
			allReqs := make([]*models.SongRequest, 0, len(tc.completed)+len(tc.initialActive)+len(tc.incoming))
			for _, r := range tc.completed {
				cp := *r
				allReqs = append(allReqs, &cp)
			}
			var maxRank int64
			for _, r := range tc.initialActive {
				cp := *r
				if cp.Rank > maxRank {
					maxRank = cp.Rank
				}
				allReqs = append(allReqs, &cp)
			}
			for _, inc := range tc.incoming {
				cp := *inc
				maxRank += 1000
				cp.Rank = maxRank
				allReqs = append(allReqs, &cp)
			}

			updated, err := queue.PlanRotationTransition(event, allReqs, models.RotationModeFCFS, models.RotationModeFairPriority)
			if err != nil {
				t.Fatalf("PlanRotationTransition error: %v", err)
			}
			got := sortedActiveIDs(updated)
			if !equalStringSlices(got, tc.wantOrder) {
				t.Fatalf("PlanRotationTransition order = %v, want %v", got, tc.wantOrder)
			}
		})
	}
}

func TestFairPriority_CompletedAtPreservedOverLaterUpdatedAt(t *testing.T) {
	base := time.Date(2026, 11, 24, 21, 0, 0, 0, time.UTC)
	event := &models.Event{
		ID:           "ev-completed-at",
		RotationMode: models.RotationModeFairPriority,
	}

	// Alice completed FIRST (t+1m), but her completed request was edited later (UpdatedAt = t+20m).
	// Bob completed SECOND (t+5m) and has legacy CompletedAt == nil (falls back to UpdatedAt = t+5m).
	aliceCompletedAt := base.Add(1 * time.Minute)
	cAlice := &models.SongRequest{
		ID:          "c-alice",
		SingerName:  "Alice",
		Status:      models.StatusCompleted,
		CreatedAt:   base,
		CompletedAt: &aliceCompletedAt,
		UpdatedAt:   base.Add(20 * time.Minute), // edited later!
	}
	cBob := &models.SongRequest{
		ID:         "c-bob",
		SingerName: "Bob",
		Status:     models.StatusCompleted,
		CreatedAt:  base.Add(2 * time.Minute),
		UpdatedAt:  base.Add(5 * time.Minute), // legacy fallback when CompletedAt is nil
	}

	active := []*models.SongRequest{
		activeReq("s1", "Stage1", 1000, base.Add(6*time.Minute)),
		activeReq("s2", "Stage2", 2000, base.Add(7*time.Minute)),
		activeReq("s3", "Stage3", 3000, base.Add(8*time.Minute)),
		activeReq("bob-2", "Bob", 4000, base.Add(9*time.Minute)),
	}
	alice2 := activeReq("alice-2", "Alice", 0, base.Add(10*time.Minute))

	allReqs := append([]*models.SongRequest{cAlice, cBob}, active...)
	queue.PlaceAcceptedRequest(event, alice2, allReqs, alice2.CreatedAt)

	// Alice completed at t+1m (seq 1) < Bob at t+5m (seq 2), so Alice's 2nd song weaves ahead of Bob's 2nd song.
	if alice2.Rank >= active[3].Rank {
		t.Fatalf("expected Alice (CompletedAt t+1m, seq 1) ahead of Bob (UpdatedAt t+5m, seq 2), got alice=%d bob=%d", alice2.Rank, active[3].Rank)
	}
}
