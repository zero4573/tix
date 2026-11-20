# Test behavior not implementation

A test asserts what users of the code can observe: return values, errors, state they read
back, effects on outside systems. Private helpers, call order and intermediate data are
implementation.

Judge each test against two imagined edits:
- break the behaviour (a wrong value, a skipped or doubled effect): if the test still passes,
  it proves nothing;
- change the internals but keep every observable result: if the test fails, it is pinned to
  the implementation.

Keep the tests that catch the first and survive the second; rewrite or delete the rest. Derive
expected values from the requirement, not from the code under test. Prefer no new test over a
bad one (mostly mocks, mirrors the implementation, depends on timing).
