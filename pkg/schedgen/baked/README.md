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
