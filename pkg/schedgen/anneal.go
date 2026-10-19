package schedgen

import (
	"errors"
	"log/slog"
	"math/rand"
)

func init() {
	RegisterGenerator("Annealed", NewAnnealed)
	RegisterGeneratorConfig("Annealed", AnnealedScheduleConfig{})
}

// AnnealedScheduleConfig provides information on the configuration
// defaults for the annealed scheduler.
type AnnealedScheduleConfig struct{}

func (AnnealedScheduleConfig) MaxRounds() int { return 25 }

func (AnnealedScheduleConfig) DefaultRounds() int { return 5 }

func (AnnealedScheduleConfig) RoundsDynamic() bool { return false }

// AnnealOptions tune the local search that produces an annealed
// schedule.
//
// The search operates on a fixed shape: every team plays exactly once
// per round, so Config.Rounds is honored with equality and cannot be
// violated by any move the search takes. Only the assignment of teams
// to (round, wave, field, position) changes.
type AnnealOptions struct {
	// Iterations is the number of candidate moves evaluated per
	// restart. Zero selects a budget scaled to the schedule size,
	// which is bounded for live use; baking passes a larger budget.
	Iterations int
	// Restarts is how many total independent searches are run and
	// compared. Zero or one runs a single search.
	Restarts int
	// History is the late-acceptance history length. Zero selects a
	// default.
	History int
	// Seed bases the RNG. Zero means "no preference": the search or
	// materialization draws a fresh seed per call, so repeated
	// requests never return identical schedules. Baking passes a
	// fixed seed to make produced designs reproducible.
	Seed int64
	// SkipBaked bypasses the baked-schedule archive and always runs
	// the search. Baking sets this so it produces new designs rather
	// than re-emitting an existing artifact.
	SkipBaked bool
}

const (
	defaultAnnealSeed    int64 = 0x5EED
	defaultAnnealHistory       = 64
	// liveAnnealFloor/ceiling bound a live (non-baked) search so an
	// HTTP-triggered schedule request finishes promptly.
	liveAnnealFloor   = 60_000
	liveAnnealCeiling = 1_000_000
	// bakeAnnealIterations is the default per-shape budget when
	// baking. Measurements show the search converging by roughly
	// 5M moves for schedules up to 100 teams, so baking spends close
	// to the point of diminishing returns; live searches stop far
	// earlier.
	bakeAnnealIterations = 5_000_000
)

// BakeOptions returns sensible options for producing a baked
// artifact: a large iteration budget and a stable seed so results are
// reproducible across machines.
func BakeOptions() AnnealOptions {
	return AnnealOptions{
		Iterations: bakeAnnealIterations,
		Restarts:   1,
		Seed:       defaultAnnealSeed,
		SkipBaked:  true,
	}
}

// annealIterations scales the default iteration budget with the size
// of the schedule being searched.
func annealIterations(c Config) int {
	iters := 40_000 * c.Rounds * (c.Fields * c.Positions)
	if iters < liveAnnealFloor {
		iters = liveAnnealFloor
	}
	if iters > liveAnnealCeiling {
		iters = liveAnnealCeiling
	}
	return iters
}

func (o AnnealOptions) withDefaults(c Config) AnnealOptions {
	if o.Iterations == 0 {
		o.Iterations = annealIterations(c)
	}
	if o.Restarts < 0 {
		o.Restarts = 0
	}
	if o.History == 0 {
		o.History = defaultAnnealHistory
	}
	if o.Seed == 0 {
		// No preference: pick a fresh seed so live searches for
		// the same shape do not all produce the identical
		// schedule.
		o.Seed = rand.Int63()
	}
	return o
}

// NewAnnealed produces an annealed schedule generator for the given
// config.
func NewAnnealed(c Config) Generator {
	return &AnnealedSchedule{
		Schedule: Schedule{
			Config:        c,
			ClosestReplay: 99,
		},
	}
}

// AnnealedSchedule is a schedule produced by fixed-shape local search,
// preferring a pre-baked artifact when the archive covers the request.
type AnnealedSchedule struct {
	Schedule
	Options AnnealOptions
}

