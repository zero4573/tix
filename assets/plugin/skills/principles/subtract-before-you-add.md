# Subtract before you add

Remove complexity first, then build on the simpler base.

- Delete dead code, redundant validators and stub references before adding anything.
- Get to the minimum before polishing it.
- Design for observed use, not speculative edge cases: no guards, parsers or validators beyond
  what the requirement asks for.
- Leave the design a little simpler than you found it, behind the same or a smaller surface.
