package schedgen

import (
	"log/slog"
	"math/rand"
)

func init() {
	RegisterGenerator("BID", NewBID)
	RegisterGeneratorConfig("BID", BIDScheduleConfig{})
}

// BIDScheduleConfig provides information on the configuration
// defaults for a Balanced Incomplete Block Design scheduler.
type BIDScheduleConfig struct{}

func (c BIDScheduleConfig) MaxRounds() int { return 100 }

func (c BIDScheduleConfig) DefaultRounds() int { return 1 }

func (c BIDScheduleConfig) RoundsDynamic() bool { return true }

func NewBID(c Config) Generator {
	return &BIDSchedule{
		Schedule: Schedule{
			Config:        c,
			ClosestReplay: 99,
		},
	}
}

// BIDSchedule generates a Balanced Incomplete Block Design schedule
// where block size k = Fields * Positions, and every pair of teams
// appears together in exactly λ=1 block. Matches are shuffled to
// produce even spacing between a team's consecutive appearances.
type BIDSchedule struct {
	Schedule
}

// Generate produces a BIBD-based schedule with the minimum number
// of matches needed so every pair of teams plays together once.
func (b *BIDSchedule) Generate() *Schedule {
	k := b.Config.Fields * b.Config.Positions
	v := b.Config.Teams

	// Validate BIBD feasibility: need v > k for a non-trivial design.
	if v <= k {
		slog.Warn("Team count <= block size, falling back to single round",
			"teams", v, "block_size", k)
		fallback := RandomSchedule{
			Schedule: Schedule{
				Config:        b.Config,
				ClosestReplay: 99,
			},
		}
		// Generate single round
		r := fallback.GenerateRound()
		b.Rounds = []Round{r}
		b.RoundsDynamic = true
		return &b.Schedule
	}

	// For λ=1 BIBD:
	//   r = (v-1) / (k-1)  – each team plays r matches
	//   b = v * r / k      – total blocks (matches) needed
	// Valid only when (v-1) % (k-1) == 0 and (v*r) % k == 0.
	rVal := (v - 1) / (k - 1)
	remainder := (v - 1) % (k - 1)

	totalBlocks := v * rVal / k
	blockRemainder := (v * rVal) % k

	if remainder != 0 || blockRemainder != 0 {
		// Parameters don't form a clean BIBD. Round up r so every
		// pair is covered, then fill remaining slots.
		rVal = (v - 1 + k - 2) / (k - 1) // ceiling division
		totalBlocks = v * rVal / k
		if (v*rVal)%k != 0 {
			totalBlocks++
		}
		slog.Warn("Non-standard BIBD parameters, approximating",
			"teams", v, "block_size", k, "r", rVal, "blocks", totalBlocks)
	}

	// Build all unordered pairs of teams.
	pairs := b.allPairs(v)

	// Try cyclic BIBD construction first (exact when parameters allow),
	// then fall back to greedy.
	blocks := b.cyclicBIBD(pairs, v, k, totalBlocks)
	if blocks == nil {
		blocks = b.greedyBID(pairs, v, k, totalBlocks)
	}

	// Convert blocks into matches with field/position placements.
	slots := []Location{}
	for f := range b.Config.Fields {
		for p := range b.Config.Positions {
			slots = append(slots, Location{f, p})
		}
	}

	allMatches := make([]Match, 0, len(blocks))
	for _, block := range blocks {
		// Shuffle team order within the block so positions are randomised.
		rand.Shuffle(len(block), func(i, j int) {
			block[i], block[j] = block[j], block[i]
		})

		m := Match{Placements: make(map[Location]int)}
		for i, team := range block {
			loc := slots[i%len(slots)]
			m.Placements[loc] = team
		}
		allMatches = append(allMatches, m)
	}

	// Shuffle matches to space out team appearances. Use a
	// round-robin interleaving based on team appearance order so
	// teams don't play back-to-back.
	shuffled := b.spaceMatches(allMatches, blocks)

	// Build a single round from the shuffled matches.
	rnd := Round{
		Matches:         shuffled,
		TeamAppearances: make(map[int]int),
	}
	for i, m := range shuffled {
		for _, team := range m.Placements {
			if _, seen := rnd.TeamAppearances[team]; !seen {
				rnd.TeamAppearances[team] = i
			}
		}
	}

	b.Rounds = []Round{rnd}
	b.RoundsDynamic = true
	return &b.Schedule
}