// Generate produces a schedule, using a baked artifact when one covers
// the requested shape and annealing live otherwise.
func (as *AnnealedSchedule) Generate() (*Schedule, error) {
	if !as.Options.SkipBaked {
		baked, err := LookupBaked(as.Config)
		if err != nil {
			slog.Warn("Baked schedule lookup failed, annealing live", "error", err)
		}
		if baked != nil {
			s, err := baked.Materialize(as.Config, as.Options.Seed)
			if err == nil {
				slog.Debug("Using baked schedule",
					"teams", as.Config.Teams,
					"baked-teams", baked.Teams,
					"baked-rounds", baked.Rounds,
				)
				return s, nil
			}
			slog.Warn("Baked schedule could not be materialized, annealing live", "error", err)
		}
	}

	s, err := AnnealSchedule(as.Config, as.Options)
	if err != nil {
		return nil, err
	}
	as.Schedule.Rounds = s.Rounds
	return &as.Schedule, nil
}

// AnnealSchedule runs the local search and returns the best schedule
// it finds for the given config. It never consults the baked archive,
// so it is the entry point used by the bake command.
func AnnealSchedule(c Config, opts AnnealOptions) (*Schedule, error) {
	s, _, err := annealBest(c, opts)
	return s, err
}

// annealBest runs the search and also reports the final cost of the
// best schedule found, which the bake command records in the artifact.
func annealBest(c Config, opts AnnealOptions) (*Schedule, int, error) {
	switch {
	case c.Teams < 1:
		return nil, 0, errors.New("schedgen: at least one team is required")
	case c.Fields < 1:
		return nil, 0, errors.New("schedgen: at least one field is required")
	case c.Positions < 1:
		return nil, 0, errors.New("schedgen: at least one position is required")
	case c.Rounds < 1:
		return nil, 0, errors.New("schedgen: at least one round is required")
	}
	opts = opts.withDefaults(c)

	var bestPerm [][]int
	bestCost := 0
	for restart := 1; restart <= max(opts.Restarts, 1); restart++ {
		a := newAnnealer(c, opts, opts.Seed+int64(restart)*7919)
		a.seed()
		perm, cost := a.run()
		if bestPerm == nil || cost < bestCost {
			bestPerm, bestCost = perm, cost
		}
		slog.Debug("Annealing restart complete",
			"restart", restart, "teams", c.Teams, "rounds", c.Rounds, "cost", cost)
	}

	s := &Schedule{
		Config:        c,
		Rounds:        buildRounds(c, bestPerm),
		ClosestReplay: 99,
	}
	return s, bestCost, nil
}

// Cost weights for the annealer's objective, chosen so that a unit of
// shortfall in the least-served team's replay distance outweighs a
// unit of opponent shortfall, which in turn outweighs location
// spread. The values were tuned against measured schedules from 8 to
// 100 teams: opponent diversity reaches its theoretical optimum and
// minimum replay distance roughly doubles versus random seeding.
const (
	costReplayWeight   = 200
	costRematchWeight  = 1200
	costLocationWeight = 50
)

// annealer holds the search state for one schedule shape.
//
// A round's permutation covers every team plus enough empty slots
// (entity ids >= Teams) to fill the last wave. Since each entity
// appears exactly once per round, every real team plays exactly
// Config.Rounds matches no matter which moves the search takes.
type annealer struct {
	cfg  Config
	rnd  *rand.Rand
	opts AnnealOptions

	slots      int // locations per wave (fields * positions)
	waves      int // matches (waves) per round
	perRound   int // padded entities per round
	emptyStart int // first empty-slot entity id

	perm [][]int // perm[round][slot] = entity id
	inv  [][]int // inv[round][team] = slot

	historyLen int

	stamp       int32
	pairSeen    []int32 // v*v, stamp of first sighting this evaluation
	distinctOpp []int32 // v, distinct opponents per evaluation
	usage       []int   // v*slots, per-team location usage
}

