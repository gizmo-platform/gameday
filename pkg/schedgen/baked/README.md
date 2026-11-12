# Baked Schedules

This directory holds pre-computed schedule artifacts produced by:

    gameday bake schedules --ranges ...

Each `teams*-fields*-positions*-rounds*.yml` file is one annealed
schedule design for a fixed (teams, fields, positions) shape with a
round cap equal to its `rounds` field. The files are embedded into
the binary via `embed.FS` and consumed at runtime by the `Annealed`
generator, which truncates the design to the requested round count
and applies a fresh random team labeling.

To add schedules for new event sizes, run the bake command pointed at
this directory, commit the resulting files, and rebuild.

## Active Positions (degraded match width)

An artifact may carry an optional `active_positions` key naming the
match width it was annealed at, when that is narrower than the
physical `positions` per field. The key is omitted when the design uses
every position, so artifacts written before this feature parse unchanged
and are read as full width.

The `grid` is stored in **active layout** — each row has
`fields × active_positions` cells. Which *physical* position a match
occupies is not stored: it is re-derived from the wave index at
materialize time (`(active_position + wave) % positions`), so an artifact
and a live anneal of the same shape can never disagree, and the rotation
stays seed-deterministic.

A lookup only accepts an artifact whose active width equals the request's
resolved width. A mismatch is skipped and the request falls back to a
live anneal — a full-width artifact can never quietly undo a degradation
decision (or vice versa).

When dynamic position degradation shipped, 16 artifacts changed width and
were re-baked with `active_positions: 3`: teams 5, 6 and 9 on one field
over rounds 6–8; teams 12 on two fields over rounds 6–8; teams 15 on one
field over rounds 7–8; and teams 17 on one and two fields over round 8 —
all at four physical positions.