// allPairs returns every unordered pair {a, b} with a < b.
func (b *BIDSchedule) allPairs(v int) [][2]int {
	pairs := make([][2]int, 0, v*(v-1)/2)
	for i := 0; i < v; i++ {
		for j := i + 1; j < v; j++ {
			pairs = append(pairs, [2]int{i, j})
		}
	}
	return pairs
}

// allPairsCovered checks whether every pair appears in at least one block.
func (b *BIDSchedule) allPairsCovered(pairs [][2]int, blocks [][]int) bool {
	covered := make(map[[2]int]bool)
	for _, blk := range blocks {
		for _, p := range b.pairsFromBlock(blk) {
			covered[p] = true
		}
	}
	for _, p := range pairs {
		if !covered[p] {
			return false
		}
	}
	return true
}

// cyclicBIBD attempts exact cyclic construction for Steiner Triple Systems
// (v, k=3, λ=1). Returns nil when parameters don't support it, leaving
// greedy as the fallback.
func (b *BIDSchedule) cyclicBIBD(pairs [][2]int, v, k, targetBlocks int) [][]int {
	// Only handle Steiner Triple Systems: k=3, λ=1.
	if k != 3 {
		return nil
	}
	if (v-1)%6 != 0 && (v-3)%6 != 0 {
		return nil
	}

	if v%6 == 1 {
		return b.cyclicSTS(v)
	}
	if v%6 == 3 {
		return b.cyclicSTSM3(v)
	}
	return nil
}

// cyclicSTS builds a Steiner Triple System for v ≡ 1 (mod 6)
// using (v-1)/6 base blocks, each developed mod v.
func (b *BIDSchedule) cyclicSTS(v int) [][]int {
	// Number of base blocks needed.
	numBase := (v - 1) / 6
	blocks := b.findSTSBaseBlocks(v, numBase)
	if blocks == nil {
		return nil
	}

	result := [][]int{}
	for _, base := range blocks {
		for s := 0; s < v; s++ {
			blk := []int{
				(base[0] + s) % v,
				(base[1] + s) % v,
				(base[2] + s) % v,
			}
			result = append(result, blk)
		}
	}
	return result
}

// cyclicSTSM3 builds a Steiner Triple System for v ≡ 3 (mod 6)
// using one full-orbit base block plus (v-3)/2 short-orbit base blocks.
func (b *BIDSchedule) cyclicSTSM3(v int) [][]int {
	// For v=7, classic base block {0,1,3}
	if v == 7 {
		return b.cyclicSTSM3Generic(v, [][3]int{{0, 1, 3}})
	}
	// For other v ≡ 3 mod 6, try to find base blocks.
	// Hard-coded known good base blocks for small values.
	switch v {
	case 9:
		return b.cyclicSTSM3Generic(v, [][3]int{{0, 1, 3}, {0, 4, 5}})
	case 13:
		return b.cyclicSTSM3Generic(v, [][3]int{{0, 1, 3}, {0, 5, 6}, {0, 2, 8}, {0, 4, 10}})
	case 15:
		return b.cyclicSTSM3Generic(v, [][3]int{{0, 1, 4}, {0, 5, 7}, {0, 3, 11}, {0, 6, 13}, {0, 2, 9}})
	default:
		return nil
	}
}

// cyclicSTSM3Generic develops base blocks for v ≡ 3 (mod 6).
// The first base block generates v blocks (full orbit).
// Remaining base blocks generate (v-1)/2 blocks (short orbit).
func (b *BIDSchedule) cyclicSTSM3Generic(v int, bases [][3]int) [][]int {
	half := (v - 1) / 2
	result := [][]int{}

	for i, base := range bases {
		if i == 0 {
			// Full orbit: v shifts
			for s := 0; s < v; s++ {
				result = append(result, []int{
					(base[0] + s) % v,
					(base[1] + s) % v,
					(base[2] + s) % v,
				})
			}
		} else {
			// Short orbit: (v-1)/2 shifts
			for s := 0; s < half; s++ {
				blk := []int{
					(base[0] + s) % v,
					(base[1] + s) % v,
					(base[2] + s) % v,
				}
				// Add the block and its "complement" shift (half-shift)
				result = append(result, blk)
			}
		}
	}
	return result
}

