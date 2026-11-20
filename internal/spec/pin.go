package spec

import (
	"fmt"
	"strings"
	"time"

	"github.com/zero4573/tix/internal/gitx"
)

func PushedHead(dir, branch string) (sha string, ok bool, err error) {
	local, err := gitx.OutIn(dir, nil, "rev-parse", "-q", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil || local == "" {
		return "", false, fmt.Errorf("no branch %s in %s: %v", branch, dir, err)
	}
	remote, err := gitx.Out(dir, "rev-parse", "-q", "--verify", "refs/remotes/origin/"+branch+"^{commit}")
	if err != nil || remote != local {
		return "", false, nil
	}
	return local, true, nil
}

func Pin(v *Version, sha string, now time.Time) bool {
	if v.Commit == sha {
		return false
	}
	v.ClearPin()
	v.Commit, v.Pinned = sha, now.Format(TimeLayout)
	return true
}

func IsAncestor(dir, a, b string) bool {
	return gitx.Ok(dir, "merge-base", "--is-ancestor", a, b)
}

func hasCommit(dir, sha string) bool {
	return gitx.Ok(dir, "cat-file", "-e", sha+"^{commit}")
}

func patchIDs(dir, patch string) ([][2]string, error) {
	if strings.TrimSpace(patch) == "" {
		return nil, nil
	}
	out, err := gitx.OutIn(dir, strings.NewReader(patch), "patch-id", "--stable")
	if err != nil {
		return nil, err
	}
	var ids [][2]string
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) == 2 {
			ids = append(ids, [2]string{f[0], f[1]})
		}
	}
	return ids, nil
}

func BranchPatchID(dir, baseRef, commit string) (string, error) {
	mb, err := gitx.Out(dir, "merge-base", baseRef, commit)
	if err != nil || mb == "" {
		return "", fmt.Errorf("no merge base of %s and %s", baseRef, short(commit))
	}
	patch, err := gitx.Out(dir, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", mb, commit)
	if err != nil {
		return "", err
	}
	ids, err := patchIDs(dir, "commit "+commit+"\n"+patch+"\n")
	if err != nil || len(ids) == 0 {
		return "", err
	}
	return ids[0][0], nil
}

func ResolveMerged(dir, baseRef string, v Version) (string, error) {
	if v.Commit == "" || !hasCommit(dir, v.Commit) {
		return "", nil
	}
	if !gitx.HasRef(dir, baseRef+"^{commit}") {
		return "", fmt.Errorf("base %s does not resolve in %s", baseRef, dir)
	}
	if IsAncestor(dir, v.Commit, baseRef) {
		return v.Commit, nil
	}
	want, err := BranchPatchID(dir, baseRef, v.Commit)
	if err != nil || want == "" {
		return "", err
	}
	args := []string{"log", "-p", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--reverse", "--format=commit %H"}
	if since := sinceArg(v.Pinned); since != "" {
		args = append(args, "--since="+since)
	}
	patch, err := gitx.Out(dir, append(args, baseRef, "--")...)
	if err != nil {
		return "", err
	}
	ids, err := patchIDs(dir, patch)
	if err != nil {
		return "", err
	}
	for _, id := range ids {
		if id[0] == want {
			return id[1], nil
		}
	}
	return "", nil
}

func TagsContaining(dir, sha string) ([]string, error) {
	if sha == "" || !hasCommit(dir, sha) {
		return nil, nil
	}
	out, err := gitx.Out(dir, "tag", "--contains", sha, "--sort=version:refname")
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, t := range strings.Split(out, "\n") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags, nil
}

func Refresh(dir, baseRef string, v *Version) (bool, error) {
	if v.Commit == "" {
		return false, nil
	}
	changed := false
	if v.Merged == "" {
		m, err := ResolveMerged(dir, baseRef, *v)
		if err != nil {
			return false, err
		}
		if m != "" {
			v.Merged, changed = m, true
		}
	}
	at := v.Merged
	if at == "" {
		at = v.Commit
	}
	tags, err := TagsContaining(dir, at)
	if err != nil {
		return changed, err
	}
	if strings.Join(tags, "\n") != strings.Join(v.Tags, "\n") {
		v.Tags, changed = tags, true
	}
	return changed, nil
}

func VersionSides(vault, slug string, v Version) (before, after Tree, err error) {
	id := v.ID()
	if err := Pending(vault, id, slug); err != nil {
		return nil, nil, err
	}
	base, err := ReadSnapshot(BaseDir(vault, id, slug))
	if err != nil {
		return nil, nil, err
	}
	snap, err := ReadSnapshot(AfterDir(vault, id, slug))
	if err != nil {
		return nil, nil, err
	}
	switch {
	case snap != nil:
		before, after = Sides(snap, base, snap, Tree{}, v.Files)
		return before, after, nil
	case base != nil:
		projects, err := ReadTree(VaultDir(vault, slug))
		if err != nil {
			return nil, nil, err
		}
		return base, Overlay(base, Pick(projects, v.Files)), nil
	}
	return Tree{}, Tree{}, nil
}

func UnderCapability(t Tree, capability string) Tree {
	out := Tree{}
	for k, b := range t {
		if strings.HasPrefix(k, capability+"/") {
			out[k] = b
		}
	}
	return out
}

func sinceArg(pinned string) string {
	t, err := time.ParseInLocation(TimeLayout, pinned, time.Local)
	if err != nil {
		return ""
	}
	return t.Add(-48 * time.Hour).Format(time.RFC3339)
}

func Reapply(path, dir, baseRef, sha string, now time.Time) (v Version, pinned, changed bool, err error) {
	if v, err = ReadVersion(path); err != nil {
		return v, false, false, err
	}
	if sha != "" {
		pinned = Pin(&v, sha, now)
	}
	changed, err = Refresh(dir, baseRef, &v)
	return v, pinned, changed || pinned, err
}
