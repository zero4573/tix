# tidy: other shapes of rewrite

The tidy of `SKILL.md` rewrites the ticket branch's own commits above its branching point. These are
the cases where that boundary, or the way the commits are rebuilt, is another one. Each needs
the user's go-ahead through the gate first (they rewrite more than the usual tidy), and each
keeps the three rules: the final tree equals the old tip's tree byte for byte, the old tip is
kept at `refs/tidy-backup/<branch>`, and the branch is moved with
`git update-ref refs/heads/<branch> <new> <old>`. Signing is always the last step, on the
moved branch: `tix ws resign <ID> <slug>`.

## Reorders (any tidy that moves hunks between commits)
No go-ahead needed: a reorder can move hunks between commits without changing the final tree.
The task agent compares, per new commit, its path set with the path sets of the commits it was
built from (`git show --name-status --format= <sha>`), and reports every path that moved to
another commit (a deletion that now sits in an unrelated commit is the usual one). A branch
rebased onto a newer base has its branching point there too, but `origin/<branch>` then holds the
old commits, is no ancestor of the backup and the lease doesn't apply: the lead ends with a
`pending — user` line giving `git push --force-with-lease=<branch>:<old remote sha> origin <branch>`.

## Rewrite from the root
For a repository whose whole history is the ticket's (a new repo, or one being published for
the first time). Nothing is immutable, so the user names what must survive.
- List for the user, before anything is rewritten: every branch and tag of the repo and
  whether it points into the history to rewrite (`git for-each-ref --contains <root>`).
  Release branches and tags are theirs to decide: left as they are (the old history stays
  reachable from them) or moved with the rewrite.
- The scratch clone starts from a new empty root (`jj new 'root()'`), and the commits are
  rebuilt onto it. `main` and the ticket branch are moved together, each with its own backup
  ref and its own `update-ref <new> <old>`.
- The branching-point check of the lead's part doesn't apply; the tree-equality check does,
  for every moved branch.

## Transform (one script over every commit)
When every commit needs the same mechanical change (a path renamed throughout, a name
replaced in files and messages).
- The transform is one script kept in the ticket's `contracts/`, run on each commit's tree,
  paths and message in order; a commit it leaves empty is dropped.
- After the last transformed commit, add one **residual** commit whose tree is the old tip's
  tree with the same transform applied by hand-checked rules, so what the script missed is
  visible as a diff, not silently lost. An empty residual is the proof the script was complete.
- Derive every tree twice (two passes of the script from the original commits) and compare
  the tree ids: a script that depends on the working directory or on time gives different
  trees.
- The tree-equality rule compares the new tip with the transformed old tip, and the contract
  says so.

## Rebuild by state (one commit per capability)
When the history is too tangled to fold and the result should read as one commit per
capability or spec.
- Group the final files by capability. Per group, in dependency order: `jj new <previous>`,
  `jj restore --from <old tip> <the group's paths>`, describe.
- Files several groups share (a router, a registry, a hub file) grow with their groups at
  function level: restore the file from the old tip, then cut it back to what the groups so
  far need, so every commit builds.
- Build and run the repo's checks **at every commit** (`jj` has no rebase `--exec`: loop over
  the new commits with `jj new <rev>` and the check command, or `git rebase --exec` in a
  second scratch clone), not only at the tip.
- The last commit's tree must equal the old tip's tree; a difference is a file no group
  claimed.

## Signing by hand
`tix ws resign` is the way. Without it (an older `tix`), re-create each commit with the same
tree, parents, author, committer, dates and the **raw** message:
`git cat-file commit <sha> | sed '1,/^$/d' | git commit-tree -S <tree> -p <new parent>` with
`GIT_AUTHOR_*` and `GIT_COMMITTER_*` set from the old commit. `git log --format=%B` is not the
raw message: it adds a trailing newline, and every signed message then differs from its
original by one blank line.
