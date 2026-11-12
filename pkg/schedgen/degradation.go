package schedgen

import "fmt"

// Dynamic position degradation lets a generator schedule matches on
// fewer than the physical maximum number of positions per field when
// the team count does not divide evenly into full waves.
//
// Width is chosen by structural tiers, never by the search cost
// function: cost() sums the location-spread term over teams x slots,
// so a narrower shape mechanically scores worse regardless of schedule
// quality and the numbers are not comparable across shapes. Cost may
// rank permutations within one fixed width; across widths only the
// tier arithmetic below decides.

// WidthTier ranks how cleanly a team count divides into full waves at
// a given slot width, best tier first.
type WidthTier int

const (
	// TierExact means every wave is completely full.
	TierExact WidthTier = iota
	// TierShortWave means the final wave is partial but holds at
	// least two teams, so every team still gets opponents.
	TierShortWave
	// TierSoloWave means the final wave holds a single team that
	// plays nobody.
	TierSoloWave
)

// String returns an operator-readable name for the tier.
func (t WidthTier) String() string {
	switch t {
	case TierExact:
		return "exact"
	case TierShortWave:
		return "short-wave"
	case TierSoloWave:
		return "solo-wave"
	default:
		return "unknown"
	}
}

// WidthOption describes one candidate match width and how the team
// count fits into waves of that width.
type WidthOption struct {
	// Positions is the per-field match width (active positions).
	Positions int
	// Tier is how the team count divides into waves of this width.
	Tier WidthTier
	// OpponentCeiling is the most distinct opponents any team can
	// accumulate across the requested rounds at this width.
	OpponentCeiling int
	// Waves is the number of waves each round needs.
	Waves int
	// LastWaveTeams is how many teams play in the final wave.
	LastWaveTeams int
	// Reason is operator-facing prose explaining the fit.
	Reason string
}

// WidthReport is the outcome of selecting an active width.
type WidthReport struct {
	// Chosen is the option the generator should schedule with.
	Chosen WidthOption
	// Considered lists every candidate, widest first.
	Considered []WidthOption
	// Degraded is true when the chosen width is narrower than the
	// physical maximum.
	Degraded bool
}

// TierOf classifies how teams divide into waves of slots slots.
func TierOf(teams, slots int) WidthTier {
	if slots <= 0 {
		return TierSoloWave
	}
	switch teams % slots {
	case 0:
		return TierExact
	case 1:
		return TierSoloWave
	default:
		return TierShortWave
	}
}

// OpponentCeiling returns the most distinct opponents a team can see
// in rounds rounds at width positions against teams opponents: one
// match per round contributes positions-1 opponents, capped by the
// size of the field.
func OpponentCeiling(rounds, positions, teams int) int {
	c := rounds * (positions - 1)
	if teams-1 < c {
		c = teams - 1
	}
	if c < 0 {
		c = 0
	}
	return c
}

// TierFunc classifies how a team count fits a slot count, per
// generator structure. It lets each generator judge width by the
// shape its algorithm actually produces.
type TierFunc func(teams, slots int) WidthTier

// WidthDegradable is an optional interface a Generator may implement
// to allow scheduling matches on fewer than the physical number of
// positions per field. Returning a nil TierFunc (or not implementing
// the interface at all) means the generator always plays at the full
// physical width.
type WidthDegradable interface {
	// DegradesWidths returns the fit function the generator wants
	// width judged by, or nil to opt out.
	DegradesWidths() TierFunc
}

// WidthCoverage is an optional interface a generator may also
// implement when it schedules until every pair of teams has met
// (BIBD-style coverage) rather than for a fixed round count: such a
// generator adds matches, not rounds, when the width narrows, so
// every candidate width keeps the full opponent ceiling and the
// ceiling guard never binds.
type WidthCoverage interface {
	CoversAllPairs() bool
}

// ResolveWidth picks the active match width for a config and returns
// the config to hand the generator along with the decision report. A
// generator that has not opted in, and an operator-pinned
// ActivePositions, both bypass the selection entirely.
func ResolveWidth(c Config, tier TierFunc, coverage bool) (Config, WidthReport) {
	if tier == nil || c.ActivePositions != 0 {
		return c, WidthReport{Chosen: WidthOption{Positions: c.ActiveWidth()}}
	}
	rounds := c.Rounds
	if coverage {
		// Coverage generators grow the match count instead of the
		// round count, so every width tops out at the field-size
		// ceiling: pass a rounds value large enough to pin the
		// ceiling there for every candidate.
		rounds = c.Teams
	}
	rep := SelectWidth(c.Teams, c.Fields, c.Positions, rounds, tier)
	c.ActivePositions = rep.Chosen.Positions
	return c, rep
}

