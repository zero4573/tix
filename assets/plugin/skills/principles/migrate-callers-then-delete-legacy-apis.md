# Migrate callers then delete legacy apis

Migrate every caller and delete the old API in the same change, instead of keeping a
compatibility layer. Update the tests to assert the new contract and delete tests that only
protected the old implementation. Applies when no external user depends on the old API and the
code can take a coordinated breaking change.