// findSTSBaseBlocks searches for base blocks that cover all differences
// exactly once for a cyclic STS(v).
func (b *BIDSchedule) findSTSBaseBlocks(v, numBase int) [][]int {
	// Each base block {a,b,c} produces differences:
	// ±(b-a), ±(c-b), ±(c-a) mod v → 3 pairs of differences.
	// numBase blocks must cover all (v-1)/2 difference pairs exactly once.
	totalDiffPairs := (v - 1) / 2
	if numBase*3 != totalDiffPairs {
		return nil
	}

	// Pre-compute all candidate triplets (0, j, k) with 0 < j < k < v/2
	type triplet struct{ j, k int }
	var candidates []triplet
	for j := 1; j < v; j++ {
		for k := j + 1; k < v; k++ {
			candidates = append(candidates, triplet{j, k})
		}
	}

	// Backtrack search
	usedDiffs := make(map[int]bool)
	result := make([][]int, 0, numBase)

	var diffsFromTriplet func(j, k int) []int
	diffsFromTriplet = func(j, k int) []int {
		d1 := (j - 0) % v; if d1 < 0 { d1 += v }
		d2 := (k - j) % v; if d2 < 0 { d2 += v }
		d3 := (k - 0) % v; if d3 < 0 { d3 += v }
		// Store smaller half-rep of each difference
		out := []int{}
		for _, d := range []int{d1, d2, d3} {
			if d > v/2 { d = v - d }
			out = append(out, d)
		}
		return out
	}

	var search func(startIdx int) bool
	search = func(startIdx int) bool {
		if len(result) == numBase {
			return true
		}
		for idx := startIdx; idx < len(candidates); idx++ {
			c := candidates[idx]
			diffs := diffsFromTriplet(c.j, c.k)
			conflict := false
			seenDiff := make(map[int]bool)
			for _, d := range diffs {
				if usedDiffs[d] || seenDiff[d] {
					conflict = true
					break
				}
				seenDiff[d] = true
			}
			if conflict {
				continue
			}
			// Accept
			for _, d := range diffs {
				usedDiffs[d] = true
			}
			result = append(result, []int{0, c.j, c.k})
			if search(idx + 1) {
				return true
			}
			result = result[:len(result)-1]
			for _, d := range diffs {
				usedDiffs[d] = false
			}
		}
		return false
	}

	if !search(0) {
		return nil
	}
	return result
}

// greedyBID constructs blocks greedily so that every pair is covered
// at least once, targeting the minimum block count.
func (b *BIDSchedule) greedyBID(pairs [][2]int, v, k, targetBlocks int) [][]int {
	// Track which pairs are already covered.
	covered := make(map[[2]int]bool)
	covCount := 0
	blocks := [][]int{}

	// Pre-compute pair indices for each team for faster lookup.
	teamPairs := make([][][2]int, v)
	for _, p := range pairs {
		teamPairs[p[0]] = append(teamPairs[p[0]], p)
		teamPairs[p[1]] = append(teamPairs[p[1]], p)
	}

	for len(blocks) < targetBlocks && covCount < len(pairs) {
		bestBlock := []int{}
		bestScore := 0

		// Try multiple random seeds to find the best block.
		for range 200 {
			candidate := b.randomBlock(v, k, covered, pairs)
			score := 0
			for _, p := range b.pairsFromBlock(candidate) {
				if !covered[p] {
					score++
				}
			}
			if score > bestScore {
				bestScore = score
				bestBlock = candidate
			}
		}

		blocks = append(blocks, bestBlock)
		for _, p := range b.pairsFromBlock(bestBlock) {
			if !covered[p] {
				covered[p] = true
				covCount++
			}
		}
	}

	// If we still have uncovered pairs, add extra blocks to cover them.
	remaining := [][2]int{}
	for _, p := range pairs {
		if !covered[p] {
			remaining = append(remaining, p)
		}
	}

	for len(remaining) > 0 {
		block := []int{}
		used := make(map[int]bool)

		// Pick teams from remaining pairs.
		for len(block) < k && len(remaining) > 0 {
			// Pick the pair that shares a team with the current block
			// if possible, otherwise pick any remaining pair.
			idx := -1
			for i, p := range remaining {
				if len(block) > 0 && (used[p[0]] || used[p[1]]) {
					idx = i
					break
				}
			}
			if idx == -1 {
				idx = rand.Intn(len(remaining))
			}

			p := remaining[idx]
			remaining = append(remaining[:idx], remaining[idx+1:]...)

			for _, t := range p {
				if !used[t] {
					block = append(block, t)
					used[t] = true
				}
			}
		}

		// Fill remaining slots in the block with unused teams.
		for len(block) < k {
			for t := 0; t < v; t++ {
				if !used[t] {
					block = append(block, t)
					used[t] = true
					break
				}
			}
		}

		blocks = append(blocks, block)
		for _, p := range b.pairsFromBlock(block) {
			covered[p] = true
		}
	}

	return blocks
}