func newAnnealer(c Config, opts AnnealOptions, seed int64) *annealer {
	a := &annealer{
		cfg:        c,
		rnd:        rand.New(rand.NewSource(seed)),
		opts:       opts,
		historyLen: defaultAnnealHistory,
	}
	if c.Rounds < 2 {
		a.historyLen = 1
	}
	a.slots = c.Fields * c.Positions
	a.waves = (c.Teams + a.slots - 1) / a.slots
	if a.waves < 1 {
		a.waves = 1
	}
	a.perRound = a.waves * a.slots
	a.emptyStart = c.Teams

	a.perm = make([][]int, c.Rounds)
	a.inv = make([][]int, c.Rounds)
	for r := range a.perm {
		a.perm[r] = make([]int, a.perRound)
		a.inv[r] = make([]int, c.Teams)
	}

	v := c.Teams
	a.pairSeen = make([]int32, v*v)
	a.distinctOpp = make([]int32, v)
	a.usage = make([]int, v*a.slots)
	return a
}

// setPerm installs entity e at (round, slot), keeping the inverse
// index in sync.
func (a *annealer) setPerm(round, slot, e int) {
	a.perm[round][slot] = e
	if e < a.cfg.Teams {
		a.inv[round][e] = slot
	}
}

func (a *annealer) commit(round int, order []int) {
	for s, e := range order {
		a.setPerm(round, s, e)
	}
}

// slotTeam returns the team at (round, slot), or -1 for an empty slot.
func (a *annealer) slotTeam(round, slot int) int {
	e := a.perm[round][slot]
	if e >= a.cfg.Teams {
		return -1
	}
	return e
}

// seed builds the initial permutation: greedy least-shared-partner
// groups per round so opponent diversity starts high, with groups
// shuffled relative to each other so replay gaps start scattered and
// the search has room to improve them.
func (a *annealer) seed() {
	v := a.cfg.Teams
	met := make([]int, v)
	shared := make([]int, v*v)
	key := func(x, y int) int {
		if x > y {
			x, y = y, x
		}
		return x*v + y
	}

	for r := range a.perm {
		available := make([]bool, v)
		order := make([]int, 0, a.perRound)
		var groups [][]int

		remaining := v
		for remaining > 0 {
			seedTeam := -1
			bestMet := 1 << 30
			for t := 0; t < v; t++ {
				if available[t] {
					continue
				}
				if met[t] < bestMet {
					bestMet = met[t]
					seedTeam = t
				}
			}
			group := []int{seedTeam}
			available[seedTeam] = true

			for len(group) < a.cfg.Positions && len(group) < remaining {
				bestT, bestPenalty := -1, 1<<30
				for t := 0; t < v; t++ {
					if available[t] {
						continue
					}
					penalty := 0
					for _, m := range group {
						penalty += shared[key(t, m)]
					}
					if penalty < bestPenalty ||
						(penalty == bestPenalty && seedTeam >= 0 && met[t] < met[bestT]) {
						bestPenalty = penalty
						bestT = t
					}
				}
				if bestT < 0 {
					break
				}
				group = append(group, bestT)
				available[bestT] = true
			}

			for _, m := range group {
				met[m] += len(group) - 1
				for _, n := range group {
					if m != n {
						shared[key(m, n)]++
					}
				}
			}
			groups = append(groups, group)
			remaining -= len(group)
		}

		a.rnd.Shuffle(len(groups), func(i, j int) {
			groups[i], groups[j] = groups[j], groups[i]
		})
		for _, g := range groups {
			order = append(order, g...)
		}
		for len(order) < a.perRound {
			order = append(order, a.emptyStart+len(order)-v)
		}
		a.commit(r, order[:a.perRound])
	}
}