// physicalPosition maps the activePos-th position of a wave played at
// width onto a physical position index, rotating by the wave index so
// a narrowed match width still visits every physical position across
// waves instead of leaving one permanently dark. A width that already
// spans every physical position maps identically.
func physicalPosition(activePos, wave, width, physical int) int {
	if width >= physical {
		return activePos
	}
	return (activePos + wave) % physical
}

// DivisorTier tiers by the wave remainder: a remainder of 1 leaves a
// solo wave where a team plays nobody, which is the defect
// degradation exists to avoid. Used by wave-based generators such as
// the annealer.
func DivisorTier(teams, slots int) WidthTier {
	return TierOf(teams, slots)
}

// BIBDTier tiers by the lambda=1 block design feasibility rule: a
// balanced design with block size slots exists exactly when
// (teams-1) is divisible by (slots-1). Used by the BID generator.
func BIBDTier(teams, slots int) WidthTier {
	if slots < 2 {
		return TierSoloWave
	}
	if (teams-1)%(slots-1) == 0 {
		return TierExact
	}
	return TierShortWave
}

// CandidateWidths returns every candidate active width from
// maxPositions down to 2, widest first, scored with DivisorTier and
// the absolute opponent ceiling (SelectWidth re-scores with the
// generator's tier function and the requested round count).
func CandidateWidths(teams, fields, maxPositions int) []WidthOption {
	var opts []WidthOption
	for p := maxPositions; p >= 2; p-- {
		slots := fields * p
		if slots < 2 {
			continue
		}
		w := WidthOption{
			Positions:       p,
			Tier:            TierOf(teams, slots),
			OpponentCeiling: teams - 1,
			Waves:           wavesFor(teams, slots),
			LastWaveTeams:   lastWaveTeams(teams, slots),
		}
		if w.OpponentCeiling < 0 {
			w.OpponentCeiling = 0
		}
		w.Reason = widthReason(w.Tier, w.LastWaveTeams, slots)
		opts = append(opts, w)
	}
	return opts
}

// SelectWidth picks the active match width for a schedule of teams
// teams across fields fields with a physical maximum of maxPositions
// positions per field over rounds rounds, judging fit with tier
// (nil selects DivisorTier).
//
// Rules, in order: candidates narrower than 2 positions are rejected;
// a candidate whose opponent ceiling is below the widest candidate's
// is rejected, so degradation can never trade away matchups to fix a
// wave shape; the lowest tier wins; ties break to the widest. A solo
// wave is therefore only ever scheduled when no better-fitting width
// can keep the matchup ceiling.
func SelectWidth(teams, fields, maxPositions, rounds int, tier TierFunc) WidthReport {
	if tier == nil {
		tier = DivisorTier
	}
	report := WidthReport{Considered: CandidateWidths(teams, fields, maxPositions)}
	if len(report.Considered) == 0 {
		report.Chosen = WidthOption{
			Positions: max(maxPositions, 0),
			Tier:      tier(teams, fields*maxPositions),
			Reason:    "no narrower width is available",
		}
		return report
	}

	widest := report.Considered[0]
	for i := range report.Considered {
		o := &report.Considered[i]
		slots := fields * o.Positions
		o.Tier = tier(teams, slots)
		o.OpponentCeiling = OpponentCeiling(rounds, o.Positions, teams)
		o.Reason = widthReason(o.Tier, o.LastWaveTeams, slots)
	}

	best := widest
	for _, o := range report.Considered {
		if o.OpponentCeiling < widest.OpponentCeiling {
			continue
		}
		if o.Tier < best.Tier {
			best = o
		}
	}

	report.Chosen = best
	report.Degraded = best.Positions < widest.Positions
	if report.Degraded {
		report.Chosen.Reason = fmt.Sprintf("using %d of %d positions: %s",
			best.Positions, widest.Positions, best.Reason)
	}
	return report
}

func wavesFor(teams, slots int) int {
	if teams <= 0 || slots <= 0 {
		return 0
	}
	return (teams + slots - 1) / slots
}

func lastWaveTeams(teams, slots int) int {
	if teams <= 0 || slots <= 0 {
		return 0
	}
	if rem := teams % slots; rem != 0 {
		return rem
	}
	return slots
}

func widthReason(t WidthTier, last, slots int) string {
	switch t {
	case TierExact:
		return "the teams split into full waves"
	case TierShortWave:
		return fmt.Sprintf("the final wave plays only %d of %d slots", last, slots)
	default:
		return "the final wave leaves a single team with no opponent"
	}
}