// randomBlock picks k teams from v, biasing toward uncovered pairs.
func (b *BIDSchedule) randomBlock(v, k int, covered map[[2]int]bool, pairs [][2]int) []int {
	// Score each team by how many uncovered pairs it has.
	scores := make([]int, v)
	for _, p := range pairs {
		if !covered[p] {
			scores[p[0]]++
			scores[p[1]]++
		}
	}

	block := make([]int, 0, k)
	used := make(map[int]bool, k)

	for len(block) < k {
		// Weighted random selection by score.
		total := 0
		for t := 0; t < v; t++ {
			if !used[t] {
				total += scores[t] + 1 // +1 so unscored teams can still be picked
			}
		}
		if total == 0 {
			total = v
		}

		r := rand.Intn(total)
		for t := 0; t < v; t++ {
			if used[t] {
				continue
			}
			weight := scores[t] + 1
			if r < weight {
				block = append(block, t)
				used[t] = true
				break
			}
			r -= weight
		}
	}

	return block
}

// pairsFromBlock returns all unordered pairs within a block.
func (b *BIDSchedule) pairsFromBlock(block []int) [][2]int {
	out := make([][2]int, 0, len(block)*(len(block)-1)/2)
	for i := 0; i < len(block); i++ {
		for j := i + 1; j < len(block); j++ {
			a, c := block[i], block[j]
			if a > c {
				a, c = c, a
			}
			out = append(out, [2]int{a, c})
		}
	}
	return out
}

// spaceMatches reorders matches so each team has a roughly even gap
// between consecutive appearances, avoiding back-to-back play.
func (b *BIDSchedule) spaceMatches(matches []Match, blocks [][]int) []Match {
	if len(matches) <= 1 {
		return matches
	}

	// Build team → list of match indices.
	teamMatches := make(map[int][]int)
	for i, blk := range blocks {
		for _, t := range blk {
			teamMatches[t] = append(teamMatches[t], i)
		}
	}

	// Find max appearances any team makes.
	maxAppear := 0
	for _, list := range teamMatches {
		if len(list) > maxAppear {
			maxAppear = len(list)
		}

		// Shuffle within each team's match list.
		rand.Shuffle(len(list), func(i, j int) {
			list[i], list[j] = list[j], list[i]
		})
	}

	// Interleave: pick match for slot s from the team whose
	// appearance for this slot hasn't been placed yet, cycling
	// through teams round-robin.
	placed := make(map[int]bool)
	result := make([]Match, 0, len(matches))

	for slot := 0; slot < maxAppear; slot++ {
		// Collect all candidate matches for this slot round.
		candidates := []int{}
		for _, list := range teamMatches {
			if slot < len(list) {
				midx := list[slot]
				if !placed[midx] {
					placed[midx] = true
					candidates = append(candidates, midx)
				}
			}
		}

		// Shuffle candidates to randomize order within the slot.
		rand.Shuffle(len(candidates), func(i, j int) {
			candidates[i], candidates[j] = candidates[j], candidates[i]
		})

		for _, midx := range candidates {
			result = append(result, matches[midx])
		}
	}

	// Place any remaining unplaced matches.
	for i := range matches {
		if !placed[i] {
			placed[i] = true
			result = append(result, matches[i])
		}
	}

	return result
}