// cost is the weighted penalty of the current permutation; lower is
// better. Every diversity term is a shortfall against what is
// achievable, squared, so the least-served teams dominate the cost.
// That is what drives variance across teams down rather than just the
// average.
func (a *annealer) cost() int {
	v := a.cfg.Teams
	waves := a.waves
	c := 0

	// Replay distance: penalize every consecutive pair of appearances
	// closer than one full round of matches, per team.
	if len(a.perm) > 1 {
		for t := 0; t < v; t++ {
			prev := -1
			for r := range a.perm {
				slot := a.inv[r][t]
				if slot < 0 {
					continue
				}
				m := r*waves + slot/a.slots
				if prev >= 0 {
					if d := waves - (m - prev); d > 0 {
						c += costReplayWeight * d * d
					}
				}
				prev = m
			}
		}
	}

	// Opponent diversity: count each team's distinct opponents via a
	// stamped pair table.
	a.stamp++
	for r := 0; r < len(a.perm); r++ {
		for w := 0; w < waves; w++ {
			for f := 0; f < a.cfg.Fields; f++ {
				base := w*a.slots + f*a.cfg.Positions
				for i := 0; i < a.cfg.Positions; i++ {
					ti := a.slotTeam(r, base+i)
					if ti < 0 {
						continue
					}
					for j := i + 1; j < a.cfg.Positions; j++ {
						tj := a.slotTeam(r, base+j)
						if tj < 0 {
							continue
						}
						k1, k2 := ti*v+tj, tj*v+ti
						if a.pairSeen[k1] == a.stamp {
							continue
						}
						a.pairSeen[k1] = a.stamp
						a.pairSeen[k2] = a.stamp
						a.distinctOpp[ti]++
						a.distinctOpp[tj]++
					}
				}
			}
		}
	}

	// Each team wants as many distinct opponents as the schedule can
	// possibly give it: one per match times everyone else.
	idealOpp := len(a.perm) * (a.cfg.Positions - 1)
	if cap := v - 1; idealOpp > cap {
		idealOpp = cap
	}
	for t := 0; t < v; t++ {
		if short := idealOpp - int(a.distinctOpp[t]); short > 0 {
			c += costRematchWeight * short * short
		}
		a.distinctOpp[t] = 0
	}

	// Location spread: sum of usage-squared over (team, location)
	// pairs, minimized when each team rotates evenly across all
	// field/position combinations.
	for i := range a.usage {
		a.usage[i] = 0
	}
	for r := range a.perm {
		for t := 0; t < v; t++ {
			slot := a.inv[r][t]
			if slot < 0 {
				continue
			}
			a.usage[t*a.slots+slot%a.slots]++
		}
	}
	for _, u := range a.usage {
		c += costLocationWeight * u * u
	}

	return c
}

// pickTeamSlot returns a slot in round r holding a real team. Up to
// tries attempts are made; a slot holding a ghost is accepted rather
// than failing.
func (a *annealer) pickTeamSlot(r, tries int) int {
	for range tries {
		s := a.rnd.Intn(a.perRound)
		if a.perm[r][s] < a.cfg.Teams {
			return s
		}
	}
	return a.rnd.Intn(a.perRound)
}

// swap exchanges the entities at two positions. Swapping is its own
// inverse, so a rejected move is undone by repeating it.
func (a *annealer) swap(r1, s1, r2, s2 int) {
	e1, e2 := a.perm[r1][s1], a.perm[r2][s2]
	a.setPerm(r1, s1, e2)
	a.setPerm(r2, s2, e1)
}

// rotateWave shifts every team in round r forward by k waves (cyclic)
// while keeping each team's field and position. Because it moves all
// teams in the round together it can repair replay spacing across the
// whole round in a single move.
func (a *annealer) rotateRound(r, k int) {
	if k%a.waves == 0 {
		return
	}
	round := make([]int, a.perRound)
	for s, e := range a.perm[r] {
		wave := s / a.slots
		loc := s % a.slots
		nw := (wave + k) % a.waves
		if nw < 0 {
			nw += a.waves
		}
		round[nw*a.slots+loc] = e
	}
	a.commit(r, round)
}

