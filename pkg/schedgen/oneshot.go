package schedgen

import (
	"log/slog"
	"math/rand"
)

func init() {
	RegisterGenerator("OneShot", NewOneShot)
	RegisterGeneratorConfig("OneShot", OneShotScheduleConfig{})
}

// OneShotScheduleConfig provides information on the configuration
// defaults for a scheduler.
type OneShotScheduleConfig struct{}

func (rsc OneShotScheduleConfig) MaxRounds() int { return 1 }

func (rsc OneShotScheduleConfig) DefaultRounds() int { return 1 }

func (rsc OneShotScheduleConfig) RoundsDynamic() bool { return false }

func NewOneShot(c Config) Generator {
	o := OneShotSchedule{
		Schedule: Schedule{
			Config:        c,
			ClosestReplay: 99,
		},
	}
	return &o
}

// OneShotSchedule contains the collection of matches in order.
type OneShotSchedule struct {
	Schedule
}

// Generate generates a candidate schedule.  The schedule is
// pre-scored on generation.
func (o *OneShotSchedule) Generate() *Schedule {
	r := Round{}
	r.TeamAppearances = make(map[int]int)

	slots := []Location{}
	for f := range o.Config.Fields {
		for p := range o.Config.Positions {
			slots = append(slots, Location{f, p})
		}
	}

	teams := rand.Perm(o.Config.Teams)
	match := 0
	placements := make(map[Location]int)
	for i := range o.Config.Teams {
		slot := slots[i%len(slots)]
		placements[slot] = teams[i]
		r.TeamAppearances[teams[i]] = match

		if i%len(slots) == len(slots)-1 {
			slog.Debug("Placements", "map", placements)
			r.Matches = append(r.Matches, Match{Placements: placements})
			placements = make(map[Location]int)
			match++
		}
	}
	if len(placements) > 0 {
		// This commits the leftover partial match that can
		// exist when the number of teams doesn't cleanly
		// divide into the available slots.
		r.Matches = append(r.Matches, Match{Placements: placements})
	}

	if len(r.TeamAppearances) != o.Config.Teams {
		slog.Error("SCHEDULER INVARIANT VIOLATION", "expected-count", o.Config.Teams, "actual-count", len(r.TeamAppearances))
	}

	seen := make(map[int]struct{})
	for _, m := range r.Matches {
		for _, t := range m.Placements {
			seen[t] = struct{}{}
		}
	}
	for t := range o.Config.Teams {
		if _, found := seen[t]; !found {
			slog.Error("Team does not appear in round!", "team", t)
		}
	}

	o.Rounds = []Round{r}

	return &o.Schedule
}

func (o *OneShotSchedule) MaxRounds() int { return 10 }

func (o *OneShotSchedule) DefaultRounds() int { return 1 }
