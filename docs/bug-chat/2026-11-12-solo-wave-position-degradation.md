# Solo waves: dynamic position degradation in schedgen

**Date:** 2026-11-12
**Area:** `pkg/schedgen` (Annealed, BID, baked artifacts), `modules/game` schedule UI

## The defect

Wave-based generators lay teams into fixed waves of `fields × positions`
seats. When the team count left a remainder of **1** in the last wave, that
one team played a *solo match*: no opponent at all, yet the system still
emitted a Match, a MatchPlacement, and a one-team scorecard for it.

The smallest example: 9 teams, 1 field, 4 positions, 6 rounds. The waves
split `4,4,1`, and annealing cannot fix what the structure caused — the best
opponent histogram it can reach is `4,4,1` (one team meets only one or two
opponents across the whole phase). Run the same shape at 3 positions and the
waves are `3,3,3`: every team plays two matches per round against real
opponents.

## The resolution

`pkg/schedgen/degradation.go` chooses the *active match width* by structural
arithmetic before a generator ever runs:

- **Tiers**: a width is `TierExact` when teams divide evenly into waves,
  `TierShortWave` when the last wave has ≥2 teams, `TierSoloWave` when the
  remainder is 1. Generators opt in with `WidthDegradable` (Annealed uses a
  divisor tier, BID a pairwise-divisibility tier); generators that don't
  implement it (RandomSeeding, OneShot, BEST Finals) are byte-identical to
  before.
- **Selection** (`ResolveWidth`): consider narrower widths, prefer the
  lowest tier, break ties toward the widest, never go below 2 seats per
  match.
- **Ceiling guard**: degradation is rejected when the narrower width cannot
  still reach the widest width's opponent ceiling
  (`min(rounds × (positions−1), teams−1)`). Narrowing may remove byes, never
  matchups. For 9 teams at rounds=3 the 4-wide ceiling (8) beats the 3-wide
  (6), so the shape stays 4-wide; at rounds=4 both ceilings are 8, so it
  degrades to 3. BID declares `CoversAllPairs()` because it adds matches,
  not rounds, so the guard is neutralized for it.
- **Operator override**: `Config.ActivePositions` pins a width explicitly;
  the game module's select-teams page offers a "Match Width" selector (only
  when the full width is ragged) and the preview page shows an info notice
  explaining a degradation. Nothing is ever blocked.
- **Physical rotation**: a 3-wide match rotates through the 4 physical
  positions by wave index (`(active + wave) % physical`), so location
  diversity keeps accruing and full-width behavior is unchanged byte-for-byte.
  The rotation is *derived*, never stored, so baked artifacts and live search
  cannot disagree.

## What changed underneath

- `Schedule.Score()`/`Validate()` now iterate `match.Placements` directly
  instead of scanning a `field × positions` grid — with rotation, teams sit
  at physical indices up to `positions−1`, and a grid bounded by the active
  width would silently miss them.
- Division interleave conflict-checks exact `(field, position)` seats rather
  than just fields, because one division may be degraded while another runs
  full width.
- Baked artifacts gained an optional `active_positions` key
  (`grid` stays in active layout); `LookupBaked` skips artifacts whose active
  width differs from the request so a stale artifact can never undo the
  resolver's decision.

## Impact

Of the 333 embedded artifacts, **16** change width under the resolver and
were re-baked with `active_positions: 3`: teams 5, 6, 9 (1 field, rounds
6–8), teams 12 (2 fields, rounds 6–8), teams 15 (1 field, rounds 7–8), and
teams 17 (1 and 2 fields, round 8) — all at 4 physical positions. The other
317 files are byte-identical.

## Caveat: cost is not comparable across shapes

`cost()` sums per-(team, location) usage² over the seat grid, so a narrower
shape mechanically scores worse (fewer seats, more reuse per physical
position) regardless of scheduling quality. That is why 9/1/4 rounds 6 shows
cost 7400 at width 3 versus 5500 at width 4 even though the degraded
schedule is strictly better for the teams. Cost ranks permutations *within*
a fixed width; it must never be used to choose the width itself — the tier
and ceiling arithmetic does that.