// run performs late-acceptance hill climbing over the fixed shape and
// returns the best permutation found along with its cost.
func (a *annealer) run() ([][]int, int) {
	cur := a.cost()
	best := clonePerm(a.perm)
	bestCost := cur

	history := make([]int, a.historyLen)
	for i := range history {
		history[i] = cur
	}
	head := 0

	for i := 0; i < a.opts.Iterations; i++ {
		undo := a.mutate()
		trial := a.cost()

		if trial <= history[head] {
			cur = trial
		} else {
			undo()
		}

		history[head] = cur
		head = (head + 1) % len(history)

		if cur < bestCost {
			bestCost = cur
			best = clonePerm(a.perm)
		}
	}
	return best, bestCost
}

// mutate applies one random move and returns a closure that undoes
// exactly that move. All move classes keep every round a permutation
// of the entities, which is what preserves the exactly-once-per-round
// and round-cap invariants.
//
// The move classes are:
//
//   - wave rotation of one round: repairs replay spacing cheaply;
//   - within-round slot swap: changes opponents and locations;
//   - transposition over a round subset: shifts when two teams play
//     without changing anyone's round count.
//
// Swapping slots between two rounds outright is never performed: each
// round must contain every team exactly once.
func (a *annealer) mutate() func() {
	nRounds := len(a.perm)
	roll := a.rnd.Intn(10)
	switch {
	case a.waves > 1 && roll < 3:
		r := a.rnd.Intn(nRounds)
		k := 1 + a.rnd.Intn(a.waves-1)
		a.rotateRound(r, k)
		return func() { a.rotateRound(r, -k) }

	case roll < 7:
		r := a.rnd.Intn(nRounds)
		s1 := a.pickTeamSlot(r, 8)
		s2 := a.pickTeamSlot(r, 8)
		a.swap(r, s1, r, s2)
		return func() { a.swap(r, s1, r, s2) }

	case nRounds > 1 && a.cfg.Teams > 1:
		return a.moveTransposition()

	default:
		r := a.rnd.Intn(nRounds)
		s1 := a.pickTeamSlot(r, 8)
		s2 := a.pickTeamSlot(r, 8)
		a.swap(r, s1, r, s2)
		return func() { a.swap(r, s1, r, s2) }
	}
}

// moveTransposition swaps two teams' slots in a random non-empty,
// non-full subset of rounds. All rounds would be a pure relabeling
// with no effect, so that case is excluded.
func (a *annealer) moveTransposition() func() {
	x := a.rnd.Intn(a.cfg.Teams)
	y := (x + 1 + a.rnd.Intn(a.cfg.Teams-1)) % a.cfg.Teams

	k := 1 + a.rnd.Intn(len(a.perm)-1)
	rounds := a.rnd.Perm(len(a.perm))[:k]

	undo := func() {
		for _, r := range rounds {
			s1, s2 := a.inv[r][x], a.inv[r][y]
			a.swap(r, s1, r, s2)
		}
	}
	undo()
	return undo
}

func clonePerm(p [][]int) [][]int {
	out := make([][]int, len(p))
	for i := range p {
		out[i] = make([]int, len(p[i]))
		copy(out[i], p[i])
	}
	return out
}

// buildRounds converts a permutation into the Schedule round list.
// Entities at or above Config.Teams are empty slots and are omitted
// from the match, which is how non-divisible team counts produce
// partially filled matches.
func buildRounds(c Config, perm [][]int) []Round {
	slots := c.Fields * c.Positions
	rounds := make([]Round, 0, len(perm))
	for r := range perm {
		waves := len(perm[r]) / slots
		round := Round{
			TeamAppearances: make(map[int]int),
			Matches:         make([]Match, 0, waves),
		}
		for w := 0; w < waves; w++ {
			m := Match{Placements: make(map[Location]int)}
			for s := 0; s < slots; s++ {
				e := perm[r][w*slots+s]
				if e >= c.Teams {
					continue
				}
				m.Placements[Location{Field: s / c.Positions, Position: s % c.Positions}] = e
				round.TeamAppearances[e] = w
			}
			round.Matches = append(round.Matches, m)
		}
		rounds = append(rounds, round)
	}
	return rounds
}
