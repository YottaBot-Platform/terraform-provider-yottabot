# Changelog fragments

One file per pull request with a user-visible change, named after the PR number
(`1234.txt`). Fragments are assembled into `CHANGELOG.md` at release time, which
avoids every PR conflicting on the same lines of one file.

Use one of these categories, and write for an operator reading release notes —
not for a reviewer reading the diff:

```
```release-note:breaking-change
provider: `endpoint` no longer accepts a bare hostname; use a full URL.
```
```

Categories: `breaking-change`, `deprecation`, `feature`, `enhancement`, `bug`,
`security`, `note`.

Skip a fragment only for changes with no user-visible effect — refactors, test
additions, CI. Say so in the PR description when you do.

Write what changed and what the reader must do about it. "Fixed a bug in the
agent resource" tells them nothing; "`tool_ids` no longer reorders on refresh,
which was producing an empty diff on every plan" tells them whether they were
affected.
